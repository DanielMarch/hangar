// session.go is Phase 15's session-resolving middleware — the piece
// authorize.go's header explicitly deferred: "a session-resolving
// middleware placed BEFORE RequirePermission in the chain is expected to
// populate [the user-id context]." It adopts that seam directly rather
// than inventing a second convention.
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/store"
)

// SessionCookieName is the one cookie every browser session carries —
// internal/sso's callback.go (Phase 5) already creates app.session rows
// keyed by SessionID; this middleware is the first HTTP code that reads
// one back.
const SessionCookieName = "hangar_session"

// ResolveSession returns middleware that, given a valid, unexpired
// SessionCookieName cookie, populates the request context with the
// session's user id via WithUserID. A missing or invalid cookie is NOT an
// error here — it leaves the request unauthenticated and lets
// RequirePermission (or a handler needing UserIDFromContext) reject it.
// This mirrors authorize.go's fail-closed default: absence of proof is
// absence of identity, never an error response of its own.
func ResolveSession(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			sessionID, err := uuid.Parse(cookie.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			// A missing session (pgx.ErrNoRows) and any other lookup
			// failure are both treated as "unauthenticated" rather than
			// 500ing every route behind this middleware — the same
			// fail-closed posture RequirePermission takes on a lookup
			// error.
			session, err := s.GetSession(r.Context(), sessionID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if !session.UserID.Valid {
				// A pre-callback PKCE session row has no user yet — still
				// unauthenticated, not an error.
				next.ServeHTTP(w, r)
				return
			}
			ctx := WithUserID(r.Context(), session.UserID.UUID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
