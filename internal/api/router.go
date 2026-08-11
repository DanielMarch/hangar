// router.go assembles the Huma v2.39.1 API (the CONTRACTUAL PIN recorded
// in go.mod's header comment) that serves every /api/v1 route registered
// by internal/api/v1. One API, one router, one OpenAPI document — SRS
// Principle 6: "the SPA gets no private endpoint."
package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
)

// Deps is everything a v1 route group needs to register its operations.
// Kept as one struct so cmd/hangar/serve.go and cmd/hangar/openapi.go build
// it once and pass it down uniformly — openapi.go's spec-only build path
// leaves every field nil (schema generation never calls a handler), so no
// v1 registration function may dereference a Deps field outside a handler
// closure.
type Deps struct {
	Store *store.Store
	// Pool is the transaction-capable handle the handful of handlers that
	// must write atomically need (PUT /admin/scopes' grant replace goes
	// through internal/rbac.ReplaceRoleGrants, which opens its own
	// transaction). Store alone cannot Begin.
	Pool store.Pool
	// SSO is nil unless cmd/hangar/serve.go configured EVE SSO credentials
	// — the character-reauthorize handler degrades to an error rather than
	// panicking when it is.
	SSO *sso.Flow
}

// NewAPI builds the Huma API bound to mux, with SRS §6's session-resolving
// middleware wired ahead of every route (Phase 10's authorize.go seam —
// RequirePermission reads what this populates). Callers needing the raw
// spec only (cmd/hangar openapi) still call this with Deps.Store == nil;
// registration never touches the store outside a handler body.
func NewAPI(mux *http.ServeMux, deps Deps) huma.API {
	config := huma.DefaultConfig("Project HANGAR API", Version)
	config.OpenAPIPath = "/api/v1/openapi"
	config.DocsPath = "/api/v1/docs"
	config.Info.Description = "HANGAR's single API surface (SRS Principle 6) — the SPA and every third-party integration share it; there is no private endpoint."

	api := humago.New(mux, config)
	return api
}

// Version is the API's reported version. cmd/hangar overrides it at build
// time via -ldflags the same way main.version is set; left as a plain var
// (not a const) so main can assign it during init without an import cycle.
var Version = "0.0.0-dev"

// Handler wraps mux with the session-resolving middleware and returns the
// final http.Handler cmd/hangar/serve.go mounts. Kept separate from NewAPI
// so the openapi-generation code path (which never serves a request) can
// build the API without paying for a middleware chain that needs a live
// Store.
func Handler(mux *http.ServeMux, s *store.Store) http.Handler {
	return middleware.ResolveSession(s)(mux)
}

// RequirePermission adapts middleware.RequirePermission's net/http
// contract into a huma.Middlewares entry, so a v1 route group can guard an
// individual operation with
// `Middlewares: huma.Middlewares{api.RequirePermission(s, "perm.name")}`
// instead of wrapping the whole mux. middleware.RequirePermission already
// either writes the 401/403 itself and returns without calling its inner
// handler, or calls it — this adapter's inner handler is exactly `next`,
// so that either-or is preserved unchanged.
func RequirePermission(s *store.Store, permission string) func(huma.Context, func(huma.Context)) {
	guard := middleware.RequirePermission(s, permission)
	return func(ctx huma.Context, next func(huma.Context)) {
		called := false
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called = true
			next(ctx)
		})
		req, w := humago.Unwrap(ctx)
		guard(inner).ServeHTTP(w, req.WithContext(ctx.Context()))
		_ = called // guard decides; nothing further to do either branch
	}
}
