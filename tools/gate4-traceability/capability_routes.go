package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/worker"
)

// CapabilitySpec is the declared delivery mapping for one Appendix A
// capability: which upstream routes deliver it, which legacy controllers it
// replaces, and where its verification lives.
//
// ── THIS IS THE ONE DECLARED INPUT, AND IT IS KEPT HONEST ────────────────
// Everything else this program prints is derived. This map is not: "which
// ESI routes deliver capability 14" is specification content (SRS §11), and
// no amount of code inspection recovers it — a route is not labelled with
// the capability it serves.
//
// What stops it from being a document that agrees with itself is
// checkRouteMapTotality: every route the sync engine can reach, and every
// route recorded as backing an unbuilt capability, must be claimed here by
// at least one capability. So this map cannot quietly omit a route in order
// to make a row look verified — omitting it fails the program instead.
//
// The reverse direction is checked too: a route named here that is neither
// subscribable nor classified is a typo or a retired path, and is an error.
type CapabilitySpec struct {
	Routes      []string
	Controllers []string
	AlertTypes  []string
	Endpoints   []string
	Phase       string
	Test        string
}

// capabilitySpecs is keyed by Appendix A capability id (1–58).
//
// Capabilities with no upstream ESI route — HANGAR-native administration,
// squads, provisioning, i18n — carry an empty Routes slice and are verified
// on their endpoints and tests alone. That is not a gap: Appendix A's own
// §11 wording is "one or more upstream ESI routes (or `n/a` for HANGAR-native
// features)".
var capabilitySpecs = map[int]CapabilitySpec{
	// ── Character (1–17) ─────────────────────────────────────────────────
	1: {
		Routes:      []string{"/characters/{character_id}/assets"},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/assets", "/api/v1/characters/{id}/assets/tree"},
		Phase:       "6", Test: "TestSyncAssets",
	},
	2: {
		Routes:      []string{"/characters/{character_id}/blueprints"},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/blueprints"},
		Phase:       "6", Test: "TestSyncBlueprints",
	},
	3: {
		Routes: []string{
			"/characters/{character_id}/calendar",
			"/characters/{character_id}/calendar/{event_id}",
			"/characters/{character_id}/calendar/{event_id}/attendees",
		},
		Endpoints: []string{"/api/v1/characters/{id}/calendar"},
		Phase:     "7", Test: "TestSyncCalendar",
	},
	4: {
		Routes: []string{
			"/characters/{character_id}/clones",
			"/characters/{character_id}/implants",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/clones"},
		Phase:       "7", Test: "TestSyncCharacterClones",
	},
	5: {
		Routes: []string{
			"/characters/{character_id}/contacts",
			"/characters/{character_id}/contacts/labels",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/contacts"},
		Phase:       "7", Test: "TestSyncCharacterContacts",
	},
	6: {
		Routes: []string{
			"/characters/{character_id}/contracts",
			"/characters/{character_id}/contracts/{contract_id}/items",
			"/characters/{character_id}/contracts/{contract_id}/bids",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/contracts"},
		Phase:       "9", Test: "TestSyncContracts",
	},
	7: {
		Routes:    []string{"/characters/{character_id}/fatigue"},
		Endpoints: []string{"/api/v1/characters/{id}"},
		Phase:     "7", Test: "TestSyncCharacterFatigue",
	},
	8: {
		Routes:    []string{"/characters/{character_id}/fittings"},
		Endpoints: []string{"/api/v1/characters/{id}/fittings", "/api/v1/characters/{id}/fittings/{sub_id}/eft"},
		// PHASE 20.7 (B48): sync handler written and dispatched; verified on
		// the live installation (1 fitting, 32 items, EFT export renders).
		// The SPA screen was added in the same phase — capability #8 had a
		// SECOND gap behind the writer: no fittings component and no route
		// existed, so the data was invisible in the app even once it landed.
		Phase: "7", Test: "TestSyncCharacterFittings",
	},
	9: {
		Routes: []string{
			"/characters/{character_id}/industry/jobs",
			"/characters/{character_id}/mining",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/industry"},
		Phase:       "8", Test: "TestSyncIndustryJobs",
	},
	10: {
		Routes: []string{
			"/characters/{character_id}/mail",
			"/characters/{character_id}/mail/labels",
			"/characters/{character_id}/mail/lists",
			"/characters/{character_id}/mail/{mail_id}",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/mail"},
		Phase:       "8", Test: "TestKeysetPagesAreTotallyOrdered",
	},
	11: {
		Routes: []string{
			"/characters/{character_id}/notifications",
			"/characters/{character_id}/notifications/contacts",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/notifications"},
		Phase:       "9", Test: "TestKeysetPagesAreTotallyOrdered",
	},
	12: {
		Routes: []string{
			"/characters/{character_id}/planets",
			"/characters/{character_id}/planets/{planet_id}",
		},
		Endpoints: []string{"/api/v1/characters/{id}/planets"},
		Phase:     "8", Test: "TestSyncPlanetColonies",
	},
	13: {
		Routes: []string{
			"/characters/{character_id}/skills",
			"/characters/{character_id}/skillqueue",
			"/characters/{character_id}/attributes",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/skills"},
		Phase:       "6", Test: "TestSyncCharacterSkills",
	},
	14: {
		Routes: []string{
			"/characters/{character_id}/wallet",
			"/characters/{character_id}/wallet/journal",
			"/characters/{character_id}/wallet/transactions",
		},
		Controllers: []string{"CharacterController"},
		Endpoints: []string{
			"/api/v1/characters/{id}/wallet/journal",
			"/api/v1/characters/{id}/wallet/transactions",
		},
		Phase: "8", Test: "TestKeysetPagesAreTotallyOrdered",
	},
	15: {
		Routes: []string{
			"/characters/{character_id}",
			"/characters/{character_id}/corporationhistory",
			"/characters/{character_id}/medals",
			"/characters/{character_id}/standings",
			"/characters/{character_id}/titles",
			"/characters/{character_id}/roles",
			"/characters/{character_id}/loyalty/points",
			"/characters/{character_id}/agents_research",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}"},
		Phase:       "6", Test: "TestSyncCharacterSheet",
	},
	16: {
		Routes: []string{
			"/characters/{character_id}/location",
			"/characters/{character_id}/online",
			"/characters/{character_id}/ship",
		},
		Endpoints: []string{"/api/v1/characters/{id}"},
		Phase:     "7", Test: "TestSyncCharacterLocation",
	},
	17: {
		Endpoints: []string{"/api/v1/characters/{id}/intel"},
		Phase:     "16", Test: "TestIntelGraph",
	},

	// ── Corporation (18–33) ──────────────────────────────────────────────
	18: {
		Routes:      []string{"/corporations/{corporation_id}/assets"},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/assets"},
		Phase:       "20.6", Test: "TestSyncCorporationAssets",
	},
	19: {
		Routes:      []string{"/corporations/{corporation_id}/blueprints"},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/blueprints"},
		Phase:       "8", Test: "TestSyncBlueprints",
	},
	20: {
		Routes: []string{
			"/corporations/{corporation_id}/contacts",
			"/corporations/{corporation_id}/contacts/labels",
		},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/contacts"},
		Phase:       "8", Test: "TestSyncCorporationContacts",
	},
	21: {
		Routes: []string{
			"/corporations/{corporation_id}/contracts",
			"/corporations/{corporation_id}/contracts/{contract_id}/items",
			"/corporations/{corporation_id}/contracts/{contract_id}/bids",
		},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/contracts"},
		Phase:       "9", Test: "TestSyncContracts",
	},
	22: {
		Routes: []string{
			"/corporations/{corporation_id}/divisions",
			"/corporations/{corporation_id}/facilities",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/divisions"},
		Phase:     "8", Test: "TestSyncCorporationDivisions",
	},
	23: {
		Routes:      []string{"/corporations/{corporation_id}/industry/jobs"},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/industry"},
		Phase:       "8", Test: "TestSyncIndustryJobs",
	},
	24: {
		Routes: []string{
			"/corporations/{corporation_id}/members",
			"/corporations/{corporation_id}/membertracking",
			"/corporations/{corporation_id}/members/limit",
			"/corporations/{corporation_id}/members/titles",
		},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/members"},
		Phase:       "8", Test: "TestSyncCorporationMembers",
	},
	25: {
		Routes: []string{
			"/corporations/{corporation_id}/projects",
			"/corporations/{corporation_id}/projects/{project_id}",
			"/corporations/{corporation_id}/projects/{project_id}/contributors",
			"/corporations/{corporation_id}/projects/{project_id}/contribution/{character_id}",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/projects"},
		Phase:     "9", Test: "TestSyncCorporationProjects",
	},
	26: {
		Routes: []string{
			"/corporations/{corporation_id}/roles",
			"/corporations/{corporation_id}/roles/history",
			"/corporations/{corporation_id}/titles",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/roles"},
		Phase:     "8", Test: "TestSyncCorporationRoles",
	},
	27: {
		Routes: []string{
			"/corporations/{corporation_id}/starbases",
			"/corporations/{corporation_id}/starbases/{starbase_id}",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/starbases"},
		Phase:     "8.1", Test: "TestSyncCorporationStarbases",
	},
	28: {
		Routes: []string{
			"/corporations/{corporation_id}/structures",
			"/corporations/{corporation_id}/structures/skyhooks",
			"/corporations/{corporation_id}/structures/skyhooks/{skyhook_id}",
			"/corporations/{corporation_id}/structures/sovereignty-hubs",
			"/corporations/{corporation_id}/structures/sovereignty-hubs/{sovereignty_hub_id}",
		},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/structures"},
		Phase:       "8.1", Test: "TestSyncCorporationStructures",
	},
	29: {
		Routes: []string{
			"/corporations/{corporation_id}/wallets",
			"/corporations/{corporation_id}/wallets/{division}/journal",
			"/corporations/{corporation_id}/wallets/{division}/transactions",
		},
		Controllers: []string{"CorporationController"},
		Endpoints: []string{
			"/api/v1/corporations/{id}/wallets/{division}/journal",
			"/api/v1/corporations/{id}/ledger/bounties",
			"/api/v1/corporations/{id}/ledger/pi",
		},
		Phase: "15.1", Test: "TestKeysetPagesAreTotallyOrdered",
	},
	30: {
		Routes: []string{
			"/corporation/{corporation_id}/mining/extractions",
			"/corporation/{corporation_id}/mining/observers",
			"/corporation/{corporation_id}/mining/observers/{observer_id}",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/mining"},
		Phase:     "8", Test: "TestSyncMiningObservers",
	},
	31: {
		Routes: []string{
			"/corporations/{corporation_id}/customs_offices",
			"/corporations/{corporation_id}/containers/logs",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/customs-offices"},
		Phase:     "8", Test: "TestSyncCorporationCustomsOffices",
	},
	32: {
		Routes: []string{
			"/corporations/{corporation_id}/medals",
			"/corporations/{corporation_id}/medals/issued",
		},
		Endpoints: []string{"/api/v1/corporations/{id}/medals"},
		Phase:     "8", Test: "TestSyncCorporationMedals",
	},
	33: {
		Routes: []string{
			"/corporations/{corporation_id}/shareholders",
			"/corporations/{corporation_id}/standings",
			"/corporations/{corporation_id}/alliancehistory",
			"/corporations/{corporation_id}",
		},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}"},
		Phase:       "8", Test: "TestSyncCorporationSheet",
	},

	// ── Market (34–36) ───────────────────────────────────────────────────
	34: {
		Routes: []string{
			"/characters/{character_id}/orders",
			"/characters/{character_id}/orders/history",
		},
		Controllers: []string{"CharacterController"},
		Endpoints:   []string{"/api/v1/characters/{id}/orders"},
		Phase:       "9", Test: "TestSyncMarketOrders",
	},
	35: {
		Routes: []string{
			"/corporations/{corporation_id}/orders",
			"/corporations/{corporation_id}/orders/history",
		},
		Controllers: []string{"CorporationController"},
		Endpoints:   []string{"/api/v1/corporations/{id}/orders"},
		Phase:       "9", Test: "TestSyncMarketOrders",
	},
	36: {
		Routes: []string{
			"/markets/prices",
			"/markets/{region_id}/history",
			"/markets/{region_id}/orders",
			"/markets/{region_id}/types",
		},
		Endpoints: []string{"/api/v1/markets/prices"},
		Phase:     "9", Test: "TestSyncMarketPrices",
	},

	// ── Alliance & Sovereignty (37–39) ───────────────────────────────────
	37: {
		Routes: []string{
			"/alliances",
			"/alliances/{alliance_id}",
			"/alliances/{alliance_id}/contacts",
			"/alliances/{alliance_id}/contacts/labels",
			"/alliances/{alliance_id}/corporations",
		},
		Controllers: []string{"AllianceController"},
		Endpoints:   []string{"/api/v1/alliances/{id}"},
		// STILL UNREACHABLE after 20.7. Handlers exist (handlers/alliance.go)
		// and no route is dispatched: the sheet and member-corporation routes
		// need a global fan-out over app.alliance, and CONTACTS additionally
		// needs an alliance-scoped acting-character elector that has no
		// worker at all. See internal/sync/worker/unmapped.go.
		Phase: "20.8", Test: "TestSyncAlliance",
	},
	38: {
		Routes: []string{
			"/sovereignty/campaigns",
			"/sovereignty/systems",
		},
		Endpoints: []string{"/api/v1/sovereignty/campaigns"},
		Phase:     "9", Test: "TestSyncSovereignty",
	},
	39: {
		Routes: []string{
			"/characters/{character_id}/killmails/recent",
			"/corporations/{corporation_id}/killmails/recent",
			"/killmails/{killmail_id}/{killmail_hash}",
		},
		Controllers: []string{"KillmailsController"},
		Endpoints: []string{
			"/api/v1/characters/{id}/killmails",
			"/api/v1/corporations/{id}/killmails",
		},
		// PHASE 20.7 (B48): two-stage fan-out (recent list -> per-killmail
		// detail) written and dispatched. Verified on the live installation
		// polling 200; landed 0 rows because the cached upstream body is `[]`
		// — CEODude has no kills in ESI's recent window. The /api/v2 shim's
		// three killmail routes remain UNSERVABLE, blocked on legacy's
		// `attacker_hash` surrogate (internal/api/v2shim/classification.go).
		Phase: "9", Test: "TestSyncKillmails",
	},

	// ── Utilities (40–44) ────────────────────────────────────────────────
	40: {
		Routes:    []string{"/characters/{character_id}/search"},
		Endpoints: []string{"/api/v1/support/search"},
		Phase:     "15", Test: "TestSearchNeverProxiesESI",
	},
	41: {
		Routes: []string{
			"/universe/structures/{structure_id}",
			"/universe/stations/{station_id}",
		},
		Endpoints: []string{
			"/api/v1/support/resolve",
			"/api/v1/support/universe/structures",
			"/api/v1/support/universe/stations",
		},
		// STILL UNREACHABLE after 20.7. handlers/location.go exists and
		// neither route is dispatched: both need a fan-out over the location
		// ids HANGAR has seen in its own synced rows, and that enumeration
		// query has not been written.
		Phase: "20.8", Test: "TestSyncLocationResolution",
	},
	42: {
		Routes:    []string{"/insurance/prices"},
		Endpoints: []string{"/api/v1/tools/insurance"},
		// PHASE 20.7 (B48): global dispatch entry; verified on the live
		// installation landing 3,414 rows.
		Phase: "15", Test: "TestSyncInsurancePrices",
	},
	43: {
		Endpoints: []string{"/api/v1/tools/character/{id}/notes"},
		Phase:     "15", Test: "TestCharacterNotes",
	},
	44: {
		Endpoints: []string{"/api/v1/tools/moon-report"},
		Phase:     "15", Test: "TestMoonReportParser",
	},

	// ── Status (45–46) ───────────────────────────────────────────────────
	45: {
		Routes:    []string{"/meta/status"},
		Endpoints: []string{"/api/v1/meta/esi-status"},
		// PHASE 20.7 (B48): global dispatch entry, and the endpoint's
		// hard-coded `"healthy": true` replaced by a value derived from CCP's
		// own per-route statuses. Verified live: 229 routes, 0 down, 0
		// degraded.
		Phase: "15.1", Test: "TestSyncEsiStatus",
	},
	46: {
		Routes:    []string{"/status"},
		Endpoints: []string{"/api/v1/meta/server-status"},
		Phase:     "15.1", Test: "TestSyncServerStatus",
	},

	// ── Admin & Auth (47–52), Squads (53–55), Alerts (56–58) ─────────────
	// HANGAR-native: no upstream ESI route delivers any of these.
	47: {Endpoints: []string{"/api/v1/api-tokens"}, Controllers: []string{"UserController"}, Phase: "18", Test: "TestApiTokenScopeCap"},
	48: {Endpoints: []string{"/api/v1/admin/scopes"}, Phase: "18", Test: "TestScopeAdministration"},
	49: {Endpoints: []string{"/api/v1/admin/esi/catalogue/pin"}, Phase: "17", Test: "TestCompatibilityPinAdvance"},
	50: {Endpoints: []string{"/api/v1/admin/users"}, Controllers: []string{"UserController", "RoleLookupController"}, Phase: "18", Test: "TestUserAdministration"},
	51: {Endpoints: []string{"/api/v1/admin/security-log"}, Phase: "18", Test: "TestAuditLog"},
	52: {Endpoints: []string{"/api/v1/admin/sync", "/api/v1/admin/sync/routes"}, Controllers: []string{"RoleController"}, Phase: "20.1", Test: "TestAdminSyncBoard"},
	53: {Endpoints: []string{"/api/v1/squads"}, Controllers: []string{"SquadController"}, Phase: "16", Test: "TestSquadCRUD"},
	54: {Endpoints: []string{"/api/v1/squads/{id}/members"}, Controllers: []string{"SquadController"}, Phase: "16", Test: "TestSquadMembers"},
	55: {Endpoints: []string{"/api/v1/squads/{id}/roles"}, Controllers: []string{"SquadController"}, Phase: "16", Test: "TestSquadRoles"},
	56: {Endpoints: []string{"/api/v1/me/webhooks", "/api/v1/admin/platforms"}, Phase: "20.5", Test: "TestWebhookRotationOverlap"},
	57: {
		// ── DEFECT B54 (PHASE 20.8): THIS DID NOT ADD UP ─────────────────
		// It read "54 seeded (42 esi_notification, 9 domain_event, 4
		// threshold), 8 domains" — and 42 + 9 + 4 is 55. A hand-written
		// breakdown of a hand-written total, in the third column of this
		// matrix found wrong in one phase, and the arithmetic alone was
		// enough to convict it.
		//
		// MEASURED two ways and they agree: catalogue.Catalogue holds 41
		// CategoryESINotification, 9 CategoryDomainEvent and 4
		// CategoryThreshold; and a FIRST BOOT of the release image against a
		// fresh Postgres 18 seeds exactly those three counts into
		// app.alert_type. TestAlertCatalogueComplete now asserts the
		// per-category split partitions the catalogue, so the next edit that
		// does not add up fails rather than being read past.
		AlertTypes: []string{"54 seeded (41 esi_notification, 9 domain_event, 4 threshold), 8 domains"},
		Endpoints:  []string{"/api/v1/admin/alerts"},
		Phase:      "20.4", Test: "TestAlertCatalogueComplete",
	},
	// #58 Localisation has NO HANGAR ENDPOINT, and the empty list is the
	// finding rather than an omission (defect B52). The matrix claimed
	// /api/v1/meta/locales, which has never been registered in any spelling:
	// HANGAR's locale is an installation-wide boot setting (HANGAR_LOCALE,
	// rejected at boot by internal/config if unsupported, resolved to an ESI
	// Accept-Language by internal/i18n), not a resource a client asks for.
	// Naming a fictional endpoint made the row look delivered by machinery
	// nobody could call; naming none says what is true.
	58: {Phase: "3", Test: "TestLocaleResolutionExhaustive"},
}

func capabilitySpec(id int) CapabilitySpec { return capabilitySpecs[id] }

// checkRouteMapTotality is what keeps capabilitySpecs from being a document
// that agrees with itself.
//
// Two directions, both of which have caught real defects in this codebase's
// history if you imagine the check having existed:
//
//  1. EVERY route the sync engine can reach, and every route recorded as
//     backing an unbuilt capability, must be claimed by some capability.
//     Without this, a capability row could be made to look verified by
//     quietly leaving its broken route out of the map — which is precisely
//     the shape of B47 (a route nobody had written down) and of B30 (routes
//     excluded from the subscribable set "for a reason").
//
//  2. Every route NAMED here must exist as a catalogued GET route, either
//     subscribable or classified. A name that matches nothing is a typo or a
//     path CCP renamed — defect B38 was exactly that, twice, and it survived
//     three phases because nothing compared the strings to the catalogue.
func checkRouteMapTotality(subscribable map[string]sync.EntityKind, unmapped map[string]worker.UnmappedReason) error {
	claimed := map[string]bool{}
	var unknown []string
	for id, spec := range capabilitySpecs {
		for _, route := range spec.Routes {
			claimed[route] = true
			_, isSubscribable := subscribable[route]
			_, isClassified := unmapped[route]
			if !isSubscribable && !isClassified {
				unknown = append(unknown, fmt.Sprintf("capability %d names %q, which is not a catalogued GET route", id, route))
			}
		}
	}

	var unclaimed []string
	for route := range subscribable {
		if !claimed[route] {
			unclaimed = append(unclaimed, route)
		}
	}
	for route, reason := range unmapped {
		if reason == worker.ReasonNotBuilt && !claimed[route] {
			unclaimed = append(unclaimed, route+" (not-built)")
		}
	}

	sort.Strings(unknown)
	sort.Strings(unclaimed)
	var problems []string
	if len(unclaimed) > 0 {
		problems = append(problems, fmt.Sprintf(
			"%d route(s) the sync engine reaches, or records as an unbuilt capability, are claimed by NO capability:\n  %s",
			len(unclaimed), strings.Join(unclaimed, "\n  ")))
	}
	if len(unknown) > 0 {
		problems = append(problems, fmt.Sprintf(
			"%d capability route(s) do not exist in the catalogue:\n  %s",
			len(unknown), strings.Join(unknown, "\n  ")))
	}
	if len(problems) > 0 {
		return fmt.Errorf("capability route map is not total:\n\n%s", strings.Join(problems, "\n\n"))
	}
	return nil
}
