// apitoken.go authenticates a request by third-party API token — the
// other half of SRS §6.1/§12's token surface, and the half that did not
// exist.
//
// PHASE 18 CLOSE-OUT DEFECT. `hangar admin bootstrap-token` has minted
// app.api_token rows since Phase 1 and prints its secret with the words
// "Use it as a Bearer token ... once Phase 15 lands". Phase 15 landed and
// wired the token MANAGEMENT endpoints (list/create/revoke/access-log) —
// but nothing ever authenticated a request BY one: session.go's cookie was
// the only credential any middleware read. So the bootstrap token, which
// exists specifically to be the way into a fresh installation before any
// human has completed SSO, could not be used for anything at all, and the
// third-party integration surface Principle 6 is built around was
// unreachable.
package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/store"
)

// BearerPrefix is the Authorization scheme API tokens use.
const BearerPrefix = "Bearer "

// ResolveAPIToken returns middleware that, given
// `Authorization: Bearer <token_id>.<secret>`, populates the request
// context with the token owner's user id AND with the token's own
// permission scope.
//
// Like ResolveSession, a missing or invalid credential is NOT an error
// here — it leaves the request unauthenticated and lets RequirePermission
// reject it. Absence of proof is absence of identity, never a response of
// its own.
//
// Ordering: this runs BEFORE ResolveSession in the chain, and
// ResolveSession leaves an already-identified request alone, so a request
// carrying both a token and a cookie is the token's. That ordering is the
// safe one: the token is the narrower credential (it carries a permission
// scope; a cookie does not), so a client that sends both gets the LESSER
// authority, never the greater.
func ResolveAPIToken(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, BearerPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			raw := strings.TrimSpace(strings.TrimPrefix(header, BearerPrefix))
			tokenID, secret, ok := strings.Cut(raw, ".")
			if !ok || tokenID == "" || secret == "" {
				next.ServeHTTP(w, r)
				return
			}
			id, err := uuid.Parse(tokenID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			decoded, err := decodeSecret(secret)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			// The lookup is BY HASH, so the raw secret is never compared
			// against anything stored, and GetApiTokenByHash's own
			// predicates exclude revoked and expired tokens.
			sum := sha256.Sum256(decoded)
			token, err := s.GetApiTokenByHash(r.Context(), sum[:])
			if err != nil {
				// Unknown, revoked or expired — all unauthenticated, never
				// distinguished to the caller.
				next.ServeHTTP(w, r)
				return
			}
			// The presented token_id must be the one that owns this hash.
			// Constant-time, and not merely belt-and-braces: without it the
			// id half of the credential would be unauthenticated input that
			// the access log then attributes the request to.
			if subtle.ConstantTimeCompare([]byte(token.TokenID.String()), []byte(id.String())) != 1 {
				next.ServeHTTP(w, r)
				return
			}

			// Best-effort: a failure to record last-used must not fail the
			// request it is describing.
			_ = s.TouchApiTokenLastUsed(r.Context(), token.TokenID)

			ctx := WithUserID(r.Context(), token.UserID)
			ctx = WithTokenScope(ctx, token.TokenID, token.Permissions)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// decodeSecret accepts the base64 form bootstrapToken prints
// (RawURLEncoding) and tolerates the padded and standard-alphabet
// variants, because the secret is copied by hand out of a terminal at
// least once in every installation's life and a padding mismatch is not a
// useful failure mode.
func decodeSecret(s string) ([]byte, error) {
	var firstErr error
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		b, err := enc.DecodeString(s)
		if err == nil {
			return b, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}
