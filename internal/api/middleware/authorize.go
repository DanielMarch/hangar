// Package middleware holds HTTP middleware. authorize.go is Phase 10's
// authorization check: it reads the caller's user id from the request
// context and 401s/403s against app.effective_permission's materialised
// hot path (02_DATABASE_SCHEMA.md §4.2: "the hot path is a single indexed
// lookup" — never a live role_grant join here).
//
// SEAM, NOT A FULL SESSION LAYER: no request-context user convention
// exists anywhere else in this codebase yet. Phase 5 (internal/sso) built
// token/session PERSISTENCE (app.session, refresh token lifecycle) but no
// HTTP middleware that reads a session cookie/header and populates a
// request context — that is Phase 15's job (internal/api/v1's router).
// Rather than leave authorize.go unable to read a user at all, this file
// defines the minimal context convention (WithUserID/UserIDFromContext)
// that a session-resolving middleware placed BEFORE RequirePermission in
// the chain is expected to populate. Phase 15 should either adopt this
// convention directly or, if it settles on a different one, this file
// updates to match — it is explicitly a placeholder seam, not a finished
// session system.
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

type contextKey int

const (
	userIDContextKey contextKey = iota
	tokenScopeContextKey
)

// WithUserID returns a context carrying the authenticated user's id.
// Called by whatever session-resolving middleware runs before
// RequirePermission in the chain (Phase 15's concern — see this file's
// header).
func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

// UserIDFromContext retrieves the user id WithUserID stored, if any.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDContextKey).(uuid.UUID)
	return id, ok
}

// tokenScope is the permission cap an API-token-authenticated request
// carries. Absent for a cookie session, which has no scope of its own.
type tokenScope struct {
	tokenID     uuid.UUID
	permissions map[string]bool
}

// WithTokenScope records that this request was authenticated by an API
// token, and which permissions THAT TOKEN was granted.
//
// This is a cap, not a grant. SRS §12 and Appendix capability 47 both call
// these tokens *scoped*: `app.api_token.permissions` is a subset a user
// chose to expose to one integration. Resolving a token to its owner's
// user id and stopping there would hand every narrowly-scoped token the
// owner's FULL effective permissions — a privilege escalation, and the
// exact thing scoping exists to prevent. RequirePermission below therefore
// requires the permission to be in BOTH the user's materialised set and
// the token's scope.
func WithTokenScope(ctx context.Context, tokenID uuid.UUID, permissions []string) context.Context {
	set := make(map[string]bool, len(permissions))
	for _, p := range permissions {
		set[p] = true
	}
	return context.WithValue(ctx, tokenScopeContextKey, tokenScope{tokenID: tokenID, permissions: set})
}

// TokenIDFromContext returns the API token this request was authenticated
// by, if it was authenticated by one at all.
func TokenIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	scope, ok := ctx.Value(tokenScopeContextKey).(tokenScope)
	if !ok {
		return uuid.Nil, false
	}
	return scope.tokenID, true
}

// tokenPermits reports whether the request's credential allows
// `permission` — always true for a cookie session, which carries no scope
// of its own, and true for a token only when the permission is inside the
// token's own grant set.
func tokenPermits(ctx context.Context, permission string) bool {
	scope, ok := ctx.Value(tokenScopeContextKey).(tokenScope)
	if !ok {
		return true
	}
	return scope.permissions[permission]
}

// RequirePermission returns middleware that 401s a request with no user
// in context, 403s one whose user lacks `permission` in
// app.effective_permission, and otherwise calls next. It never falls back
// to a live role_grant recomputation — a user who hasn't been
// materialized yet (internal/rbac.RefreshUser was never called for them,
// e.g. a brand new account before its first role assignment) is treated
// as having no permissions, the same "zero roles = zero permissions"
// default the resolver itself uses.
func RequirePermission(s *store.Store, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserIDFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			// An API-token-authenticated request is additionally capped by
			// the token's OWN permission set (see WithTokenScope). Checked
			// before the database lookup because it needs no database, and
			// checked as a 403 rather than a 401: the credential is valid,
			// it simply does not carry this permission.
			if !tokenPermits(r.Context(), permission) {
				http.Error(w, "forbidden: this API token's scope does not include "+permission, http.StatusForbidden)
				return
			}

			row, err := s.GetEffectivePermission(r.Context(), userID, permission)
			switch {
			case err == nil && row.Permitted:
				next.ServeHTTP(w, r)
			case err == nil:
				http.Error(w, "forbidden", http.StatusForbidden)
			default:
				// A missing row (pgx.ErrNoRows via GetEffectivePermission's
				// :one query) and any genuine query failure both fail
				// closed — never permit on an error rather than an
				// explicit permitted=true row. A superuser holder never
				// bypasses this: internal/rbac.RefreshUser writes a
				// materialised effective_permission row for EVERY
				// permission in the closed set, so an absent row here
				// means "never materialized", not "implicitly permitted".
				http.Error(w, "forbidden", http.StatusForbidden)
			}
		})
	}
}
