package v2shim

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/store"
)

// Prefix is the shim's mount point. Every path below it is served here and
// nowhere else.
const Prefix = "/api/v2"

// Deps is what the shim needs. Deliberately narrower than api.Deps: the
// shim is READ-ONLY (SRS §10), so it never takes a transaction-capable
// pool. Not having one is a structural guarantee rather than a promise —
// a shim handler could not write even by mistake.
type Deps struct {
	Store *store.Store
}

// Register mounts every /api/v2 route on mux.
//
// ── HOW THE SHIM RELATES TO /api/v1 ──────────────────────────────────────
// The roadmap describes the shim as "translating legacy request and
// response shapes onto /api/v1 handlers". It translates onto the same
// STORE and the same RBAC PERMISSIONS the matching /api/v1 route uses, not
// by re-entering the Huma stack over loopback HTTP. Re-entering would mean
// serialising HANGAR's shape and immediately re-parsing it just to reshape
// it, and it would put a network hop inside every request for no gain.
//
// What must NOT differ, and does not, is authorisation: every shim route
// goes through middleware.RequirePermission with the SAME permission name
// its /api/v1 counterpart uses, so a shim route cannot be a way around a
// check — including the API token's own permission cap, which
// RequirePermission applies (Phase 18, defect B21).
func Register(mux *http.ServeMux, deps Deps) {
	for _, route := range Routes() {
		mux.Handle(route.Method+" "+route.Pattern, route.handler(deps))
	}

	// The reshaped controllers. Registered as prefixes so EVERY path under
	// them answers, including ones this shim has never heard of: a client
	// calling `/api/v2/roles/query/anything` must be told the grant model
	// changed, not handed a 404 that reads as "wrong URL, try again".
	mux.Handle("/api/v2/roles/query/", breakingChangeHandler(roleLookupBreak))
	mux.Handle("/api/v2/roles", breakingChangeHandler(roleBreak))
	mux.Handle("/api/v2/roles/", breakingChangeHandler(roleBreak))

	// Anything else under /api/v2 that this shim does not serve. Without
	// this, an unmatched /api/v2 path falls through to the SPA handler,
	// which answers 200 with HTML for a client expecting JSON.
	mux.Handle(Prefix+"/", notShimmedHandler())
	mux.Handle(Prefix, notShimmedHandler())
}

// Route is one legacy read route.
type Route struct {
	// Controller is the legacy controller this route came from, so
	// TestShimByteCompatibleForAllNineControllers can assert coverage per
	// controller rather than just in total.
	Controller string
	Method     string
	// Pattern is the net/http ServeMux pattern.
	Pattern string
	// Corpus is the basename in testdata/legacy-api-v2/responses this
	// route is measured against. Empty for a route with no recording.
	Corpus string
	// Permission is the RBAC permission the equivalent /api/v1 route
	// requires. Never empty — a shim route with no permission would be a
	// hole in the very surface Phase 18 just closed.
	Permission string
	// Appends mirrors whether the legacy controller called
	// `paginate()->appends(request()->except('page'))`. It changes the
	// pagination link bytes, and the legacy controllers are inconsistent
	// about it, so it is recorded per route rather than guessed.
	Appends bool
	// Handle produces the response body.
	Handle func(*Request) (any, error)
}

// Request is one incoming shim request, already parsed.
type Request struct {
	HTTP  *http.Request
	Deps  Deps
	Page  int
	IDs   []int64
	Query map[string][]string
	// BaseURL is the absolute URL without query — Laravel's `path`.
	BaseURL string
	// Appends carries the route's ->appends() behaviour into the envelope.
	Appends bool
}

// PageOf builds the legacy envelope for a collection route.
func (r *Request) PageOf(rows Arr, total int64) *Obj {
	page := Page{
		Rows: rows, Total: total, CurrentPage: r.Page,
		PerPage: LegacyPerPage, BaseURL: r.BaseURL,
	}
	if r.Appends {
		query := make(map[string][]string, len(r.Query))
		for key, values := range r.Query {
			if key != "page" {
				query[key] = values
			}
		}
		page.Query = query
	}
	return page.Envelope()
}

// handler wraps one route's Handle with authentication, authorisation, the
// deprecation headers and legacy's error shapes.
func (route Route) handler(deps Deps) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := parseRequest(r, deps, route)
		if err != nil {
			writeLegacyError(w, http.StatusBadRequest, err.Error())
			return
		}

		body, err := route.Handle(req)
		if err != nil {
			writeShimError(w, err)
			return
		}

		encoded, encodeErr := Encode(body)
		if encodeErr != nil {
			writeLegacyError(w, http.StatusInternalServerError, "Server Error")
			return
		}
		writeJSON(w, http.StatusOK, encoded)
	})

	// RequirePermission writes its own plain-text 401/403 bodies, which are
	// right for /api/v1 and wrong here — legacy answered a bare JSON string
	// "Unauthorized". legacyErrorBodies rewrites them without duplicating
	// the authorisation logic itself, so there is still exactly one place
	// that decides whether a request is allowed.
	guarded := middleware.RequirePermission(deps.Store, route.Permission)(inner)
	return legacyErrorBodies(guarded)
}

// parseRequest extracts the path ids and the page.
func parseRequest(r *http.Request, deps Deps, route Route) (*Request, error) {
	req := &Request{
		HTTP: r, Deps: deps,
		Page:    ParsePage(r.URL.Query().Get("page")),
		Query:   r.URL.Query(),
		BaseURL: BaseURL(requestScheme(r), r.Host, r.URL.Path),
		Appends: route.Appends,
	}

	// Legacy's `$filter` is NOT implemented — see the note on
	// errFilterUnsupported.
	if r.URL.Query().Has("$filter") {
		return nil, errFilterUnsupported
	}

	for _, name := range []string{"id", "sub_id"} {
		raw := r.PathValue(name)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errBadID
		}
		req.IDs = append(req.IDs, id)
	}
	return req, nil
}

// requestScheme reproduces what Laravel's UrlGenerator would have used, so
// the pagination links point back at the scheme the client actually
// reached the server on. X-Forwarded-Proto is honoured because HANGAR is
// documented as running behind a reverse proxy (SRS §9); without it every
// link on a TLS-terminated deployment says `http://`.
func requestScheme(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		if scheme, _, found := strings.Cut(forwarded, ","); found {
			return strings.TrimSpace(scheme)
		}
		return strings.TrimSpace(forwarded)
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// LegacyTokenHeader is the header legacy SeAT authenticated with.
const LegacyTokenHeader = "X-Token"

// LegacyTokenAlias lets a /api/v2 client present its HANGAR API token in
// legacy's `X-Token` header instead of `Authorization: Bearer`.
//
// ── THE DECISION, AND WHY IT IS NOT A WEAKER CREDENTIAL ───────────────────
// The question the roadmap poses is real: requiring every existing client
// to change its auth header on day one is not much of a migration aid, but
// silently accepting a weaker credential is worse. The resolution is that
// this alias changes the header NAME and nothing else.
//
// The value must still be a HANGAR credential in the exact
// `<token_id>.<secret>` form, and it is resolved by rewriting the request
// into the Bearer form BEFORE middleware.ResolveAPIToken runs — so there is
// still exactly ONE credential path, one hash lookup, one revoked/expired
// check, and one permission cap. There is no second authenticator to audit.
// A legacy SeAT token is not accepted, because HANGAR has no such tokens;
// what a migrating client changes is a config value, not its code.
//
// Scoped to /api/v2 paths only. /api/v1 keeps a single scheme, because the
// forward-looking surface should not carry the compatibility layer's
// vocabulary.
//
// Authorization wins when both are present: a client that has already
// migrated its header should not have a stale X-Token silently override it.
func LegacyTokenAlias(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, Prefix) {
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimSpace(r.Header.Get(LegacyTokenHeader))
		if token == "" || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}
		aliased := r.Clone(r.Context())
		aliased.Header.Set("Authorization", middleware.BearerPrefix+token)
		next.ServeHTTP(w, aliased)
	})
}

// legacyErrorBodies converts RequirePermission's plain-text 401/403 into
// legacy's bare-JSON-string form, and stamps the deprecation headers on
// them — a client that only ever sees 401s is exactly the one that needs
// to be told the surface is going away.
func legacyErrorBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &statusCapturingWriter{ResponseWriter: w}
		next.ServeHTTP(capture, r)
		if capture.wroteBody {
			return
		}
		switch capture.status {
		case http.StatusUnauthorized:
			writeLegacyError(w, http.StatusUnauthorized, "Unauthorized")
		case http.StatusForbidden:
			// Legacy had no 403 on this surface — an X-Token was either
			// valid for everything or rejected. HANGAR's tokens are
			// scoped, so a valid token CAN lack a permission, and saying
			// "Unauthorized" would send an integrator to re-check a
			// credential that is fine. The status is the honest 403; the
			// body stays a bare string so the shape does not change.
			writeLegacyError(w, http.StatusForbidden, "Forbidden")
		default:
			writeLegacyError(w, capture.status, http.StatusText(capture.status))
		}
	})
}

// statusCapturingWriter swallows the inner handler's status and body when
// the inner handler is RequirePermission rejecting the request, and passes
// everything through when it is a real shim response.
type statusCapturingWriter struct {
	http.ResponseWriter
	status    int
	wroteBody bool
	rejected  bool
}

func (w *statusCapturingWriter) WriteHeader(status int) {
	w.status = status
	// 401 and 403 are the only statuses RequirePermission produces, and a
	// shim handler never produces them itself — it writes 200 or goes
	// through writeShimError. So these two, and only these two, are
	// rewritten.
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		w.rejected = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	if w.rejected {
		// Discard RequirePermission's plain-text body; legacyErrorBodies
		// writes the legacy-shaped one.
		return len(b), nil
	}
	w.wroteBody = true
	return w.ResponseWriter.Write(b)
}

// notShimmedHandler answers a /api/v2 path this shim does not serve.
//
// SRS §10 is explicit that write routes "must return a clear 'not shimmed'
// response rather than a 404": a 404 tells a client the URL is wrong, which
// sends them looking for a typo instead of reading the migration guide.
func notShimmedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		message := "This /api/v2 route is not shimmed. The shim is read-only; " +
			"see " + DeprecationDocsURL + " for the /api/v1 equivalent."
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			message = "The /api/v2 shim is read-only — " + r.Method + " is not shimmed. " +
				"Use the /api/v1 equivalent; see " + DeprecationDocsURL + "."
		}
		writeLegacyError(w, http.StatusNotImplemented, message)
	})
}
