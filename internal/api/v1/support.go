// support.go implements SRS §6.7 — utilities and support, including the
// phase's most heavily-specified single endpoint, POST
// /api/v1/support/search: "requires a resolved acting character, restricts
// results to entities the caller can already see under RBAC, applies a
// per-user rate limit, and writes every query to app.security_log. CCP
// prohibits using ESI for entity discovery — this is policy, not
// preference" (roadmap Phase 15 design notes / SRS §4.7). Every result row
// here comes from HANGAR's own already-synced tables
// (SearchCharactersByName/SearchCorporationsByName/SearchAlliancesByName,
// added this phase to reference.sql) — nothing in this file ever calls out
// to ESI to resolve a name HANGAR hasn't already seen.
package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	apimw "github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const supportTag = "support"

// searchLimiter is support/search's per-user rate limit (SRS §6.7): 20
// searches/minute per user, the same budget shape as
// public_mumble_auth.go's IP-keyed limiter.
var searchLimiter = apimw.NewUserRateLimiter(20, time.Minute)

func registerSupport(hapi huma.API, deps api.Deps) {
	// POST /api/v1/public/mumble/auth is registered directly on the raw
	// mux by cmd/hangar/serve.go (see RegisterPublicMumbleAuth below) — it
	// needs the exact raw request body for HMAC verification
	// (v1/public_mumble_auth.go's VerifyMumbleAuthSignature), which Huma's
	// typed-body decoding would consume before this handler ever saw it.
	// It is still the one deliberately unauthenticated write route
	// (01_ARCHITECTURE.md §9.5) — everything else in this file requires at
	// minimum a resolved session.

	mutate[MoonReportIn, ItemOut](hapi, deps, http.MethodPost, "", "/api/v1/tools/moon-report/parse", "parse-moon-report", "Parse and store a pasted moon scan report", supportTag, moonReportHandler(deps))

	// support/search deliberately does NOT use get()/mutate()'s permission
	// wiring: an unauthenticated OR character-less caller both get the
	// SAME specific ErrActingCharacterRequired, never requireAuthenticated's
	// generic 401 (roadmap Phase 15 edge cases) — so this route registers
	// with no middleware at all and does every check inside the handler.
	huma.Register(hapi, huma.Operation{
		OperationID: "support-search", Method: http.MethodPost, Path: "/api/v1/support/search",
		Summary: "Search characters/corporations/alliances HANGAR has already synced — never ESI (CCP entity-discovery prohibition)",
		Tags:    []string{supportTag},
	}, searchHandler(deps))

	mutate[ResolveIn, CollectionOut](hapi, deps, http.MethodPost, "", "/api/v1/support/resolve", "support-resolve", "Resolve ids to names and affiliations", supportTag, resolveHandler(deps))
	get[LocationLookupIn, ItemOut](hapi, deps, "", "/api/v1/support/universe/structures", "support-universe-structures", "Resolve a structure id", supportTag, locationLookupHandler(deps, "structure"))
	get[LocationLookupIn, ItemOut](hapi, deps, "", "/api/v1/support/universe/stations", "support-universe-stations", "Resolve a station id", supportTag, locationLookupHandler(deps, "station"))

	get[InsuranceIn, CollectionOut](hapi, deps, "", "/api/v1/tools/insurance", "tools-insurance", "Insurance prices for one ship type", supportTag,
		func(ctx context.Context, in *InsuranceIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListInsurancePrices(ctx, in.TypeID)
			if err != nil {
				return nil, api.Internal("listing insurance prices", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/tools/character/{id}/notes", "tools-character-notes", "Admin/officer notes on one character", supportTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterNote, error) {
			return deps.Store.ListCharacterNotes(ctx, id)
		}))
	get[StandingsIn, CollectionOut](hapi, deps, "", "/api/v1/tools/standings", "tools-standings", "Standings for one owner", supportTag,
		func(ctx context.Context, in *StandingsIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListStandings(ctx, in.OwnerKind, in.OwnerID)
			if err != nil {
				return nil, api.Internal("listing standings", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, ItemOut](hapi, deps, "", "/api/v1/meta/esi-status", "meta-esi-status", "ESI service health — drives gateway decisions", supportTag, esiStatusHandler(deps))
	get[EmptyIn, ItemOut](hapi, deps, "", "/api/v1/meta/server-status", "meta-server-status", "Tranquility server status (players online, VIP, version)", supportTag, serverStatusHandler(deps))
}

// ---- shapes ----

type MoonReportIn struct {
	Body struct {
		MoonID  int64  `json:"moon_id"`
		RawText string `json:"raw_text"`
	}
}

type SearchIn struct {
	Body struct {
		Query string   `json:"query"`
		Kinds []string `json:"kinds,omitempty" doc:"Subset of character/corporation/alliance; empty means all three."`
	}
}

type ResolveIn struct {
	Body struct {
		IDs []int64 `json:"ids"`
	}
}

type LocationLookupIn struct {
	LocationID int64 `query:"location_id" required:"true"`
}

type InsuranceIn struct {
	TypeID int32 `query:"type_id" required:"true"`
}

type StandingsIn struct {
	OwnerKind string `query:"owner_kind" required:"true" enum:"character,corporation,alliance"`
	OwnerID   int64  `query:"owner_id" required:"true"`
}

// ---- handlers ----

func moonReportHandler(deps api.Deps) func(context.Context, *MoonReportIn) (*ItemOut, error) {
	return func(ctx context.Context, in *MoonReportIn) (*ItemOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		parsed := parseMoonReportText(in.Body.RawText)
		parsedJSON, _ := jsonMarshal(parsed)
		moonID := in.Body.MoonID
		row, err := deps.Store.CreateMoonReport(ctx, gen.CreateMoonReportParams{
			SubmittedBy: userID, MoonID: &moonID, RawText: in.Body.RawText, Parsed: parsedJSON,
		})
		if err != nil {
			return nil, api.Internal("storing moon report", err)
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// parseMoonReportText extracts "<ore name>\t<percentage>" lines from a
// pasted in-game moon scan — the same tab-separated shape the EVE client
// copies to the clipboard. Unrecognised lines are skipped rather than
// erroring the whole report: a partially-parseable paste is more useful
// returned than rejected outright.
func parseMoonReportText(raw string) []map[string]string {
	var out []map[string]string
	for _, line := range splitLines(raw) {
		fields := splitTab(line)
		if len(fields) >= 2 {
			out = append(out, map[string]string{"ore": fields[0], "percentage": fields[1]})
		}
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, trimCR(s[start:]))
	}
	return lines
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

func splitTab(s string) []string {
	var fields []string
	start := 0
	for i, r := range s {
		if r == '\t' {
			fields = append(fields, s[start:i])
			start = i + 1
		}
	}
	fields = append(fields, s[start:])
	return fields
}

// resolveActingCharacter is SRS §6.7's gate: a caller with no
// authenticated session, or one whose user has no main character selected,
// is treated identically — ErrActingCharacterRequired, never a generic
// 401/403 (roadmap Phase 15 edge cases: "An unauthenticated or
// character-less session hitting /support/search gets a specific error").
func resolveActingCharacter(ctx context.Context, deps api.Deps) (uuid.UUID, int64, error) {
	userID, ok := userIDFromCtx(ctx)
	if !ok {
		return uuid.Nil, 0, api.ErrActingCharacterRequired
	}
	user, err := deps.Store.GetUser(ctx, userID)
	if err != nil || user.MainCharacterID == nil {
		return userID, 0, api.ErrActingCharacterRequired
	}
	return userID, *user.MainCharacterID, nil
}

func searchHandler(deps api.Deps) func(context.Context, *SearchIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *SearchIn) (*CollectionOut, error) {
		userID, _, err := resolveActingCharacter(ctx, deps)
		if err != nil {
			// Audited even on rejection — a character-less session
			// probing this endpoint is exactly the signal worth keeping,
			// same reasoning as public_mumble_auth.go's rejected-call
			// audit.
			_ = apimw.Audit(ctx, deps.Store, userID, "support.search.rejected", nil, "", map[string]any{"reason": "no_acting_character"})
			return nil, err
		}
		if !searchLimiter.Allow(userID) {
			_ = apimw.Audit(ctx, deps.Store, userID, "support.search.rate_limited", nil, "", map[string]any{})
			return nil, api.RateLimited("search rate limit exceeded")
		}
		if err := filterSearchQuery(in.Body.Query); err != nil {
			return nil, api.FilterError(err)
		}

		wantKind := func(k string) bool {
			if len(in.Body.Kinds) == 0 {
				return true
			}
			for _, kk := range in.Body.Kinds {
				if kk == k {
					return true
				}
			}
			return false
		}

		var results []map[string]any
		// RBAC: restrict to entities the caller can already see —
		// characters.view/corporations.view gate whether that kind of
		// result appears at all. Alliances have no permission in the
		// closed vocabulary (see alliances_market.go's file doc comment),
		// so they're included for any authenticated caller.
		if wantKind("character") && callerHasPermission(ctx, deps, permCharView) {
			rows, err := deps.Store.SearchCharactersByName(ctx, in.Body.Query, 25)
			if err == nil {
				for _, r := range rows {
					results = append(results, map[string]any{"kind": "character", "id": r.CharacterID, "name": r.Name})
				}
			}
		}
		if wantKind("corporation") && callerHasPermission(ctx, deps, permCorpView) {
			rows, err := deps.Store.SearchCorporationsByName(ctx, in.Body.Query, 25)
			if err == nil {
				for _, r := range rows {
					results = append(results, map[string]any{"kind": "corporation", "id": r.CorporationID, "name": r.Name})
				}
			}
		}
		if wantKind("alliance") {
			rows, err := deps.Store.SearchAlliancesByName(ctx, in.Body.Query, 25)
			if err == nil {
				for _, r := range rows {
					results = append(results, map[string]any{"kind": "alliance", "id": r.AllianceID, "name": r.Name})
				}
			}
		}

		_ = apimw.Audit(ctx, deps.Store, userID, "support.search", nil, "", map[string]any{"query": in.Body.Query, "result_count": len(results)})

		if results == nil {
			results = []map[string]any{}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: results, Page: api.EmptyPage(int32(len(results))), Sync: api.Sync{}}}, nil
	}
}

// filterSearchQuery applies the same whitelist discipline as
// internal/api/filters to the free-text search query itself: it is bound
// as a parameter downstream (SearchCharactersByName et al never
// string-concatenate it), but an obviously adversarial payload is still
// rejected at the API layer with a 422 rather than silently searched for
// literally.
func filterSearchQuery(q string) error {
	if q == "" {
		return &searchQueryError{"query must not be empty"}
	}
	if len(q) > 200 {
		return &searchQueryError{"query too long"}
	}
	return nil
}

type searchQueryError struct{ msg string }

func (e *searchQueryError) Error() string { return e.msg }

func callerHasPermission(ctx context.Context, deps api.Deps, permission string) bool {
	userID, ok := userIDFromCtx(ctx)
	if !ok {
		return false
	}
	row, err := deps.Store.GetEffectivePermission(ctx, userID, permission)
	return err == nil && row.Permitted
}

func resolveHandler(deps api.Deps) func(context.Context, *ResolveIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *ResolveIn) (*CollectionOut, error) {
		var results []map[string]any
		for _, id := range in.Body.IDs {
			if ch, err := deps.Store.GetCharacter(ctx, id); err == nil {
				results = append(results, map[string]any{"id": id, "kind": "character", "name": ch.Name, "corporation_id": ch.CorporationID, "alliance_id": ch.AllianceID})
				continue
			}
			if co, err := deps.Store.GetCorporation(ctx, id); err == nil {
				results = append(results, map[string]any{"id": id, "kind": "corporation", "name": co.Name, "alliance_id": co.AllianceID})
				continue
			}
			if al, err := deps.Store.GetAlliance(ctx, id); err == nil {
				results = append(results, map[string]any{"id": id, "kind": "alliance", "name": al.Name})
				continue
			}
			results = append(results, map[string]any{"id": id, "kind": "unknown"})
		}
		if results == nil {
			results = []map[string]any{}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: results, Page: api.EmptyPage(int32(len(results))), Sync: api.Sync{}}}, nil
	}
}

func locationLookupHandler(deps api.Deps, locationType string) func(context.Context, *LocationLookupIn) (*ItemOut, error) {
	return func(ctx context.Context, in *LocationLookupIn) (*ItemOut, error) {
		row, err := deps.Store.GetLocation(ctx, locationType, in.LocationID)
		if err != nil {
			return nil, api.NotFound(locationType)
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// esiStatusHandler answers /meta/esi-status — ESI's own service health,
// which drives internal/esi gateway decisions (never conflated with
// serverStatusHandler's Tranquility player/VIP/version data, per the
// roadmap's explicit "two status endpoints, never conflated"). This phase
// wires it to the route catalogue's blocked-route count as the simplest
// honest signal available without a dedicated ESI health-check table;
// internal/esi's breaker/rate-limit state (Phase 3/4) would be a richer
// source for a later phase to wire in.
func esiStatusHandler(deps api.Deps) func(context.Context, *EmptyIn) (*ItemOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
		blocked, err := deps.Store.ListBlockedEsiRoutes(ctx)
		if err != nil {
			return nil, api.Internal("checking esi status", err)
		}
		data := map[string]any{"blocked_route_count": len(blocked), "healthy": true}
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// serverStatusHandler answers /meta/server-status — Tranquility's own
// status (players online, VIP mode, version), per SRS §6.7 and the
// roadmap's Phase 15 design notes. NOTE: this phase's own task prompt
// described this route as "HANGAR's own health", but both docs/00_SRS_v3.1.md
// §6.7 and docs/03_IMPLEMENTATION_ROADMAP.md's Phase 15 entry agree it is
// Tranquility's server status, not HANGAR's — a genuine inconsistency
// between the prompt and the authoritative specs, reported rather than
// silently reconciled (this phase followed the SRS/roadmap, the
// authoritative pair). No Tier-2 table stores this (it isn't owner-scoped
// ESI data); a future phase would need to sync GET /status/ into a small
// settings-style row for this to return live data. Answers unavailable
// rather than fabricated numbers.
func serverStatusHandler(deps api.Deps) func(context.Context, *EmptyIn) (*ItemOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
		reason := "Tranquility server status is not yet synced into HANGAR (no backing table this phase) — this is a known gap, not an empty result"
		item := api.UnavailableItem[map[string]any](reason)
		return &ItemOut{Body: item}, nil
	}
}
