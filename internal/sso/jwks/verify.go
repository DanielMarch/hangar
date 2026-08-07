package jwks

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Skew is the clock-skew allowance §7.2 specifies for exp/nbf/iat.
const Skew = 60 * time.Second

// VerifierConfig is the caller-supplied validation policy —
// 01_ARCHITECTURE.md §7.2's fixed checks, not tunables a caller can loosen.
type VerifierConfig struct {
	// Issuers is the configured issuer set — accept both
	// "login.eveonline.com" and "https://login.eveonline.com".
	Issuers []string
	// ClientID is HANGAR's own EVE SSO application client id; aud must
	// contain it.
	ClientID string
	// Audience is the second, fixed required audience entry: "EVE Online".
	Audience string
}

// Verifier performs offline JWT validation against a Cache — no network
// round trip on the common path, and never a call to the retired /verify
// endpoint (01_ARCHITECTURE.md §7.2: "There is no verification endpoint
// and internal/sso must contain no code path that could call one").
type Verifier struct {
	cache    *Cache
	cfg      VerifierConfig
	timeFunc func() time.Time // nil means jwt's own default (time.Now)
}

// NewVerifier constructs a Verifier bound to cache and cfg.
func NewVerifier(cache *Cache, cfg VerifierConfig) *Verifier {
	return &Verifier{cache: cache, cfg: cfg}
}

// WithTimeFunc overrides the clock exp/nbf/iat are checked against — test
// hook only; production always uses jwt's own time.Now-based default.
func (v *Verifier) WithTimeFunc(f func() time.Time) *Verifier {
	v.timeFunc = f
	return v
}

// Verify validates tokenString and returns its claims. ctx is used only if
// an unknown kid forces a throttled JWKS refetch (EnsureKey) — the common
// case, a known kid, makes zero outbound calls.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (*Claims, error) {
	claims := &Claims{}
	keyfunc := func(token *jwt.Token) (interface{}, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, fmt.Errorf("jwks: token has no kid header")
		}
		key, ok := v.cache.EnsureKey(ctx, kid)
		if !ok {
			return nil, fmt.Errorf("jwks: kid %q not found in the cached key set", kid)
		}
		return key, nil
	}

	// WithValidMethods enforces alg=RS256 and rejects "none"/HS* outright —
	// jwt-go otherwise happily accepts whatever alg the token claims.
	// WithIssuedAt turns on iat validation (off by default in jwt/v5);
	// WithExpirationRequired refuses a token that omits exp rather than
	// treating a missing expiry as "never expires".
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithLeeway(Skew),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
	}
	if v.timeFunc != nil {
		opts = append(opts, jwt.WithTimeFunc(v.timeFunc))
	}
	token, err := jwt.ParseWithClaims(tokenString, claims, keyfunc, opts...)
	if err != nil {
		return nil, fmt.Errorf("jwks: verify: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("jwks: verify: token not valid")
	}

	if err := v.checkIssuer(claims); err != nil {
		return nil, err
	}
	if err := v.checkAudience(claims); err != nil {
		return nil, err
	}
	if _, err := claims.CharacterID(); err != nil {
		return nil, fmt.Errorf("jwks: verify: %w", err)
	}

	return claims, nil
}

func (v *Verifier) checkIssuer(claims *Claims) error {
	for _, iss := range v.cfg.Issuers {
		if claims.Issuer == iss {
			return nil
		}
	}
	return fmt.Errorf("jwks: verify: iss %q not in configured issuer set %v", claims.Issuer, v.cfg.Issuers)
}

func (v *Verifier) checkAudience(claims *Claims) error {
	if !audienceContains(claims.Audience, v.cfg.Audience) {
		return fmt.Errorf("jwks: verify: aud %v does not contain %q", claims.Audience, v.cfg.Audience)
	}
	if !audienceContains(claims.Audience, v.cfg.ClientID) {
		return fmt.Errorf("jwks: verify: aud %v does not contain the configured client id", claims.Audience)
	}
	return nil
}
