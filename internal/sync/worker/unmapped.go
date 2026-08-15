package worker

// unmapped.go answers Gate 4.2 for the routes SubscribableRoutes does not
// contain.
//
// ── DEFECT B47 (PHASE 20.6) ──────────────────────────────────────────────
// B47 was reported as one route: GET /corporations/{corporation_id}/assets
// had an owner-generic handler, ran for characters, and was absent from
// corporationDispatch, so no corporation's assets had ever synced. The fix
// for that one route is a map entry (corporation.go). This file exists
// because the fix for the CLASS is not.
//
// Gate 4.2's wording is the whole design constraint here: every measured ESI
// route must "map to at least one app.sync_subscription route, or be
// explicitly recorded as deliberately unmapped WITH A REASON". A route that
// is absent from both the dispatch tables and this registry is not
// "deliberately unmapped" — it is unnoticed, which is precisely what B47
// was, and "nobody wrote the map entry" is not a reason.
//
// So the partition is made total and machine-checked:
//
//	catalogued GET routes  ==  SubscribableRoutes()  ∪  DeliberatelyUnmapped()
//
// with the two sides disjoint. TestEveryCatalogedGetRouteIsClassified
// enforces it against the embedded spec snapshot, so a route CCP adds fails
// the build until somebody classifies it, rather than silently joining the
// set of routes nothing polls.
//
// ── SOME OF THESE REASONS ARE FAILURES, AND SAY SO ───────────────────────
// ReasonNotBuilt is not an excuse; it is a recorded Gate 4 blocker. Those
// routes back capabilities Appendix A claims as delivered, whose tables and
// API endpoints exist and whose sync handler does not, so the endpoint
// serves an empty array on every installation forever. They are enumerated
// here rather than quietly omitted precisely so the traceability CSV can
// count them and the gate can fail on the number.

// UnmappedReason is why a catalogued GET route carries no sync subscription.
type UnmappedReason string

const (
	// ReasonSDE — the data is served from the `sde.*` schema, populated by
	// `hangar admin import-sde` from CCP's Static Data Export. These routes
	// are per-item lookups over static reference data (a type, a station, a
	// solar system); polling them would be tens of thousands of requests to
	// rebuild a dataset CCP publishes as a single download, and SRS §12
	// requires the SDE stay joinable from `app` for exactly this reason.
	ReasonSDE UnmappedReason = "sde"

	// ReasonPostV1 — SRS §12's explicit post-v1.0 backlog. Named there with
	// a route count, so these are declared scope reductions (Gate 4.7's
	// "recorded as intentional, not counted as gaps"), not oversights.
	ReasonPostV1 UnmappedReason = "post-v1.0"

	// ReasonNotificationSourced — the capability is delivered, but its data
	// arrives through the character notifications feed rather than this
	// route. All six Wars alert types in app.alert_type carry source_kind =
	// 'esi_notification'; nothing HANGAR renders needs the /wars poll.
	ReasonNotificationSourced UnmappedReason = "notification-sourced"

	// ReasonOnDemand — answered at request time from rows HANGAR already
	// holds, never proxied upstream. Character search is the load-bearing
	// case: CCP's entity-discovery prohibition forbids offering ESI's search
	// as a lookup service, so /api/v1/support/search deliberately queries
	// only already-synced characters, corporations and alliances.
	ReasonOnDemand UnmappedReason = "on-demand"

	// ReasonNoCapability — catalogued by ESI, claimed by no Appendix A
	// capability and absent from the 106-route measured legacy baseline in
	// docs/BASELINE.md. HANGAR is a parity product; a route the legacy
	// system never called and no capability names is out of scope by
	// construction, not by omission.
	ReasonNoCapability UnmappedReason = "no-capability"

	// ReasonNotBuilt — ⚠ A GATE 4 FAILURE, RECORDED.
	//
	// An Appendix A capability names this route, the schema for it exists,
	// store queries for it are generated, and an /api/v1 endpoint serves it
	// — and no sync handler writes it, so the table is empty on every
	// installation and the endpoint returns `"data":[]` forever. Measured in
	// Phase 20.6 by finding every INSERT/UPDATE query with no production
	// caller (27 of 247) and intersecting that with the catalogue.
	//
	// These are NOT to be marked verified in the traceability CSV with a
	// note. Per 04_RELEASE_GATES.md §0.4 a recorded failure is the correct
	// artefact and a note is not.
	ReasonNotBuilt UnmappedReason = "not-built"

	// ReasonFetchedByParent — the route IS synced, inside another route's
	// pass, because it cannot have a subscription of its own.
	//
	// This is not ReasonNotBuilt (a handler exists and runs) and it is not
	// subscribable (there is nothing for a subscription to enumerate). The
	// only member is /killmails/{killmail_id}/{killmail_hash}: app.killmail
	// requires killmail_time NOT NULL and PARTITIONS on it, so no row can
	// exist before its detail is fetched, so a detail subscription would
	// enumerate an empty set forever. See worker/killmail_fanout.go.
	//
	// It is a distinct reason rather than a note on an existing one because
	// the two questions a reader has — "does anything fetch this?" and "does
	// anything schedule this?" — have different answers here, and collapsing
	// them into either existing reason would make one of them a lie.
	ReasonFetchedByParent UnmappedReason = "fetched-by-parent"

	// ReasonScopeNotGranted — a handler exists and is dispatched, and the
	// route's scope is not in the grant the acting token carries, so it
	// cannot return data on THIS installation until an operator enables the
	// scope in the developer portal and the character re-authorizes.
	//
	// Reserved deliberately and currently unused: the five scopes B48 needed
	// (killmails ×2, fittings, universe structures, alliance contacts) were
	// enabled by the operator during the phase, so the routes that need them
	// are subscribable rather than unmapped. It stays defined because the
	// NEXT route to need a new scope should be recorded as blocked on an
	// operator action rather than mislabelled "not built" — which is what
	// the 47th scope's disappearance (B47) cost a phase to discover.
	ReasonScopeNotGranted UnmappedReason = "scope-not-granted"
)

// DeliberatelyUnmapped is every catalogued GET route that carries no sync
// subscription, with the reason it carries none.
//
// A fresh map is returned per call; callers may mutate it freely.
func DeliberatelyUnmapped() map[string]UnmappedReason {
	return map[string]UnmappedReason{
		// ── Static reference data: the SDE serves this ───────────────────
		// Each of these has a corresponding sde.* table (category, group_,
		// type, region, constellation, solar_system, station, planet, moon,
		// race, bloodline, ancestry, faction, graphic, dogma_attribute,
		// dogma_effect, market_group, npc_corporation).
		"/universe/ancestries":                        ReasonSDE,
		"/universe/bloodlines":                        ReasonSDE,
		"/universe/categories":                        ReasonSDE,
		"/universe/categories/{category_id}":          ReasonSDE,
		"/universe/constellations":                    ReasonSDE,
		"/universe/constellations/{constellation_id}": ReasonSDE,
		"/universe/factions":                          ReasonSDE,
		"/universe/graphics":                          ReasonSDE,
		"/universe/graphics/{graphic_id}":             ReasonSDE,
		"/universe/groups":                            ReasonSDE,
		"/universe/groups/{group_id}":                 ReasonSDE,
		"/universe/moons/{moon_id}":                   ReasonSDE,
		"/universe/planets/{planet_id}":               ReasonSDE,
		"/universe/races":                             ReasonSDE,
		"/universe/regions":                           ReasonSDE,
		"/universe/regions/{region_id}":               ReasonSDE,
		"/universe/schematics/{schematic_id}":         ReasonSDE,
		"/universe/stargates/{stargate_id}":           ReasonSDE,
		"/universe/stars/{star_id}":                   ReasonSDE,
		"/universe/systems":                           ReasonSDE,
		"/universe/systems/{system_id}":               ReasonSDE,
		"/universe/types":                             ReasonSDE,
		"/universe/types/{type_id}":                   ReasonSDE,
		"/universe/asteroid_belts/{asteroid_belt_id}": ReasonSDE,
		"/dogma/attributes":                           ReasonSDE,
		"/dogma/attributes/{attribute_id}":            ReasonSDE,
		"/dogma/effects":                              ReasonSDE,
		"/dogma/effects/{effect_id}":                  ReasonSDE,
		"/markets/groups":                             ReasonSDE,
		"/markets/groups/{market_group_id}":           ReasonSDE,
		"/corporations/npccorps":                      ReasonSDE,

		// ── SRS §12: explicit post-v1.0 backlog ──────────────────────────
		// Military Campaigns (6), Freelance Jobs (4), Access Lists (2),
		// Mercenary Dens & Tactical Operations (4), public contracts (3),
		// structure market orders (1), Fleets (7 — never implemented in
		// legacy SeAT either).
		"/military-campaigns":                                                     ReasonPostV1,
		"/military-campaigns/{campaign_id}":                                       ReasonPostV1,
		"/military-campaigns/{campaign_id}/objectives":                            ReasonPostV1,
		"/military-campaigns/{campaign_id}/objectives/{objective_id}":             ReasonPostV1,
		"/characters/{character_id}/military-campaigns/objectives":                ReasonPostV1,
		"/characters/{character_id}/military-campaigns/objectives/{objective_id}": ReasonPostV1,
		"/freelance-jobs":                                                         ReasonPostV1,
		"/freelance-jobs/{job_id}":                                                ReasonPostV1,
		"/characters/{character_id}/freelance-jobs":                               ReasonPostV1,
		"/characters/{character_id}/freelance-jobs/{job_id}/participation":        ReasonPostV1,
		"/corporations/{corporation_id}/freelance-jobs":                           ReasonPostV1,
		"/corporations/{corporation_id}/freelance-jobs/{job_id}/participants":     ReasonPostV1,
		"/characters/{character_id}/access-lists":                                 ReasonPostV1,
		"/characters/{character_id}/access-lists/{access_list_id}":                ReasonPostV1,
		"/characters/{character_id}/mercenary-tactical-operations":                ReasonPostV1,
		"/characters/{character_id}/mercenary-tactical-operations/{operation_id}": ReasonPostV1,
		"/characters/{character_id}/structures/mercenary-dens":                    ReasonPostV1,
		"/characters/{character_id}/structures/mercenary-dens/{mercenary_den_id}": ReasonPostV1,
		"/contracts/public/{region_id}":                                           ReasonPostV1,
		"/contracts/public/items/{contract_id}":                                   ReasonPostV1,
		"/contracts/public/bids/{contract_id}":                                    ReasonPostV1,
		"/markets/structures/{structure_id}":                                      ReasonPostV1,
		"/fleets/{fleet_id}":                                                      ReasonPostV1,
		"/fleets/{fleet_id}/members":                                              ReasonPostV1,
		"/fleets/{fleet_id}/wings":                                                ReasonPostV1,
		"/characters/{character_id}/fleet":                                        ReasonPostV1,

		// ── Delivered, but sourced from the notifications feed ───────────
		"/wars":                    ReasonNotificationSourced,
		"/wars/{war_id}":           ReasonNotificationSourced,
		"/wars/{war_id}/killmails": ReasonNotificationSourced,

		// ── Answered locally at request time ─────────────────────────────
		"/characters/{character_id}/search": ReasonOnDemand,

		// ── Catalogued by ESI, claimed by no capability ──────────────────
		// Factional warfare, incursions, industry indices, loyalty stores
		// and the raidable-skyhook feed appear in no Appendix A capability
		// and in none of the 106 measured legacy routes. /meta/changelog,
		// /meta/compatibility-dates and /meta/name are read by HANGAR's
		// catalogue-ingest and pin machinery as SPEC metadata, not polled as
		// data. (/meta/status is a different matter entirely — it is
		// capability #45 and it is NOT built; see ReasonNotBuilt below.)
		//
		// /dogma/dynamic/items/{type_id}/{item_id} is the mutated-module
		// (abyssal) attribute lookup. It is not SDE data — the values are
		// per-item and generated at mutation time, so the SDE cannot carry
		// them — and no capability names it.
		"/fw/leaderboards":                         ReasonNoCapability,
		"/fw/leaderboards/characters":              ReasonNoCapability,
		"/fw/leaderboards/corporations":            ReasonNoCapability,
		"/fw/stats":                                ReasonNoCapability,
		"/fw/systems":                              ReasonNoCapability,
		"/fw/wars":                                 ReasonNoCapability,
		"/characters/{character_id}/fw/stats":      ReasonNoCapability,
		"/corporations/{corporation_id}/fw/stats":  ReasonNoCapability,
		"/incursions":                              ReasonNoCapability,
		"/industry/facilities":                     ReasonNoCapability,
		"/industry/systems":                        ReasonNoCapability,
		"/loyalty/stores/{corporation_id}/offers":  ReasonNoCapability,
		"/skyhooks/raidable":                       ReasonNoCapability,
		"/universe/system_jumps":                   ReasonNoCapability,
		"/universe/system_kills":                   ReasonNoCapability,
		"/universe/structures":                     ReasonNoCapability,
		"/meta/changelog":                          ReasonNoCapability,
		"/meta/compatibility-dates":                ReasonNoCapability,
		"/meta/name":                               ReasonNoCapability,
		"/characters/{character_id}/portrait":      ReasonNoCapability,
		"/corporations/{corporation_id}/icons":     ReasonNoCapability,
		"/alliances/{alliance_id}/icons":           ReasonNoCapability,
		"/dogma/dynamic/items/{type_id}/{item_id}": ReasonNoCapability,

		// ── SYNCED INSIDE ANOTHER ROUTE'S PASS ───────────────────────────
		// Capability #39's detail route. It has a handler and it runs; what
		// it does not have is a subscription, because app.killmail cannot
		// hold a row before this route has answered (killmail_time is NOT
		// NULL and is the partition key), so nothing would ever be there for
		// a detail subscription to enumerate. Fetched by the two recent-list
		// routes' own passes — worker/killmail_fanout.go.
		killmailDetailPath: ReasonFetchedByParent,

		// ── PHASE 20.7: RECLASSIFIED, NOT WIRED ──────────────────────────
		//
		// These four were recorded as ReasonNotBuilt by Phase 20.6's sweep,
		// which derived them from "an INSERT with no production caller"
		// intersected with the catalogue. That measurement was sound and its
		// conclusion was wrong for these: the endpoints they were supposed
		// to back are not backed by them at all.
		//
		// ── The two regional market routes ───────────────────────────────
		// GET /api/v1/markets/{region_id}/orders is documented, in its own
		// handler and in ListMarketOrdersByRegion's SQL comment, as "orders
		// HANGAR has synced FOR TRACKED OWNERS in this region" — a
		// region-scoped projection over app.market_order, which is written
		// by the character and corporation order syncs and has been since
		// Phase 8. /types is the same set's distinct type ids. Neither is a
		// mirror of ESI's public regional order book, and mirroring one is a
		// different product: ~300,000 live orders for a single major trade
		// hub, re-fetched at the route's cadence, for a question no HANGAR
		// screen asks. SRS §12 already places public/structure market orders
		// in the post-v1.0 backlog, which is the same judgement.
		//
		// app.market_order holding 0 rows on the live installation is
		// therefore NOT evidence of a missing writer. The writer exists and
		// runs; CEODude simply has no market orders. Established the way
		// every empty in this phase was — by reading the cached upstream
		// body, not by trusting the count.
		"/markets/{region_id}/orders": ReasonNoCapability,
		"/markets/{region_id}/types":  ReasonNoCapability,

		// ── /alliances, the global id list ───────────────────────────────
		// Returns every alliance id in New Eden. HANGAR resolves the
		// alliances it REFERENCES (the stubs the identity syncs create), not
		// a directory of entities this installation has no relationship
		// with — see handlers/alliance.go. Nothing reads a list of all
		// alliance ids, and GET /api/v1/alliances serves app.alliance, which
		// the per-alliance sheet sync fills.
		"/alliances": ReasonNoCapability,

		// ── ⚠ GATE 4 FAILURES STILL RECORDED (ReasonNotBuilt) ────────────
		//
		// Capability #37's contacts half. app.contact/contact_label carry an
		// 'alliance' owner_kind and GET /api/v1/alliances/{id}/contacts reads
		// it, and the two routes that would fill it are NOT wired.
		//
		// The blocker is structural, not clerical, and is recorded rather
		// than worked around: both routes need a token from a character IN
		// the alliance, which means an alliance-scoped acting-character
		// election. internal/sync.EntityAlliance exists in the vocabulary,
		// but there is no AllianceWorker and no elector for it — DispatchWorker
		// routes character, corporation and global only. Making these two
		// routes work means building that worker, which is a phase's own
		// piece of work and not a map entry.
		//
		// The alliance SHEET and MEMBER-CORPORATION routes do not have this
		// problem (both are public) and are also not wired, for the narrower
		// reason that they need a global fan-out over app.alliance that this
		// phase did not build. app.alliance holds 0 rows on this installation
		// — HANGAR Corp is in no alliance — so neither route would have had
		// anything to resolve.
		//
		// Handlers for all four WERE written in 20.7 and then DELETED, on
		// purpose. Unwired handlers are defect class B20 — code that is
		// built, tested and never called — and test/reachability catches it;
		// keeping them would have been the same "written but unreachable"
		// disease this phase spent itself removing, with a comment attached.
		// The design is recorded here instead, which is where the next phase
		// will look.
		"/alliances/{alliance_id}":                 ReasonNotBuilt,
		"/alliances/{alliance_id}/contacts":        ReasonNotBuilt,
		"/alliances/{alliance_id}/contacts/labels": ReasonNotBuilt,
		"/alliances/{alliance_id}/corporations":    ReasonNotBuilt,

		// Capability #41's two resolution routes. app.location exists and
		// UpsertLocation still has no production caller: both routes need a
		// fan-out over the location ids HANGAR has already seen in its own
		// synced rows (app.asset.location_id and friends), and that
		// enumeration query has not been written. GET
		// /api/v1/support/universe/{structures,stations} still 404 on every
		// id.
		//
		// Handlers were written in 20.7 and DELETED unwired, for the same
		// reason the alliance ones were — see above. Note that the ids ARE
		// now available: app.asset holds character- and corporation-owned
		// rows carrying location_ids, so the enumeration this needs would
		// have something real to resolve on the next attempt.
		"/universe/structures/{structure_id}": ReasonNotBuilt,
		"/universe/stations/{station_id}":     ReasonNotBuilt,
	}
}

// UnmappedByReason groups DeliberatelyUnmapped by reason — the shape the
// Gate 4 traceability generator counts.
func UnmappedByReason() map[UnmappedReason][]string {
	out := map[UnmappedReason][]string{}
	for path, reason := range DeliberatelyUnmapped() {
		out[reason] = append(out[reason], path)
	}
	return out
}
