package catalogue

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/pb33f/libopenapi"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

// Route is one parsed ESI operation, pre-database: the pure output of
// ParseSpec. It carries every field 02_DATABASE_SCHEMA.md §4.3 wants in
// app.esi_route plus the scopes and roles that land in the two child
// tables — kept separate from any gen.* type so parsing can be unit-tested
// without a database (TestUpstreamPathStoredVerbatim,
// TestUnknownCacheModeDefaultsToTtlBased, TestCompatibilityDateRolloverAt1100UTC
// all run at this level).
type Route struct {
	OperationID       string
	Method            string // upper-case: GET, POST, ...
	UpstreamPath      string // verbatim from the spec key — never derived, never pluralised
	CacheAge          *time.Duration
	CacheMode         *string // x-server-cache-mode, falling back to x-cache-mode; NULL ⇒ ttl-based
	RateLimitGroup    *string
	RateLimitMax      *int32
	RateLimitWindow   *time.Duration
	PaginationStyle   *string
	CompatibilityDate time.Time
	BlockedByPin      bool
	SpecFragment      json.RawMessage // the raw operation object, verbatim
	IdentifierTypes   map[string]string
	Scopes            []string
	Roles             []string
}

// SchedulingMode resolves the scheduling behaviour a cache_mode value
// implies. Only "event-based" and "not-cached" are treated specially;
// every other value — including nil (not declared) and any value CCP or a
// synthetic spec invents that this package has never seen — defaults to
// "ttl-based" (02_DATABASE_SCHEMA.md §4.3: "cache_mode ... NULL ⇒
// 'ttl-based'"; Gate 6 condition (d)). This is a closed set of HANGAR's OWN
// scheduling behaviours, not a validation of the external cache_mode
// vocabulary — the stored string itself is never rejected or coerced.
func SchedulingMode(cacheMode *string) string {
	if cacheMode == nil {
		return "ttl-based"
	}
	switch *cacheMode {
	case "event-based", "not-cached":
		return *cacheMode
	default:
		return "ttl-based"
	}
}

// httpMethods is the fixed, ordered set of OpenAPI 3.1 operation slots a
// PathItem carries. Iteration order only affects which order routes are
// appended to ParseSpec's result, never correctness.
var httpMethods = []struct {
	name string
	get  func(*v3.PathItem) *v3.Operation
}{
	{"GET", func(p *v3.PathItem) *v3.Operation { return p.Get }},
	{"PUT", func(p *v3.PathItem) *v3.Operation { return p.Put }},
	{"POST", func(p *v3.PathItem) *v3.Operation { return p.Post }},
	{"DELETE", func(p *v3.PathItem) *v3.Operation { return p.Delete }},
	{"OPTIONS", func(p *v3.PathItem) *v3.Operation { return p.Options }},
	{"HEAD", func(p *v3.PathItem) *v3.Operation { return p.Head }},
	{"PATCH", func(p *v3.PathItem) *v3.Operation { return p.Patch }},
	{"TRACE", func(p *v3.PathItem) *v3.Operation { return p.Trace }},
}

// ParseSpec parses raw OpenAPI 3.1 bytes into Route rows. It is pure — no
// network, no database — so callers (Boot, and every parse-level test) can
// exercise it directly. appPin decides BlockedByPin per route.
//
// Two parses run side by side on purpose. libopenapi (via BuildV3Model)
// resolves $ref chains for parameters and security requirements, which
// ESI's real spec leans on heavily (e.g. every {corporation_id} parameter
// is a $ref to #/components/schemas/CorporationID) — hand-rolling that
// resolution would either be wrong or reimplement a chunk of the OpenAPI
// spec. A second, plain encoding/json parse into a generic tree captures
// each operation's exact raw JSON for spec_fragment and the six vendor
// extensions, verbatim and without any risk of the high-level model's own
// re-serialisation dropping or reordering a field.
func ParseSpec(specBytes []byte, appPin time.Time) ([]Route, error) {
	doc, err := libopenapi.NewDocument(specBytes)
	if err != nil {
		return nil, fmt.Errorf("catalogue: parsing OpenAPI document: %w", err)
	}
	model, buildErr := doc.BuildV3Model()
	if buildErr != nil {
		return nil, fmt.Errorf("catalogue: building OpenAPI v3 model: %w", buildErr)
	}

	var rawDoc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(specBytes, &rawDoc); err != nil {
		return nil, fmt.Errorf("catalogue: parsing raw spec JSON: %w", err)
	}

	var routes []Route
	for path, item := range model.Model.Paths.PathItems.FromOldest() {
		rawPath := rawDoc.Paths[path]
		for _, m := range httpMethods {
			op := m.get(item)
			if op == nil {
				continue
			}
			rawOp := rawPath[strings.ToLower(m.name)]
			route, err := buildRoute(path, m.name, item, op, rawOp, appPin)
			if err != nil {
				return nil, fmt.Errorf("catalogue: %s %s: %w", m.name, path, err)
			}
			routes = append(routes, route)
		}
	}

	if len(routes) == 0 {
		// A truncated download (a valid-but-empty document, a proxy
		// returning "{}") must not silently wipe the catalogue — this is a
		// failure, not an empty success (roadmap Phase 2 edge case).
		return nil, fmt.Errorf("catalogue: ingest mapped zero operations — refusing to treat this as an empty success")
	}
	return routes, nil
}

// rawExtensions is the shape every ESI operation's vendor extensions take
// on the wire, decoded straight from the raw JSON tree.
type rawExtensions struct {
	CacheAge          *int64        `json:"x-cache-age"`
	CacheMode         *string       `json:"x-cache-mode"`
	ServerCacheMode   *string       `json:"x-server-cache-mode"`
	CompatibilityDate string        `json:"x-compatibility-date"`
	Pagination        *string       `json:"x-pagination"`
	RequiredRoles     []string      `json:"x-required-roles"`
	RateLimit         *rawRateLimit `json:"x-rate-limit"`
}

type rawRateLimit struct {
	Group      *string `json:"group"`
	MaxTokens  *int32  `json:"max-tokens"`
	WindowSize *string `json:"window-size"`
}

func buildRoute(path, method string, item *v3.PathItem, op *v3.Operation, rawOp json.RawMessage, appPin time.Time) (Route, error) {
	if op.OperationId == "" {
		return Route{}, fmt.Errorf("operation has no operationId")
	}

	var ext rawExtensions
	if len(rawOp) > 0 {
		if err := json.Unmarshal(rawOp, &ext); err != nil {
			return Route{}, fmt.Errorf("parsing vendor extensions: %w", err)
		}
	}
	if ext.CompatibilityDate == "" {
		return Route{}, fmt.Errorf("operation %s has no x-compatibility-date", op.OperationId)
	}
	compatDate, err := ParseDate(ext.CompatibilityDate)
	if err != nil {
		return Route{}, fmt.Errorf("operation %s: x-compatibility-date %q: %w", op.OperationId, ext.CompatibilityDate, err)
	}

	route := Route{
		OperationID:       op.OperationId,
		Method:            method,
		UpstreamPath:      path, // verbatim: the spec's own path key, never touched
		CompatibilityDate: compatDate,
		BlockedByPin:      compatDate.After(appPin),
		SpecFragment:      json.RawMessage(rawOp),
		IdentifierTypes:   identifierTypes(mergedParameters(item, op)),
		Scopes:            operationScopes(op),
		Roles:             ext.RequiredRoles,
	}
	if len(route.SpecFragment) == 0 {
		route.SpecFragment = json.RawMessage(`{}`)
	}

	if ext.CacheAge != nil {
		d := time.Duration(*ext.CacheAge) * time.Second
		route.CacheAge = &d
	}
	// x-server-cache-mode is the authoritative field observed on the live
	// spec; x-cache-mode is carried alongside it on a subset of operations
	// and — where both are present — has never been seen to disagree.
	// Prefer the server-authoritative name, falling back to the older one
	// so an operation declaring only x-cache-mode is not silently dropped.
	switch {
	case ext.ServerCacheMode != nil:
		route.CacheMode = ext.ServerCacheMode
	case ext.CacheMode != nil:
		route.CacheMode = ext.CacheMode
	}
	if ext.RateLimit != nil {
		route.RateLimitGroup = ext.RateLimit.Group
		route.RateLimitMax = ext.RateLimit.MaxTokens
		if ext.RateLimit.WindowSize != nil {
			if d, err := time.ParseDuration(*ext.RateLimit.WindowSize); err == nil {
				route.RateLimitWindow = &d
			}
			// An unparseable window-size is recorded as absent rather than
			// failing the whole ingest — Governor 1 falls back to treating
			// an undeclared window conservatively (Phase 4 concern); losing
			// one route's rate-limit metadata must never lose the route.
		}
	}
	route.PaginationStyle = ext.Pagination

	return route, nil
}

// mergedParameters combines a PathItem's shared parameters with an
// Operation's own, per OpenAPI 3.1's own override rule: an operation-level
// parameter with the same (name, in) as a path-level one replaces it.
func mergedParameters(item *v3.PathItem, op *v3.Operation) []*v3.Parameter {
	type key struct{ name, in string }
	byKey := make(map[key]*v3.Parameter)
	var order []key
	add := func(p *v3.Parameter) {
		k := key{p.Name, p.In}
		if _, exists := byKey[k]; !exists {
			order = append(order, k)
		}
		byKey[k] = p
	}
	for _, p := range item.Parameters {
		add(p)
	}
	for _, p := range op.Parameters {
		add(p)
	}
	out := make([]*v3.Parameter, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

// identifierTypes maps every "%_id"-suffixed parameter to the Postgres
// identifier type its resolved OpenAPI schema implies
// (02_DATABASE_SCHEMA.md §3.2, Principle 13). SchemaProxy.Schema()
// transparently resolves a $ref — ESI's real spec declares almost every
// identifier parameter this way (e.g. {corporation_id} ->
// #/components/schemas/CorporationID) — so this never has to special-case
// inline versus referenced schemas.
func identifierTypes(params []*v3.Parameter) map[string]string {
	out := map[string]string{}
	for _, p := range params {
		if !strings.HasSuffix(p.Name, "_id") || p.Schema == nil {
			continue
		}
		schema := p.Schema.Schema()
		if schema == nil {
			continue
		}
		out[p.Name] = mapOpenAPIType(schema.Type, schema.Format)
	}
	return out
}

// mapOpenAPIType implements 02_DATABASE_SCHEMA.md §3.2's type table. An
// OpenAPI shape this table has no row for (a schema type this package has
// never seen) is recorded as "unknown" rather than guessed at or dropped —
// Principle 14's "ingested and surfaced, never rejected" applies here too,
// even though identifier typing is Principle 13's concern: an admin
// looking at app.esi_route.identifier_types must see exactly what the spec
// said, not a silently-assumed default.
func mapOpenAPIType(types []string, format string) string {
	has := func(t string) bool {
		for _, x := range types {
			if x == t {
				return true
			}
		}
		return false
	}
	switch {
	case has("integer") && format == "int64":
		return string(domain.IdentifierBigInt)
	case has("integer"):
		return "integer" // format: int32, or undeclared — still integer-shaped
	case has("string") && format == "uuid":
		return string(domain.IdentifierUUID)
	case has("string") && format == "date-time":
		return "timestamptz"
	case has("string"):
		return "text"
	case has("number"):
		return "numeric"
	default:
		return "unknown"
	}
}

// operationScopes flattens every security requirement's scope list into
// one set. ESI operations declare at most one requirement in practice
// ({"OAuth2": [...]}), but the spec permits several (logical OR between
// requirements) — every scope named anywhere is recorded, regardless of
// which scheme requires it, because app.esi_scope is opaque and keyed on
// the scope string alone (Principle 14).
func operationScopes(op *v3.Operation) []string {
	seen := map[string]bool{}
	var out []string
	for _, req := range op.Security {
		if req.Requirements == nil {
			continue
		}
		for _, scopes := range req.Requirements.FromOldest() {
			for _, s := range scopes {
				if !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
	}
	return out
}
