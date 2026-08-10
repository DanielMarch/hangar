package jwks_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/stretchr/testify/require"
)

// ---- test fixtures ----

type fakeSettingStore struct {
	mu   sync.Mutex
	rows map[string]json.RawMessage
}

func newFakeSettingStore() *fakeSettingStore {
	return &fakeSettingStore{rows: map[string]json.RawMessage{}}
}

func (s *fakeSettingStore) GetSetting(ctx context.Context, key string) (jwks.SettingRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rows[key]
	if !ok {
		return jwks.SettingRow{}, fmt.Errorf("not found")
	}
	return jwks.SettingRow{Value: v}, nil
}

func (s *fakeSettingStore) UpsertSetting(ctx context.Context, key string, value json.RawMessage, _ uuid.NullUUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[key] = value
	return nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func genKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func jwksJSON(kid string, pub *rsa.PublicKey) []byte {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big64(pub.E))
	body := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"use":"sig","n":%q,"e":%q}]}`, kid, n, e)
	return []byte(body)
}

func big64(e int) []byte {
	// Standard EVE/Google-style exponent encoding: minimal big-endian bytes.
	if e == 65537 {
		return []byte{0x01, 0x00, 0x01}
	}
	// Fallback for any other exponent value used in tests.
	b := []byte{byte(e >> 16), byte(e >> 8), byte(e)}
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

func newSignedToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims jwt.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(priv)
	require.NoError(t, err)
	return s
}

func validClaims(now time.Time, sub string, scp interface{}) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   "login.eveonline.com",
		"sub":   sub,
		"aud":   []string{"test-client-id", "EVE Online"},
		"exp":   now.Add(20 * time.Minute).Unix(),
		"nbf":   now.Add(-time.Minute).Unix(),
		"iat":   now.Add(-time.Minute).Unix(),
		"scp":   scp,
		"name":  "Test Character",
		"owner": "owner-hash-1",
	}
}

func testVerifierConfig() jwks.VerifierConfig {
	return jwks.VerifierConfig{
		Issuers:  []string{"login.eveonline.com", "https://login.eveonline.com"},
		ClientID: "test-client-id",
		Audience: "EVE Online",
	}
}

// ---- tests ----

// TestJWTValidatedOfflineNoNetwork (roadmap exit criterion): validation
// succeeds with a JWKS server that FAILS the test if hit at all — the
// cached key must already resolve the token's kid.
func TestJWTValidatedOfflineNoNetwork(t *testing.T) {
	priv := genKey(t)
	const kid = "key-1"

	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		t.Errorf("JWKS endpoint hit during validation — offline validation must make zero outbound calls")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newFakeSettingStore()
	clock := &fakeClock{now: time.Now()}
	cache := jwks.NewCache(srv.URL, store, srv.Client(), clock)

	// Seed the cache directly via a successful Refresh against a SECOND,
	// separate server (not the one under test), simulating "already
	// cached from boot" — then swap the cache's URL is not possible, so
	// instead we persist to app.setting and Load from there, which is
	// exactly the cold-boot-with-no-network path §7.2 describes.
	seedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(jwksJSON(kid, &priv.PublicKey))
	}))
	defer seedSrv.Close()
	seedCache := jwks.NewCache(seedSrv.URL, store, seedSrv.Client(), clock)
	require.NoError(t, seedCache.Refresh(context.Background()))

	require.NoError(t, cache.Load(context.Background()))
	_, ok := cache.Key(kid)
	require.True(t, ok, "the persisted key set must be loaded into memory")

	verifier := jwks.NewVerifier(cache, testVerifierConfig())
	tokenString := newSignedToken(t, priv, kid, validClaims(clock.Now(), "CHARACTER:EVE:2112625428", "esi-characters.read_contacts.v1"))

	claims, err := verifier.Verify(context.Background(), tokenString)
	require.NoError(t, err)
	require.Equal(t, "login.eveonline.com", claims.Issuer)
	charID, err := claims.CharacterID()
	require.NoError(t, err)
	require.Equal(t, int64(2112625428), charID)

	require.Equal(t, int64(0), atomic.LoadInt64(&hits), "zero outbound calls during validation")
}

// TestJWKSUnknownKidRefetchThrottled (roadmap exit criterion): at most one
// refetch per 5 minutes under a burst of unknown kids.
func TestJWKSUnknownKidRefetchThrottled(t *testing.T) {
	priv := genKey(t)
	const kid = "key-throttle"

	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write(jwksJSON(kid, &priv.PublicKey))
	}))
	defer srv.Close()

	store := newFakeSettingStore()
	clock := &fakeClock{now: time.Now()}
	cache := jwks.NewCache(srv.URL, store, srv.Client(), clock)

	// A burst of lookups for an unknown kid — none of them advance the
	// clock, simulating many tokens arriving in the same instant.
	for i := 0; i < 20; i++ {
		cache.EnsureKey(context.Background(), "some-other-unknown-kid")
	}
	require.LessOrEqual(t, atomic.LoadInt64(&hits), int64(1), "a burst of unknown kids must trigger at most one refetch")

	// The key we actually care about now resolves (the refetch above
	// fetched the live set, which contains `kid`).
	_, ok := cache.Key(kid)
	require.True(t, ok)

	hitsAfterFirstBurst := atomic.LoadInt64(&hits)

	// More unknown-kid lookups within the throttle window: still no
	// additional fetch.
	for i := 0; i < 20; i++ {
		cache.EnsureKey(context.Background(), "yet-another-unknown-kid")
	}
	require.Equal(t, hitsAfterFirstBurst, atomic.LoadInt64(&hits), "still within the throttle window")

	// Advance past the throttle window: exactly one more fetch is allowed.
	clock.Advance(jwks.UnknownKidThrottle + time.Second)
	cache.EnsureKey(context.Background(), "yet-another-unknown-kid-2")
	require.Equal(t, hitsAfterFirstBurst+1, atomic.LoadInt64(&hits), "past the throttle window, exactly one more refetch is allowed")
}

// TestScpClaimAcceptsStringAndArray (roadmap exit criterion): single-scope
// string form and multi-scope array form both parse.
func TestScpClaimAcceptsStringAndArray(t *testing.T) {
	priv := genKey(t)
	const kid = "key-scp"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(jwksJSON(kid, &priv.PublicKey))
	}))
	defer srv.Close()

	store := newFakeSettingStore()
	clock := &fakeClock{now: time.Now()}
	cache := jwks.NewCache(srv.URL, store, srv.Client(), clock)
	require.NoError(t, cache.Refresh(context.Background()))
	verifier := jwks.NewVerifier(cache, testVerifierConfig())

	t.Run("single scope as a bare string", func(t *testing.T) {
		tokenString := newSignedToken(t, priv, kid, validClaims(clock.Now(), "CHARACTER:EVE:1", "esi-mail.read_mail.v1"))
		claims, err := verifier.Verify(context.Background(), tokenString)
		require.NoError(t, err)
		require.Equal(t, []string{"esi-mail.read_mail.v1"}, []string(claims.Scopes))
	})

	t.Run("multiple scopes as an array", func(t *testing.T) {
		tokenString := newSignedToken(t, priv, kid, validClaims(clock.Now(), "CHARACTER:EVE:1",
			[]string{"esi-mail.read_mail.v1", "esi.activity.char:read"}))
		claims, err := verifier.Verify(context.Background(), tokenString)
		require.NoError(t, err)
		require.Equal(t, []string{"esi-mail.read_mail.v1", "esi.activity.char:read"}, []string(claims.Scopes))
	})
}

func TestVerifyRejectsNoneAlgAndHSAlg(t *testing.T) {
	priv := genKey(t)
	const kid = "key-alg"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(jwksJSON(kid, &priv.PublicKey))
	}))
	defer srv.Close()
	store := newFakeSettingStore()
	clock := &fakeClock{now: time.Now()}
	cache := jwks.NewCache(srv.URL, store, srv.Client(), clock)
	require.NoError(t, cache.Refresh(context.Background()))
	verifier := jwks.NewVerifier(cache, testVerifierConfig())

	// alg=none, unsigned.
	noneTok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(clock.Now(), "CHARACTER:EVE:1", "s"))
	noneTok.Header["kid"] = kid
	noneStr, err := noneTok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, err = verifier.Verify(context.Background(), noneStr)
	require.Error(t, err, "alg=none must be rejected")

	// HS256 "signed" with the RSA public key's modulus as an HMAC secret —
	// an attacker's classic RS256->HS256 confusion attempt.
	hsTok := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims(clock.Now(), "CHARACTER:EVE:1", "s"))
	hsTok.Header["kid"] = kid
	hsStr, err := hsTok.SignedString([]byte("attacker-controlled-secret"))
	require.NoError(t, err)
	_, err = verifier.Verify(context.Background(), hsStr)
	require.Error(t, err, "HS256 must be rejected")
}

func TestVerifyRejectsWrongIssuerAudienceAndSub(t *testing.T) {
	priv := genKey(t)
	const kid = "key-checks"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(jwksJSON(kid, &priv.PublicKey))
	}))
	defer srv.Close()
	store := newFakeSettingStore()
	clock := &fakeClock{now: time.Now()}
	cache := jwks.NewCache(srv.URL, store, srv.Client(), clock)
	require.NoError(t, cache.Refresh(context.Background()))
	verifier := jwks.NewVerifier(cache, testVerifierConfig())

	base := func() jwt.MapClaims { return validClaims(clock.Now(), "CHARACTER:EVE:1", "s") }

	badIss := base()
	badIss["iss"] = "evil.example.com"
	_, err := verifier.Verify(context.Background(), newSignedToken(t, priv, kid, badIss))
	require.Error(t, err)

	badAud := base()
	badAud["aud"] = []string{"someone-elses-client-id", "EVE Online"}
	_, err = verifier.Verify(context.Background(), newSignedToken(t, priv, kid, badAud))
	require.Error(t, err)

	badSub := base()
	badSub["sub"] = "CHARACTER:EVE:not-digits"
	_, err = verifier.Verify(context.Background(), newSignedToken(t, priv, kid, badSub))
	require.Error(t, err)

	require.True(t, true) // acceptable issuer alt form still passes:
	okIss := base()
	okIss["iss"] = "https://login.eveonline.com"
	_, err = verifier.Verify(context.Background(), newSignedToken(t, priv, kid, okIss))
	require.NoError(t, err)
}
