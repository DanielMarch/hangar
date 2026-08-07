package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RefreshInterval is the background JWKS refresh cadence
// (01_ARCHITECTURE.md §7.2). UnknownKidThrottle bounds how often an
// unknown kid may trigger an on-demand refetch — unthrottled, a burst of
// tokens signed with a not-yet-cached key would be a self-inflicted DoS
// against login.eveonline.com.
const (
	RefreshInterval    = 12 * time.Hour
	UnknownKidThrottle = 5 * time.Minute

	// SettingKey is the app.setting row the fetched key set is persisted
	// under, so a cold boot with no network still validates existing
	// sessions (§7.2).
	SettingKey = "sso.jwks_cache"
)

// Clock abstracts time for testability — the same one-method pattern
// internal/esi/{ratelimit,breaker} use, kept local rather than shared
// since importing either of those packages here would be a layering
// violation with nothing to justify it.
type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SettingStore is the subset of gen.Querier the JWKS cache needs for
// cold-boot persistence.
type SettingStore interface {
	GetSetting(ctx context.Context, key string) (SettingRow, error)
	UpsertSetting(ctx context.Context, key string, value json.RawMessage, updatedBy uuid.NullUUID) error
}

// SettingRow mirrors gen.AppSetting's Value field structurally, so this
// package doesn't need to import internal/store/gen directly.
type SettingRow struct {
	Value json.RawMessage
}

// rawJWKS is the wire format https://login.eveonline.com/oauth/jwks
// returns and what gets persisted verbatim to app.setting.
type rawJWKS struct {
	Keys []rawJWK `json:"keys"`
}

type rawJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// Cache is the in-process JWKS key set: fetched at boot, refreshed every
// RefreshInterval, and refetched on an unknown kid throttled to
// UnknownKidThrottle. Reads (Key) never touch the network — only Refresh
// and the throttled path inside EnsureKey do.
type Cache struct {
	url        string
	httpClient *http.Client
	store      SettingStore
	clock      Clock

	mu                  sync.RWMutex
	keys                map[string]*rsa.PublicKey
	lastUnknownKidFetch time.Time
}

// NewCache constructs a Cache. httpClient defaults to http.DefaultClient
// when nil; clock defaults to the system clock when nil.
func NewCache(url string, store SettingStore, httpClient *http.Client, clock Clock) *Cache {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Cache{url: url, httpClient: httpClient, store: store, clock: clock, keys: make(map[string]*rsa.PublicKey)}
}

// Key looks up kid in the in-memory cache only — no network, ever. This is
// the path every token validation takes (TestJWTValidatedOfflineNoNetwork).
func (c *Cache) Key(kid string) (*rsa.PublicKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.keys[kid]
	return k, ok
}

// Load reads the persisted key set from app.setting (SettingKey) into
// memory, so a cold boot with no network still validates existing
// sessions. A missing setting row is not an error — it means this is the
// installation's very first boot, and Refresh (called separately, at boot,
// with network available) is what seeds it.
func (c *Cache) Load(ctx context.Context) error {
	row, err := c.store.GetSetting(ctx, SettingKey)
	if err != nil {
		return nil //nolint:nilerr // absent setting row == first boot, not a failure
	}
	var raw rawJWKS
	if err := json.Unmarshal(row.Value, &raw); err != nil {
		return fmt.Errorf("jwks: load: unmarshalling persisted key set: %w", err)
	}
	keys, err := parseKeys(raw)
	if err != nil {
		return fmt.Errorf("jwks: load: %w", err)
	}
	c.mu.Lock()
	c.keys = keys
	c.mu.Unlock()
	return nil
}

// Refresh fetches the live key set from c.url, replaces the in-memory
// cache, and persists it to app.setting.
func (c *Cache) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("jwks: refresh: building request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jwks: refresh: fetching %s: %w", c.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks: refresh: %s returned %d", c.url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("jwks: refresh: reading body: %w", err)
	}

	var raw rawJWKS
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("jwks: refresh: unmarshalling: %w", err)
	}
	keys, err := parseKeys(raw)
	if err != nil {
		return fmt.Errorf("jwks: refresh: %w", err)
	}

	c.mu.Lock()
	c.keys = keys
	c.mu.Unlock()

	if err := c.store.UpsertSetting(ctx, SettingKey, json.RawMessage(body), uuid.NullUUID{}); err != nil {
		return fmt.Errorf("jwks: refresh: persisting: %w", err)
	}
	return nil
}

// EnsureKey looks kid up in memory; on a miss it refetches the live key
// set — but only if the last unknown-kid-triggered refetch was more than
// UnknownKidThrottle ago (TestJWKSUnknownKidRefetchThrottled). A burst of
// tokens signed with the same not-yet-cached kid therefore costs at most
// one network round trip per throttle window, never one per token.
func (c *Cache) EnsureKey(ctx context.Context, kid string) (*rsa.PublicKey, bool) {
	if k, ok := c.Key(kid); ok {
		return k, true
	}

	c.mu.Lock()
	now := c.clock.Now()
	if now.Sub(c.lastUnknownKidFetch) < UnknownKidThrottle {
		c.mu.Unlock()
		return nil, false
	}
	c.lastUnknownKidFetch = now
	c.mu.Unlock()

	if err := c.Refresh(ctx); err != nil {
		return nil, false
	}
	return c.Key(kid)
}

// Run blocks, refreshing every RefreshInterval until ctx is cancelled. An
// initial fetch happens immediately rather than waiting a full interval so
// a fresh boot with network available doesn't run on a stale/empty cache
// for up to 12h.
func (c *Cache) Run(ctx context.Context) {
	_ = c.Refresh(ctx) // best-effort; a failure here leaves Load's persisted set (if any) in place
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.Refresh(ctx)
		}
	}
}

func parseKeys(raw rawJWKS) (map[string]*rsa.PublicKey, error) {
	keys := make(map[string]*rsa.PublicKey, len(raw.Keys))
	for _, k := range raw.Keys {
		if k.Kty != "RSA" {
			continue // EVE SSO's JWKS is RSA-only; a future non-RSA entry is skipped, not an error
		}
		pub, err := parseRSAKey(k)
		if err != nil {
			return nil, fmt.Errorf("kid %q: %w", k.Kid, err)
		}
		keys[k.Kid] = pub
	}
	return keys, nil
}

func parseRSAKey(k rawJWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding e: %w", err)
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, fmt.Errorf("exponent too large")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}
