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

		// ── ⚠ GATE 4 FAILURES, RECORDED (ReasonNotBuilt) ─────────────────
		// Each line: the Appendix A capability that claims it, the table
		// that exists for it, and the /api/v1 endpoint already serving that
		// table's emptiness.

		// Capability #37–39 "Killmails (character, corporation, detail)".
		// app.killmail / killmail_attacker / killmail_item exist;
		// UpsertKillmail, UpsertKillmailAttacker and UpsertKillmailItem have
		// no production caller. GET /api/v1/characters/{id}/killmails and
		// /corporations/{id}/killmails read ListKillmailsByOwner and return
		// [] on every installation.
		"/characters/{character_id}/killmails/recent":     ReasonNotBuilt,
		"/corporations/{corporation_id}/killmails/recent": ReasonNotBuilt,
		"/killmails/{killmail_id}/{killmail_hash}":        ReasonNotBuilt,

		// Capability #1–14 "Fittings (+ EFT export)". app.character_fitting
		// and character_fitting_item exist; UpsertCharacterFitting,
		// UpsertCharacterFittingItem and DeleteCharacterFittingsNotIn have
		// no production caller. Three /api/v1 handlers read
		// ListCharacterFittings, including the EFT export.
		"/characters/{character_id}/fittings": ReasonNotBuilt,

		// Capability #1–14 "Notifications (+ contact notifications)".
		// app.notification_contact exists; UpsertNotificationContact has no
		// production caller.
		"/characters/{character_id}/notifications/contacts": ReasonNotBuilt,

		// Capability #40–44 "Insurance Prices". app.insurance_price exists;
		// UpsertInsurancePrice has no production caller. GET
		// /api/v1/tools/insurance reads ListInsurancePrices.
		"/insurance/prices": ReasonNotBuilt,

		// Capability #40–44 "ID & Name Resolution (+ affiliations,
		// structures, stations)". app.location exists; UpsertLocation has no
		// production caller. GET /api/v1/support/universe/{structures,
		// stations} read GetLocation and 404 on every id.
		"/universe/structures/{structure_id}": ReasonNotBuilt,
		"/universe/stations/{station_id}":     ReasonNotBuilt,

		// Capability #18–30 "Projects (list, detail, contributors,
		// per-character contribution)". The list and contributors halves are
		// synced; the project DETAIL route and the per-character
		// contribution route have no handler.
		// app.corporation_project_contribution exists.
		"/corporations/{corporation_id}/projects/{project_id}":                             ReasonNotBuilt,
		"/corporations/{corporation_id}/projects/{project_id}/contribution/{character_id}": ReasonNotBuilt,

		// Capability #45 "ESI Service Health (/meta/status)", and Gate 4.6's
		// requirement that /meta/status and /status be "present as two
		// distinct capabilities". /status is delivered (globalDispatch, into
		// app.setting). /meta/status is not: no dispatch entry, no handler,
		// no table. GET /api/v1/meta/esi-status exists and answers from the
		// blocked-route count with a hard-coded `"healthy": true` — a
		// derived proxy for ESI's health that never asks ESI, so the field
		// is asserted rather than measured and cannot ever report unhealthy.
		"/meta/status": ReasonNotBuilt,

		// Capability #34–36 "Global Market (regional orders, history, types,
		// prices)". History and prices are in globalDispatch; regional
		// ORDERS and TYPES are not, and app.market_order holds 0 rows.
		"/markets/{region_id}/orders": ReasonNotBuilt,
		"/markets/{region_id}/types":  ReasonNotBuilt,

		// Capability #37–39 "Alliance Information, Members & Contacts".
		// app.alliance exists but only UpsertAllianceStub is called (from
		// the character/structure identity syncs, which write an id with an
		// empty name); UpsertAlliance — the one that stores the real
		// alliance sheet — has no production caller, and there is no
		// alliance sync at all, so alliance contacts and member corporations
		// are never fetched.
		"/alliances":                               ReasonNotBuilt,
		"/alliances/{alliance_id}":                 ReasonNotBuilt,
		"/alliances/{alliance_id}/contacts":        ReasonNotBuilt,
		"/alliances/{alliance_id}/contacts/labels": ReasonNotBuilt,
		"/alliances/{alliance_id}/corporations":    ReasonNotBuilt,
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
