//go:build integration

package v1_test

// auth_integration_test.go is Phase 15.1's proof that the EVE SSO login
// flow works end to end over real HTTP against a real database — the
// missing piece Phase 15 left behind when it registered /auth/login and
// /auth/callback but passed a nil *sso.Flow into RegisterAuthRedirects,
// so both answered 501.
//
// The round trip proven here is exactly the one the roadmap names:
// BeginLogin -> callback -> session cookie -> an authenticated
// /api/v1/me call. It uses a stub EVE SSO server (JWKS + token endpoint,
// RS256-signed access tokens) rather than the live login.eveonline.com —
// the same approach internal/sso's own unit tests take — but everything
// on HANGAR's side is the production path: the real store, the real
// jwksSettingStore-equivalent adapter over app.setting, the real Huma
// router, and the real session-resolving middleware.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	neturl "net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/api"
	apimw "github.com/hangar-project/hangar/internal/api/middleware"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/hangar-project/hangar/internal/store"
)

// ---- stub EVE SSO ----

type stubSSO struct {
	srv      *httptest.Server
	priv     *rsa.PrivateKey
	kid      string
	clientID string
	charID   int64
	charName string
	owner    string
}

func newStubSSO(t *testing.T) *stubSSO {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	s := &stubSSO{
		priv: priv, kid: "phase151-kid", clientID: "phase151-client",
		charID: 2112625428, charName: "Phase Fifteen One", owner: "owner-hash-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/jwks", func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(s.priv.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
		_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":%q,"use":"sig","n":%q,"e":%q}]}`, s.kid, n, e)
	})
	mux.HandleFunc("/v2/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		user, _, ok := r.BasicAuth()
		if !ok || user != s.clientID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  s.signAccessToken(t),
			"refresh_token": "stub-refresh-token",
			"expires_in":    1200,
			"token_type":    "Bearer",
		})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubSSO) signAccessToken(t *testing.T) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": "login.eveonline.com",
		"sub": fmt.Sprintf("CHARACTER:EVE:%d", s.charID),
		"aud": []string{s.clientID, "EVE Online"},
		"exp": time.Now().Add(20 * time.Minute).Unix(),
		"nbf": time.Now().Add(-time.Minute).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
		"scp": "esi-characters.read_contacts.v1",
		"nam": s.charName, "name": s.charName, "owner": s.owner,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.priv)
	require.NoError(t, err)
	return signed
}

// storeSettingAdapter mirrors cmd/hangar/sso.go's jwksSettingStore. The
// production adapter lives in package main and can't be imported here, so
// this is a deliberate structural duplicate — if the jwks.SettingStore
// contract ever changes, BOTH fail to compile, which is the intended
// coupling.
type storeSettingAdapter struct{ s *store.Store }

func (a storeSettingAdapter) GetSetting(ctx context.Context, key string) (jwks.SettingRow, error) {
	row, err := a.s.GetSetting(ctx, key)
	if err != nil {
		return jwks.SettingRow{}, err
	}
	return jwks.SettingRow{Value: row.Value}, nil
}

func (a storeSettingAdapter) UpsertSetting(ctx context.Context, key string, value json.RawMessage, updatedBy uuid.NullUUID) error {
	return a.s.UpsertSetting(ctx, key, value, updatedBy)
}

func testKeyringFor(t *testing.T) *crypto.Keyring {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	kr, err := crypto.NewKeyring(config.CryptoConfig{
		MasterKey: config.NewSecret(base64.StdEncoding.EncodeToString(raw)), MasterKeyVersion: 1,
	})
	require.NoError(t, err)
	return kr
}

// TestSSOLoginRoundTripToAuthenticatedMe is Phase 15.1's named exit
// criterion for item 1.
func TestSSOLoginRoundTripToAuthenticatedMe(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	stub := newStubSSO(t)

	cache := jwks.NewCache(stub.srv.URL+"/oauth/jwks", storeSettingAdapter{s: s}, stub.srv.Client(), nil)
	require.NoError(t, cache.Refresh(ctx))
	verifier := jwks.NewVerifier(cache, jwks.VerifierConfig{
		Issuers:  []string{"login.eveonline.com", "https://login.eveonline.com"},
		ClientID: stub.clientID, Audience: "EVE Online",
	})

	flow := &sso.Flow{
		Store: s,
		OAuth: sso.OAuthConfig{
			ClientID: stub.clientID, ClientSecret: "phase151-secret",
			CallbackURL:  "http://127.0.0.1/auth/callback",
			AuthorizeURL: stub.srv.URL + "/v2/oauth/authorize",
			TokenURL:     stub.srv.URL + "/v2/oauth/token",
			HTTPClient:   stub.srv.Client(),
		},
		Verifier:   verifier,
		Keyring:    testKeyringFor(t),
		SessionTTL: 48 * time.Hour,
	}

	// The production serving stack: Huma API + auth redirects + the
	// session-resolving middleware, exactly as cmd/hangar/serve.go mounts
	// them.
	mux := http.NewServeMux()
	deps := api.Deps{Store: s, SSO: flow}
	hapi := api.NewAPI(mux, deps)
	v1.RegisterAll(hapi, deps)
	v1.RegisterAuthRedirects(mux, s, flow)
	srv := httptest.NewServer(api.Handler(mux, s))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{
		Jar: jar,
		// Don't follow the redirect to the SPA root — we want to inspect
		// each hop's Set-Cookie.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// ---- 1. GET /auth/login issues a pre-auth session cookie + redirect ----
	loginResp, err := client.Get(srv.URL + "/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()
	require.Equal(t, http.StatusFound, loginResp.StatusCode, "/auth/login must redirect, not 501 (the Phase 15 state)")

	redirectURL := loginResp.Header.Get("Location")
	require.Contains(t, redirectURL, "code_challenge_method=S256", "PKCE challenge must be present")

	var sessionCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == apimw.SessionCookieName {
			sessionCookie = c
		}
	}
	require.NotNil(t, sessionCookie, "/auth/login must set the session cookie")
	require.True(t, sessionCookie.HttpOnly, "the session cookie must be HttpOnly (§7.1)")
	preAuthExpiry := sessionCookie.Expires

	state := mustQueryParam(t, redirectURL, "state")
	require.NotEmpty(t, state)

	// ---- 2. GET /auth/callback completes the login ----
	cbResp, err := client.Get(srv.URL + "/auth/callback?code=stub-auth-code&state=" + state)
	require.NoError(t, err)
	defer cbResp.Body.Close()
	require.Equal(t, http.StatusFound, cbResp.StatusCode, "a valid callback must redirect, not error")

	var authedCookie *http.Cookie
	for _, c := range cbResp.Cookies() {
		if c.Name == apimw.SessionCookieName {
			authedCookie = c
		}
	}
	require.NotNil(t, authedCookie, "the callback must re-issue the session cookie with the authenticated expiry")
	require.True(t, authedCookie.Expires.After(preAuthExpiry.Add(time.Hour)),
		"the authenticated cookie must outlive the 10-minute pre-auth window (Phase 15.1 session-TTL fix)")

	// ---- 3. the cookie authenticates /api/v1/me ----
	meResp, err := client.Get(srv.URL + "/api/v1/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusOK, meResp.StatusCode, "the session cookie must authenticate /api/v1/me")

	var me struct {
		Data struct {
			DisplayName     string `json:"display_name"`
			MainCharacterID *int64 `json:"main_character_id"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&me))
	require.Equal(t, stub.charName, me.Data.DisplayName)
	require.NotNil(t, me.Data.MainCharacterID)
	require.Equal(t, stub.charID, *me.Data.MainCharacterID)

	// ---- 4. the character and its token really landed ----
	ch, err := s.GetCharacter(ctx, stub.charID)
	require.NoError(t, err)
	require.Equal(t, stub.charName, ch.Name)
	tok, err := s.GetCharacterToken(ctx, stub.charID)
	require.NoError(t, err)
	require.True(t, tok.Valid, "the envelope-encrypted refresh token must be persisted and valid")
}

// TestSSOCallbackReplayDoesNotLogTheUserOut covers the roadmap's stated
// edge case: "The SSO callback route must consume the `state` cookie
// exactly once and handle the back-button replay without erroring the
// user out."
func TestSSOCallbackReplayDoesNotLogTheUserOut(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	stub := newStubSSO(t)

	cache := jwks.NewCache(stub.srv.URL+"/oauth/jwks", storeSettingAdapter{s: s}, stub.srv.Client(), nil)
	require.NoError(t, cache.Refresh(ctx))
	flow := &sso.Flow{
		Store: s,
		OAuth: sso.OAuthConfig{
			ClientID: stub.clientID, ClientSecret: "phase151-secret",
			CallbackURL:  "http://127.0.0.1/auth/callback",
			AuthorizeURL: stub.srv.URL + "/v2/oauth/authorize",
			TokenURL:     stub.srv.URL + "/v2/oauth/token",
			HTTPClient:   stub.srv.Client(),
		},
		Verifier: jwks.NewVerifier(cache, jwks.VerifierConfig{
			Issuers:  []string{"login.eveonline.com", "https://login.eveonline.com"},
			ClientID: stub.clientID, Audience: "EVE Online",
		}),
		Keyring:    testKeyringFor(t),
		SessionTTL: 48 * time.Hour,
	}

	mux := http.NewServeMux()
	deps := api.Deps{Store: s, SSO: flow}
	hapi := api.NewAPI(mux, deps)
	v1.RegisterAll(hapi, deps)
	v1.RegisterAuthRedirects(mux, s, flow)
	srv := httptest.NewServer(api.Handler(mux, s))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	loginResp, err := client.Get(srv.URL + "/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()
	state := mustQueryParam(t, loginResp.Header.Get("Location"), "state")

	cbURL := srv.URL + "/auth/callback?code=stub-auth-code&state=" + state
	first, err := client.Get(cbURL)
	require.NoError(t, err)
	defer first.Body.Close()
	require.Equal(t, http.StatusFound, first.StatusCode)

	// The back button re-issues the exact same callback GET. `state` is
	// single-use (CompleteSessionLogin nulls it), so HandleCallback fails
	// — but the user IS logged in, and must not be thrown out.
	replay, err := client.Get(cbURL)
	require.NoError(t, err)
	defer replay.Body.Close()
	require.Equal(t, http.StatusFound, replay.StatusCode,
		"a back-button callback replay must redirect a still-logged-in user, not 401 them out of a working session")

	// And the session still works.
	meResp, err := client.Get(srv.URL + "/api/v1/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusOK, meResp.StatusCode, "the session must survive a callback replay")
}

// TestSSOCallbackWithoutSessionIsRejected proves the replay tolerance
// above does NOT weaken the unauthenticated case.
func TestSSOCallbackWithoutSessionIsRejected(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	stub := newStubSSO(t)

	cache := jwks.NewCache(stub.srv.URL+"/oauth/jwks", storeSettingAdapter{s: s}, stub.srv.Client(), nil)
	require.NoError(t, cache.Refresh(context.Background()))
	flow := &sso.Flow{
		Store: s,
		OAuth: sso.OAuthConfig{
			ClientID: stub.clientID, ClientSecret: "phase151-secret",
			CallbackURL: "http://127.0.0.1/auth/callback",
			TokenURL:    stub.srv.URL + "/v2/oauth/token", HTTPClient: stub.srv.Client(),
		},
		Verifier: jwks.NewVerifier(cache, jwks.VerifierConfig{
			Issuers: []string{"login.eveonline.com"}, ClientID: stub.clientID, Audience: "EVE Online",
		}),
		Keyring: testKeyringFor(t),
	}

	mux := http.NewServeMux()
	deps := api.Deps{Store: s, SSO: flow}
	hapi := api.NewAPI(mux, deps)
	v1.RegisterAll(hapi, deps)
	v1.RegisterAuthRedirects(mux, s, flow)
	srv := httptest.NewServer(api.Handler(mux, s))
	t.Cleanup(srv.Close)

	// No cookie at all.
	resp, err := http.Get(srv.URL + "/auth/callback?code=x&state=y")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a callback with no session cookie must be rejected")

	// A cookie pointing at a session that does not exist.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/auth/callback?code=x&state=y", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: apimw.SessionCookieName, Value: uuid.NewString()})
	resp2, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp2.StatusCode, "an unknown session must 401, never redirect as if logged in")
}

func mustQueryParam(t *testing.T, rawURL, key string) string {
	t.Helper()
	u, err := neturl.Parse(rawURL)
	require.NoError(t, err)
	return u.Query().Get(key)
}
