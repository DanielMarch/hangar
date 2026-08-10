package sso_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/stretchr/testify/require"
)

// ---- in-memory fake Store ----

type fakeStore struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]gen.AppSession
	users    map[uuid.UUID]gen.AppUser
	chars    map[int64]gen.AppCharacter
	tokens   map[int64]gen.AppCharacterToken
	scopes   map[int64]map[string]struct{}
	esiScope map[string]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions: map[uuid.UUID]gen.AppSession{},
		users:    map[uuid.UUID]gen.AppUser{},
		chars:    map[int64]gen.AppCharacter{},
		tokens:   map[int64]gen.AppCharacterToken{},
		scopes:   map[int64]map[string]struct{}{},
		esiScope: map[string]bool{},
	}
}

func (f *fakeStore) CreateSession(ctx context.Context, arg gen.CreateSessionParams) (gen.AppSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := gen.AppSession{
		SessionID: uuid.New(), UserID: arg.UserID, PkceVerifier: arg.PkceVerifier,
		State: arg.State, UserAgent: arg.UserAgent, ExpiresAt: arg.ExpiresAt, CreatedAt: time.Now(),
	}
	f.sessions[s.SessionID] = s
	return s, nil
}

func (f *fakeStore) GetSession(ctx context.Context, sessionID uuid.UUID) (gen.AppSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[sessionID]
	if !ok || s.ExpiresAt.Before(time.Now()) {
		return gen.AppSession{}, fmt.Errorf("not found")
	}
	return s, nil
}

func (f *fakeStore) CompleteSessionLogin(ctx context.Context, sessionID uuid.UUID, userID uuid.NullUUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[sessionID]
	s.UserID = userID
	s.PkceVerifier = nil
	s.State = nil
	f.sessions[sessionID] = s
	return nil
}

func (f *fakeStore) DeleteSession(ctx context.Context, sessionID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, sessionID)
	return nil
}

func (f *fakeStore) GetCharacter(ctx context.Context, characterID int64) (gen.AppCharacter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.chars[characterID]
	if !ok {
		return gen.AppCharacter{}, fmt.Errorf("not found")
	}
	return c, nil
}

func (f *fakeStore) UpsertCharacter(ctx context.Context, arg gen.UpsertCharacterParams) (gen.AppCharacter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := gen.AppCharacter{
		CharacterID: arg.CharacterID, UserID: arg.UserID, Name: arg.Name,
		CorporationID: arg.CorporationID, AllianceID: arg.AllianceID, OwnerHash: arg.OwnerHash,
	}
	f.chars[arg.CharacterID] = c
	return c, nil
}

func (f *fakeStore) CreateUser(ctx context.Context, displayName string) (gen.AppUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := gen.AppUser{UserID: uuid.New(), DisplayName: displayName, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.users[u.UserID] = u
	return u, nil
}

func (f *fakeStore) GetUser(ctx context.Context, userID uuid.UUID) (gen.AppUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return gen.AppUser{}, fmt.Errorf("not found")
	}
	return u, nil
}

func (f *fakeStore) SetUserMainCharacter(ctx context.Context, userID uuid.UUID, mainCharacterID *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[userID]
	u.MainCharacterID = mainCharacterID
	f.users[userID] = u
	return nil
}

func (f *fakeStore) TouchUserLastLogin(ctx context.Context, userID uuid.UUID) error {
	return nil
}

func (f *fakeStore) GetCharacterToken(ctx context.Context, characterID int64) (gen.AppCharacterToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[characterID]
	if !ok {
		return gen.AppCharacterToken{}, fmt.Errorf("not found")
	}
	return t, nil
}

func (f *fakeStore) UpsertCharacterToken(ctx context.Context, arg gen.UpsertCharacterTokenParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	f.tokens[arg.CharacterID] = gen.AppCharacterToken{
		CharacterID: arg.CharacterID, KeyVersion: arg.KeyVersion, WrappedDek: arg.WrappedDek,
		Nonce: arg.Nonce, Ciphertext: arg.Ciphertext, AccessExpiresAt: arg.AccessExpiresAt,
		Valid: true, OwnerHash: arg.OwnerHash, LastRefreshedAt: &now,
	}
	return nil
}

func (f *fakeStore) InvalidateCharacterToken(ctx context.Context, characterID int64, invalidReason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tokens[characterID]
	t.Valid = false
	t.InvalidReason = invalidReason
	f.tokens[characterID] = t
	return nil
}

func (f *fakeStore) ReplaceCharacterTokenScopes(ctx context.Context, characterID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes[characterID] = map[string]struct{}{}
	return nil
}

func (f *fakeStore) AddCharacterTokenScope(ctx context.Context, characterID int64, scope string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scopes[characterID] == nil {
		f.scopes[characterID] = map[string]struct{}{}
	}
	f.scopes[characterID][scope] = struct{}{}
	return nil
}

func (f *fakeStore) UpsertEsiScope(ctx context.Context, scope string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.esiScope[scope] = true
	return nil
}

var _ sso.Store = (*fakeStore)(nil)

// ---- test fixtures shared with the fake EVE SSO server ----

type fakeSSOServer struct {
	srv      *httptest.Server
	priv     *rsa.PrivateKey
	kid      string
	clientID string

	mu             sync.Mutex
	nextChar       int64
	ownerHash      string
	grantResponses []string // if non-empty, popped in order as forced grant errors instead of a normal response
	tokenCalls     int
}

func newFakeSSOServer(t *testing.T, clientID string) *fakeSSOServer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeSSOServer{priv: priv, kid: "test-kid", clientID: clientID, nextChar: 2112625428, ownerHash: "owner-hash-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(f.priv.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
		_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":%q,"use":"sig","n":%q,"e":%q}]}`, f.kid, n, e)
	})
	mux.HandleFunc("/v2/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		user, pass, ok := r.BasicAuth()
		if !ok || user != f.clientID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = pass

		f.mu.Lock()
		f.tokenCalls++
		charID := f.nextChar
		owner := f.ownerHash
		var forcedGrantErr string
		if len(f.grantResponses) > 0 {
			forcedGrantErr = f.grantResponses[0]
			f.grantResponses = f.grantResponses[1:]
		}
		f.mu.Unlock()

		if forcedGrantErr != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": forcedGrantErr, "error_description": "forced by test"})
			return
		}

		access := f.signAccessToken(t, charID, owner, "Test Character", "esi-characters.read_contacts.v1")
		resp := map[string]any{
			"access_token": access, "refresh_token": "refresh-" + uuid.NewString(),
			"expires_in": 1200, "token_type": "Bearer",
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeSSOServer) setOwnerHash(h string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ownerHash = h
}

// forceNextGrantError and callCount are used ONLY by
// refresh_integration_test.go, which is behind the `integration` build tag.
// golangci-lint's `unused` linter runs untagged and therefore reports both
// as dead — they are not. Deleting them (as that report invites) breaks
// `go build -tags=integration`, which is how this was caught. The
// //nolint directives below record that, so the next lint sweep does not
// repeat the mistake.
//
//nolint:unused // used by refresh_integration_test.go (build tag: integration)
func (f *fakeSSOServer) forceNextGrantError(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantResponses = append(f.grantResponses, code)
}

//nolint:unused // used by refresh_integration_test.go (build tag: integration)
func (f *fakeSSOServer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls
}

func (f *fakeSSOServer) signAccessToken(t *testing.T, charID int64, owner, name string, scp interface{}) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": "login.eveonline.com", "sub": fmt.Sprintf("CHARACTER:EVE:%d", charID),
		"aud": []string{f.clientID, "EVE Online"},
		"exp": time.Now().Add(20 * time.Minute).Unix(),
		"nbf": time.Now().Add(-time.Minute).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
		"scp": scp, "name": name, "owner": owner,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.kid
	s, err := tok.SignedString(f.priv)
	require.NoError(t, err)
	return s
}

func (f *fakeSSOServer) close() { f.srv.Close() }

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

func testKeyring(t *testing.T) *crypto.Keyring {
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

func buildFlow(t *testing.T, sso1 *fakeSSOServer, store *fakeStore) *sso.Flow {
	t.Helper()
	cache := jwks.NewCache(sso1.srv.URL+"/oauth/jwks", newFakeSettingStore(), sso1.srv.Client(), nil)
	require.NoError(t, cache.Refresh(context.Background()))
	verifier := jwks.NewVerifier(cache, jwks.VerifierConfig{
		Issuers:  []string{"login.eveonline.com", "https://login.eveonline.com"},
		ClientID: sso1.clientID, Audience: "EVE Online",
	})

	return &sso.Flow{
		Store: store,
		OAuth: sso.OAuthConfig{
			ClientID: sso1.clientID, ClientSecret: "test-secret",
			CallbackURL:  "http://localhost/callback",
			AuthorizeURL: sso1.srv.URL + "/v2/oauth/authorize", TokenURL: sso1.srv.URL + "/v2/oauth/token",
			HTTPClient: sso1.srv.Client(),
		},
		Verifier: verifier,
		Keyring:  testKeyring(t),
	}
}

// TestAuthorizationCodePKCEEndToEnd (roadmap exit criterion): full exchange
// against a stub SSO.
func TestAuthorizationCodePKCEEndToEnd(t *testing.T) {
	sso1 := newFakeSSOServer(t, "test-client-id")
	defer sso1.close()
	store := newFakeStore()
	flow := buildFlow(t, sso1, store)

	pending, err := flow.BeginLogin(context.Background(), []string{"esi-characters.read_contacts.v1"}, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, pending.RedirectURL)

	parsed, err := url.Parse(pending.RedirectURL)
	require.NoError(t, err)
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	require.NotEmpty(t, parsed.Query().Get("code_challenge"))
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state)

	result, err := flow.HandleCallback(context.Background(), pending.SessionID, "auth-code-123", state)
	require.NoError(t, err)
	require.Equal(t, int64(2112625428), result.CharacterID)
	require.True(t, result.IsNewUser)
	require.Contains(t, result.Scopes, "esi-characters.read_contacts.v1")

	// The session's PKCE verifier/state must be single-use — a second
	// callback attempt against the same session must fail.
	_, err = flow.HandleCallback(context.Background(), pending.SessionID, "auth-code-123", state)
	require.Error(t, err, "state must be single-use")

	// The token round-trips through envelope encryption.
	tok, err := store.GetCharacterToken(context.Background(), result.CharacterID)
	require.NoError(t, err)
	require.True(t, tok.Valid)
}

// TestOwnerHashChangeInvalidatesTokens (roadmap exit criterion): changed
// owner invalidates every token for the character.
func TestOwnerHashChangeInvalidatesTokens(t *testing.T) {
	sso1 := newFakeSSOServer(t, "test-client-id")
	defer sso1.close()
	store := newFakeStore()
	flow := buildFlow(t, sso1, store)

	var invalidated []int64
	lifecycle := sso.NewLifecycle(store, nil)
	flow.OnOwnerHashChanged = func(ctx context.Context, characterID int64) {
		invalidated = append(invalidated, characterID)
		require.NoError(t, lifecycle.InvalidateForOwnerHashChange(ctx, characterID))
	}

	// First login establishes the character with owner-hash-1.
	pending, err := flow.BeginLogin(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	state := mustState(t, pending.RedirectURL)
	result, err := flow.HandleCallback(context.Background(), pending.SessionID, "code-1", state)
	require.NoError(t, err)

	tok, err := store.GetCharacterToken(context.Background(), result.CharacterID)
	require.NoError(t, err)
	require.True(t, tok.Valid)

	// Simulate a transfer: the character's SSO-reported owner changes on
	// the NEXT login for the same character_id.
	sso1.setOwnerHash("owner-hash-2-transferred")

	pending2, err := flow.BeginLogin(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	state2 := mustState(t, pending2.RedirectURL)
	_, err = flow.HandleCallback(context.Background(), pending2.SessionID, "code-2", state2)
	require.NoError(t, err)

	require.Contains(t, invalidated, result.CharacterID, "an owner_hash change must invalidate the character's tokens")

	// The character record itself now reflects the new owner — the
	// re-login that revealed the transfer is also what re-establishes a
	// legitimately valid token for the (new) current owner.
	tok2, err := store.GetCharacterToken(context.Background(), result.CharacterID)
	require.NoError(t, err)
	require.Equal(t, "owner-hash-2-transferred", tok2.OwnerHash)
}

func mustState(t *testing.T, redirectURL string) string {
	t.Helper()
	u, err := url.Parse(redirectURL)
	require.NoError(t, err)
	return u.Query().Get("state")
}
