// Package v1 implements every operation under /api/v1 (SRS §6.1–6.8). It
// is organised one file per SRS subsection (auth.go → §6.1, characters.go
// → §6.2, ...) plus this file, which holds the shared Huma wiring every
// other file in the package reuses: common path/query input shapes, the
// generic owner-scoped list/detail registration helpers, and RegisterAll,
// the single entrypoint cmd/hangar/serve.go and cmd/hangar/openapi.go both
// call.
//
// internal/api/middleware/authorize.go, internal/api/v1/admin_provisioning.go
// and internal/api/v1/public_mumble_auth.go are Phase 10/11/13's
// documented placeholder seams — this package wires Huma handlers directly
// on top of them rather than duplicating their logic.
package v1

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/api/dto"
	"github.com/hangar-project/hangar/internal/api/filters"
	apimw "github.com/hangar-project/hangar/internal/api/middleware"
)

// rowOf/rowSliceOf/jsonMarshal are package-private forwarders to
// internal/api/dto so every file in this package can build a generic
// response body without a per-file dto import line.
func rowOf(v any) map[string]any                  { return dto.Row(v) }
func rowSliceOf[T any](rows []T) []map[string]any { return dto.RowSlice(rows) }
func jsonMarshal(v any) ([]byte, error)           { return json.Marshal(v) }
func parseUUID(s string) (uuid.UUID, error)       { return uuid.Parse(s) }

// userIDFrom extracts the authenticated user id middleware.ResolveSession
// populated, from a huma.Context.
func userIDFrom(ctx huma.Context) (uuid.UUID, bool) {
	return apimw.UserIDFromContext(ctx.Context())
}

// RegisterAll registers every /api/v1 operation against hapi. deps.Store
// may be nil (cmd/hangar/openapi.go's spec-only build path) — every
// handler closure only dereferences it once an actual request arrives.
func RegisterAll(hapi huma.API, deps api.Deps) {
	registerAuth(hapi, deps)
	registerCharacters(hapi, deps)
	registerCorporations(hapi, deps)
	registerAlliancesAndMarket(hapi, deps)
	registerSquads(hapi, deps)
	registerSupport(hapi, deps)
	registerAdmin(hapi, deps)
	registerAdminRoles(hapi, deps)
	registerAdminRules(hapi, deps)
	registerTeamspeakLink(hapi, deps)
	registerWebhooks(hapi, deps)
}

// ---- shared input/output shapes -------------------------------------------------

// IDIn is a single numeric EVE id path parameter — every §6.2/§6.3
// `/{characters,corporations,alliances}/{id}/...` route uses it.
type IDIn struct {
	ID int64 `path:"id" doc:"Numeric EVE id (character, corporation, alliance...)."`
}

// UUIDIn is a single UUID path parameter — squads, projects, structures'
// UUID-keyed sub-resources.
type UUIDIn struct {
	ID string `path:"id" format:"uuid" doc:"HANGAR-assigned UUID."`
}

// SubIDIn is an owner id plus a nested numeric sub-resource id, e.g.
// /characters/{id}/mail/{mail_id}.
type SubIDIn struct {
	ID    int64 `path:"id"`
	SubID int64 `path:"sub_id"`
}

// PageIn is the standard cursor-pagination query parameters — SRS §6:
// "limit accepts 10–100 with a default of 50."
type PageIn struct {
	After  string `query:"after" doc:"Opaque forward cursor; the literal '0' means start-of-set."`
	Before string `query:"before" doc:"Opaque backward cursor; the literal '0' means end-of-set. Mutually exclusive with 'after'."`
	Limit  int32  `query:"limit" default:"50" doc:"Page size, 10-100, default 50."`
}

// IDPageIn is IDIn+PageIn combined — the shape of every paginated
// owner-scoped collection route.
type IDPageIn struct {
	ID     int64  `path:"id"`
	After  string `query:"after"`
	Before string `query:"before"`
	Limit  int32  `query:"limit" default:"50"`
}

// CollectionOut is the generic list-endpoint response body. Endpoints that
// warrant a hand-typed row shape define their own Out struct instead (see
// e.g. support.go's search response, admin.go's dead-letter board).
type CollectionOut struct {
	Body api.Collection[map[string]any]
}

// ItemOut is the generic detail-endpoint response body.
type ItemOut struct {
	Body api.Item[map[string]any]
}

// ---- registration helpers --------------------------------------------------

// get registers a GET operation gated by permission (empty string means
// "authenticated only" — no RBAC permission beyond a resolved session;
// see the per-file comments on which SRS §6 groups have no matching
// closed-vocabulary permission yet, a gap this phase reports rather than
// invents an ad hoc permission name for).
func get[I, O any](hapi huma.API, deps api.Deps, permission, path, opID, summary string, tag string, handler func(context.Context, *I) (*O, error)) {
	op := huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{tag},
	}
	if permission != "" {
		op.Middlewares = huma.Middlewares{api.RequirePermission(deps.Store, permission)}
	} else {
		op.Middlewares = huma.Middlewares{requireAuthenticated()}
	}
	// PHASE 20.5 (B33). The filter whitelist, derived from *I's own `query:`
	// tags — the same tags huma builds the OpenAPI document's parameter list
	// from, so the closed set and the documented set are one thing. Ordered
	// AFTER the permission guard deliberately: an unauthorised caller must
	// learn they are unauthorised, not which query parameters exist.
	op.Middlewares = append(op.Middlewares, api.ValidateQueryFilters(hapi, queryFilterSpec[I](opID)))
	huma.Register(hapi, op, handler)
}

// queryFilterSpec builds one operation's closed filter set from its input
// type. Called once per operation at registration, never per request.
func queryFilterSpec[I any](opID string) filters.Spec {
	var zero I
	return filters.SpecFromQueryTags(opID, zero)
}

func mutate[I, O any](hapi huma.API, deps api.Deps, method, permission, path, opID, summary, tag string, handler func(context.Context, *I) (*O, error)) {
	op := huma.Operation{
		OperationID: opID,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        []string{tag},
	}
	if permission != "" {
		op.Middlewares = huma.Middlewares{api.RequirePermission(deps.Store, permission)}
	} else {
		op.Middlewares = huma.Middlewares{requireAuthenticated()}
	}
	// A mutation's query string is whitelisted on the same terms as a
	// collection's. A POST is the LAST place an ignored, narrowing parameter
	// should pass silently.
	op.Middlewares = append(op.Middlewares, api.ValidateQueryFilters(hapi, queryFilterSpec[I](opID)))
	huma.Register(hapi, op, handler)
}

// requireAuthenticated is the floor every /api/v1 route (other than the
// one deliberately unauthenticated write route,
// POST /api/v1/public/mumble/auth) sits behind, even where no specific
// RBAC permission from the closed vocabulary applies: a resolved session
// via middleware.ResolveSession/WithUserID is still mandatory.
func requireAuthenticated() func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if _, ok := userIDFrom(ctx); !ok {
			_, w := humago.Unwrap(ctx)
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		next(ctx)
	}
}

// ownerListHandler adapts a `func(ctx, id) ([]Row, error)` fetch closure
// (typically `func(ctx context.Context, id int64) ([]Row, error) { return
// deps.Store.List<Resource>(ctx, id) }`) into a huma handler returning a
// single, unpaginated Collection — the correct shape for the many
// §6.2/§6.3 sub-resources that are naturally bounded per owner (skills,
// clones, titles, standings, divisions, ...) rather than needing true
// keyset pagination. fetch must close over deps itself (not take it as a
// parameter) so nothing dereferences deps.Store until a real request
// arrives — deps.Store is nil during cmd/hangar/openapi.go's spec-only
// build, and forming a bound method value from a nil *store.Store at
// registration time (rather than a closure that defers the access) would
// panic immediately.
func ownerListHandler[Row any](fetch func(ctx context.Context, id int64) ([]Row, error)) func(context.Context, *IDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *IDIn) (*CollectionOut, error) {
		rows, err := fetch(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing resource", err)
		}
		data := dto.RowSlice(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data,
			Page: api.EmptyPage(int32(len(data))),
			Sync: api.Sync{},
		}}, nil
	}
}

// ownerDetailHandler is ownerListHandler's single-resource counterpart.
func ownerDetailHandler[Row any](fetch func(ctx context.Context, id int64) (Row, error)) func(context.Context, *IDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *IDIn) (*ItemOut, error) {
		row, err := fetch(ctx, in.ID)
		if err != nil {
			return nil, api.NotFound("resource")
		}
		data := dto.Row(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}
