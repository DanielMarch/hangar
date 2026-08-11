//go:build integration

package middleware_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/stretchr/testify/require"
)

// mintToken issues an app.api_token exactly the way
// cmd/hangar/admin_bootstrap_token.go does, and returns the raw
// "<token_id>.<secret>" credential it prints — so this test exercises the
// real format an operator copies out of a terminal, not an invented one.
func mintToken(t *testing.T, s *store.Store, userID uuid.UUID, permissions []string) string {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)

	token, err := s.CreateApiToken(context.Background(), gen.CreateApiTokenParams{
		UserID: userID, Name: "test", HashedSecret: sum[:], Permissions: permissions,
	})
	require.NoError(t, err)
	return token.TokenID.String() + "." + base64.RawURLEncoding.EncodeToString(raw)
}

// guarded builds the real middleware chain — token, then session, then the
// permission guard — around a handler that reports the resolved identity.
func guarded(s *store.Store, permission string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := middleware.UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(id.String()))
	})
	return middleware.ResolveAPIToken(s)(
		middleware.ResolveSession(s)(
			middleware.RequirePermission(s, permission)(inner)))
}

// TestAPITokenAuthenticatesAndIsScoped is the Phase 18 close-out proof for
// the defect recorded in SRS §0: `hangar admin bootstrap-token` minted a
// token that NO middleware accepted, so the token surface Principle 6 is
// built around — and the only way into a fresh installation before anyone
// has completed SSO — was unreachable.
//
// Both halves are asserted, and the second is the one that matters: a
// token must authenticate, AND it must not confer more than its own
// scope. Resolving a token to its owner's user id and stopping there would
// hand every narrowly-scoped integration the owner's full permissions.
func TestAPITokenAuthenticatesAndIsScoped(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	user, err := s.CreateUser(ctx, "Token Owner "+uuid.NewString())
	require.NoError(t, err)

	// The owner holds BOTH permissions through RBAC.
	role, err := s.CreateRole(ctx, "token-test-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	for _, p := range []string{"admin.sync.view", "admin.esi.view"} {
		_, err := s.AddRoleGrant(ctx, role.RoleID, p, "allow")
		require.NoError(t, err)
	}
	require.NoError(t, s.AssignUserRole(ctx, user.UserID, role.RoleID, uuid.NullUUID{}))
	require.NoError(t, rbac.RefreshUser(ctx, s, user.UserID))

	t.Run("a valid token authenticates a request", func(t *testing.T) {
		cred := mintToken(t, s, user.UserID, []string{"admin.sync.view"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sync/routes", nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()

		guarded(s, "admin.sync.view").ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code,
			"a minted bootstrap token must authenticate — this is the defect the whole file exists for")
		require.Equal(t, user.UserID.String(), rec.Body.String(), "resolved to the token's owner")
	})

	t.Run("the token's scope CAPS the owner's permissions", func(t *testing.T) {
		// The owner can do admin.esi.view. This token cannot, and must not
		// inherit it.
		cred := mintToken(t, s, user.UserID, []string{"admin.sync.view"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/esi/replicas", nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()

		guarded(s, "admin.esi.view").ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code,
			"a scoped token must not confer its owner's full RBAC — that is a privilege escalation")
		require.Contains(t, rec.Body.String(), "scope")
	})

	t.Run("the scope cannot exceed the owner's own permissions either", func(t *testing.T) {
		// The cap works in both directions: a token naming a permission the
		// OWNER does not hold gains nothing, because the materialised
		// effective_permission check still runs.
		cred := mintToken(t, s, user.UserID, []string{domain.SuperuserPermission})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()

		guarded(s, domain.SuperuserPermission).ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("a revoked token stops working", func(t *testing.T) {
		cred := mintToken(t, s, user.UserID, []string{"admin.sync.view"})
		tokenID := uuid.MustParse(cred[:36])
		require.NoError(t, s.RevokeApiToken(ctx, tokenID))

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()

		guarded(s, "admin.sync.view").ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("garbage credentials are unauthenticated, never a 500", func(t *testing.T) {
		valid := mintToken(t, s, user.UserID, []string{"admin.sync.view"})
		for name, header := range map[string]string{
			"empty":              "Bearer ",
			"no dot":             "Bearer not-a-token",
			"bad uuid":           "Bearer nope.c2VjcmV0",
			"unknown secret":     "Bearer " + uuid.NewString() + ".c2VjcmV0",
			"wrong scheme":       "Basic " + valid,
			"id/secret mismatch": uuid.NewString() + valid[36:],
		} {
			t.Run(name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/x", nil)
				req.Header.Set("Authorization", header)
				rec := httptest.NewRecorder()

				guarded(s, "admin.sync.view").ServeHTTP(rec, req)
				require.Equal(t, http.StatusUnauthorized, rec.Code)
			})
		}
	})

	t.Run("a token beats a cookie presented alongside it", func(t *testing.T) {
		// The token is the NARROWER credential. A request carrying both
		// must get the lesser authority, or a browser cookie riding along
		// would silently upgrade a scoped integration.
		session, err := s.CreateSession(ctx, gen.CreateSessionParams{
			UserID:    uuid.NullUUID{UUID: user.UserID, Valid: true},
			ExpiresAt: time.Now().Add(time.Hour),
		})
		require.NoError(t, err)

		cred := mintToken(t, s, user.UserID, []string{"admin.sync.view"})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: session.SessionID.String()})
		rec := httptest.NewRecorder()

		// admin.esi.view is inside the cookie's authority but outside the
		// token's scope. The token must win.
		guarded(s, "admin.esi.view").ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code,
			"the cookie overrode the token's scope — a scoped integration was silently upgraded")
	})

	t.Run("a cookie alone still works, and carries no scope cap", func(t *testing.T) {
		session, err := s.CreateSession(ctx, gen.CreateSessionParams{
			UserID:    uuid.NullUUID{UUID: user.UserID, Valid: true},
			ExpiresAt: time.Now().Add(time.Hour),
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: session.SessionID.String()})
		rec := httptest.NewRecorder()

		guarded(s, "admin.esi.view").ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "cookie sessions must be unaffected by the token cap")
	})
}
