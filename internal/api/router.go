// router.go assembles the Huma v2.39.1 API (the CONTRACTUAL PIN recorded
// in go.mod's header comment) that serves every /api/v1 route registered
// by internal/api/v1. One API, one router, one OpenAPI document — SRS
// Principle 6: "the SPA gets no private endpoint."
package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/api/v2shim"
	"github.com/hangar-project/hangar/internal/provisioning"
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

	// Urgent is the < 60s revocation path's enqueuer (§9.2), backed by an
	// INSERT-ONLY River client: the API process produces provision-urgent
	// jobs and never consumes them — cmd/hangar/work.go still owns the only
	// worker pool.
	//
	// PHASE 20.3. Phase 18 recorded that "wiring the API process to River
	// would be an architectural change well beyond this phase" and left
	// DELETE-a-rule unmounted because of it; see cmd/hangar/revocation.go
	// for why an insert-only client is not that change. It is nil in the
	// spec-only build path (cmd/hangar/openapi.go) and in tests, and every
	// handler that needs it says so with a 500 rather than panicking.
	Urgent *provisioning.Urgent

	// Alerts is §4.4's emitter, for the handful of handlers whose action IS
	// a domain event worth alerting on (Phase 20.4, defect B25 — the
	// `domain_event` third of the alert catalogue, which until then had no
	// producer anywhere).
	//
	// Nil in the spec-only build path and in tests, and every user checks:
	// an installation whose administrator advanced the ESI pin must still
	// have advanced it even if nothing was listening.
	Alerts *alerting.Emitter
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

	declareSecuritySchemes(config.OpenAPI)

	api := humago.New(mux, config)
	return api
}

// Names of the declared security schemes, referenced from the document's
// top-level `security` requirement.
const (
	SecuritySchemeBearer = "apiToken"
	SecuritySchemeCookie = "session"
)

// declareSecuritySchemes fills in `components.securitySchemes` and the
// document-wide `security` requirement.
//
// PHASE 19. Until now docs/openapi.json declared NO security schemes at all
// — not for the session cookie, not for the bearer token — which was
// survivable while the only consumer was HANGAR's own SPA (Phase 16
// generates web/src/api/schema.d.ts from this document, and types do not
// care about auth). It stopped being survivable the moment §12's
// third-party surface became real: a generated client built from a spec
// with no security scheme sends no credential, and the integrator's first
// experience of HANGAR is a 401 the document gave them no way to predict.
//
// BOTH schemes are declared because both are real and they are not
// interchangeable — the cookie is the SPA's, the bearer token is an
// integration's, and only the latter carries a permission scope that caps
// it. The top-level requirement lists them as ALTERNATIVES (two entries in
// the array, not two keys in one entry), which is OpenAPI's spelling for
// "either of these", and matches middleware.ResolveAPIToken /
// ResolveSession: a request presenting both gets the token's — the lesser —
// authority.
func declareSecuritySchemes(spec *huma.OpenAPI) {
	if spec.Components == nil {
		spec.Components = &huma.Components{}
	}
	if spec.Components.SecuritySchemes == nil {
		spec.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}

	spec.Components.SecuritySchemes[SecuritySchemeBearer] = &huma.SecurityScheme{
		Type:   "http",
		Scheme: "bearer",
		Description: "A third-party API token (SRS §12), presented as " +
			"`Authorization: Bearer <token_id>.<secret>`. Mint one with " +
			"`hangar admin bootstrap-token` or `POST /api/v1/admin/tokens`.\n\n" +
			"The token's own `permissions` array is a CAP: a request is permitted only when the " +
			"permission is in both the owner's roles and the token's scope, so a narrowly-scoped " +
			"integration can never act with its owner's full authority.\n\n" +
			"On the deprecated `/api/v2` shim only, the same credential may be presented in " +
			"legacy SeAT's `X-Token` header instead.",
	}
	spec.Components.SecuritySchemes[SecuritySchemeCookie] = &huma.SecurityScheme{
		Type: "apiKey",
		In:   "cookie",
		Name: "hangar_session",
		Description: "The browser session cookie established by the EVE SSO login flow " +
			"(`/auth/login`). Carries no scope of its own — it is the full authority of the " +
			"user it identifies — so it is the SPA's credential, not an integration's.",
	}

	// Alternatives, not a conjunction. Individual operations still carry
	// their own RBAC permission requirement; this only says which
	// credentials the surface understands.
	spec.Security = []map[string][]string{
		{SecuritySchemeBearer: {}},
		{SecuritySchemeCookie: {}},
	}
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
	// Token first, then cookie. ResolveSession leaves an already-identified
	// request alone, so a caller presenting both gets the TOKEN's identity
	// — which is the safe precedence, because a token additionally carries
	// a permission scope that caps it and a cookie does not. Resolving the
	// cookie first would silently upgrade a scoped integration to the
	// owner's full authority whenever a browser cookie happened to ride
	// along.
	//
	// PHASE 19: v2shim.LegacyTokenAlias runs BEFORE ResolveAPIToken and
	// only on /api/v2 paths. It rewrites legacy's `X-Token` header into the
	// Bearer form so a migrating client changes a config value rather than
	// its source — and, critically, it is a rewrite rather than a second
	// authenticator, so there is still exactly ONE credential path with one
	// hash lookup, one revocation check and one permission cap to audit.
	return v2shim.LegacyTokenAlias(
		middleware.ResolveAPIToken(s)(
			middleware.ResolveSession(s)(mux)))
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
