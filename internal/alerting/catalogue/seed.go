package catalogue

import (
	"sort"
	"strings"
)

// AlertType is one row of app.alert_type, plus the build-time-only fields
// the database column set has no place for (SourceRoute's upstream_path,
// which the seed SQL resolves into a route_id, and Summary, which is the
// human-readable subject line render/template.go falls back to).
type AlertType struct {
	// Name is app.alert_type.alert_type — for a CCP notification, the
	// notification `type` value VERBATIM (see the sourcing note below);
	// for everything else, a dotted hangar-side identifier.
	Name string
	// Domain is one of the eight (never DomainUnknown — that value exists
	// only for runtime-discovered types, which are not seeded).
	Domain Domain
	// Category is how the alert comes into being.
	Category Category
	// SourceRoute is app.esi_route.upstream_path, verbatim, for a
	// threshold alert; empty for every other category. Populated ONLY for
	// CategoryThreshold — §4.4's "each threshold alert must declare its
	// source route".
	SourceRoute string
	// SourceMethod is the HTTP method of SourceRoute ("GET" throughout —
	// carried explicitly because app.esi_route's natural key is
	// (method, upstream_path), and the seed SQL joins on both).
	SourceMethod string
	// DefaultEnabled is app.alert_type.default_enabled.
	DefaultEnabled bool
	// Summary is a short human-readable label used as the default subject
	// when no per-type template exists (internal/alerting/render).
	Summary string
}

// ─────────────────────────────────────────────────────────────────────────
// SOURCING NOTE — read before changing any Name below.
//
// Two independent things are being asserted by each CCP-notification row,
// and they have very different confidence levels:
//
//  1. THE TYPE NAME ITSELF is verified. Every CategoryESINotification Name
//     below appears verbatim in the `type` enum of the live ingested spec
//     (internal/esi/catalogue/embedded/openapi.snapshot.json, schema
//     CharactersCharacterIdNotificationsGet.items.properties.type — 254
//     values). A name that is not in that enum can never arrive from ESI,
//     so this is the property worth machine-checking; TestCatalogueTypes
//     ExistInLiveSpecEnum does exactly that against the snapshot.
//
//  2. THE DOMAIN ASSIGNMENT IS NOT VERIFIED against the nominal upstream.
//     The roadmap names eveseat/notifications `src/Notifications/**` as
//     the authoritative source for which of CCP's 254 types are promoted
//     to first-class alerts and which directory (= domain) each lives in.
//     That repository is NOT fetchable in this environment (the same
//     constraint Phase 13 hit with MurmurRPC's actual .proto). The
//     selection and domain placement below are therefore HANGAR's
//     judgement, constrained to hit §4.4's per-domain counts exactly and
//     chosen for operational usefulness to a corporation-management tool.
//     They are NOT a reproduction of eveseat's file tree and must not be
//     presented as one. Reconciling them against the real upstream is a
//     follow-up worth doing the moment that source is reachable.
//
// The counts are the contract; the membership is a defensible reading.
// ─────────────────────────────────────────────────────────────────────────

// Catalogue is the seeded alert-type set. Order within a domain is
// operational (most severe first) rather than alphabetical, so an operator
// reading `hangar admin alerts list` sees the ones that matter at the top.
var Catalogue = []AlertType{
	// ── Structures (22 = 15 CCP + 5 Skyhook + 2 threshold) ──────────────
	// The 5 Skyhook types are §4.4's explicitly named subset; they are
	// grouped together below and counted by TestAlertCatalogueSeeds54
	// AcrossEightDomains's Skyhook assertion.
	{Name: "StructureUnderAttack", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure under attack"},
	{Name: "StructureLostShields", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure lost its shields"},
	{Name: "StructureLostArmor", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure lost its armour"},
	{Name: "StructureDestroyed", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure destroyed"},
	{Name: "StructureFuelAlert", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure fuel running out"},
	{Name: "StructureAnchoring", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure anchoring"},
	{Name: "StructureUnanchoring", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure unanchoring"},
	{Name: "StructureOnline", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure online"},
	{Name: "StructureServicesOffline", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure services offline"},
	{Name: "StructureWentHighPower", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure went high power"},
	{Name: "StructureWentLowPower", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure went low power"},
	{Name: "StructureImpendingAbandonmentAssetsAtRisk", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Assets at risk in an abandoning structure"},
	{Name: "OwnershipTransferred", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure ownership transferred"},
	// TowerAlertMsg/TowerResourceAlertMsg are the starbase (POS) analogues
	// of StructureUnderAttack/StructureFuelAlert — same domain, different
	// upstream structure family.
	{Name: "TowerAlertMsg", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Starbase under attack"},
	{Name: "TowerResourceAlertMsg", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Starbase fuel running out"},

	// Skyhook (5) — §4.4's named subset. Equinox-era structures; all five
	// names are present in the live spec enum.
	{Name: "SkyhookUnderAttack", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook under attack"},
	{Name: "SkyhookLostShields", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook lost its shields"},
	{Name: "SkyhookDestroyed", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook destroyed"},
	{Name: "SkyhookDeployed", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook deployed"},
	{Name: "SkyhookOnline", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook online"},

	// Threshold (2) — the two §4.4 names explicitly: "Structure and
	// starbase fuel alerts depend on /corporations/{id}/structures and
	// /corporations/{id}/starbases/{starbase_id} respectively". The
	// starbase one's alert_type string is fixed by
	// 02_DATABASE_SCHEMA.md §4.x and migration 00010's own comment
	// ("app.alert_type('corporation.starbase.fuel_low').source_route_id"),
	// so it is used verbatim and the structure one mirrors its shape.
	//
	// Note the starbase source is the DETAIL route, not the list route:
	// app.starbase_detail.fuels — the fuel bay itself — is only populated
	// by the detail fan-out (Phase 8.1 wired it for exactly this reason).
	{
		Name: "corporation.structure.fuel_low", Domain: DomainStructures, Category: CategoryThreshold,
		SourceRoute: "/corporations/{corporation_id}/structures", SourceMethod: "GET",
		DefaultEnabled: true, Summary: "Structure fuel below threshold",
	},
	{
		Name: "corporation.starbase.fuel_low", Domain: DomainStructures, Category: CategoryThreshold,
		SourceRoute: "/corporations/{corporation_id}/starbases/{starbase_id}", SourceMethod: "GET",
		DefaultEnabled: true, Summary: "Starbase fuel below threshold",
	},

	// ── Characters (7) ──────────────────────────────────────────────────
	{Name: "CharTerminationMsg", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Character removed from corporation"},
	{Name: "CharLeftCorpMsg", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Character left the corporation"},
	{Name: "CharMedalMsg", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Medal awarded"},
	{Name: "CloneActivationMsg2", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Jump clone activated"},
	{Name: "JumpCloneDeletedMsg1", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Jump clone deleted"},
	{Name: "InsurancePayoutMsg", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Insurance paid out"},
	{Name: "ExpertSystemExpiryImminent", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Expert system about to expire"},

	// ── platform (7) ────────────────────────────────────────────────────
	// HANGAR's own events. These seven already existed in
	// db/seed/alert_types.sql from Phase 1a (that file's header explains
	// why only these seven could be seeded before app.esi_route existed);
	// they are restated here because this Go catalogue — not the SQL — is
	// the build-time source of truth the count assertions read, and
	// TestSeedSQLMatchesGoCatalogue proves the two agree.
	{Name: "hangar.platform.replica_clustered", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Rate-limit ledger switched to clustered mode"},
	{Name: "hangar.platform.replica_solo", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Rate-limit ledger switched to solo mode"},
	{Name: "hangar.platform.esi_pin_advanced", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "ESI compatibility pin advanced"},
	{Name: "hangar.platform.error_budget_420", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "ESI error budget exhausted (420)"},
	{Name: "hangar.platform.sde_import_failed", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "SDE import failed"},
	{Name: "hangar.provisioning.revocation_exposed", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Access revocation still exposed on a platform"},
	{Name: "hangar.provisioning.driver_unreachable", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Provisioning driver unreachable"},

	// ── Wars (6) ────────────────────────────────────────────────────────
	// §4.4 [v3.1 — B10]: "Wars are notification-derived." All six are CCP
	// notification types; §6 exposes no wars endpoint and §5.2 defines no
	// wars table, and this phase invents neither.
	//
	// "WarAdopted" carries a VERIFIED CCP SPEC QUIRK: the live enum value
	// is "WarAdopted " — with a trailing space. See Normalize below; the
	// catalogue stores the trimmed form and the interpreter trims before
	// lookup, so a payload carrying CCP's literal spelling still matches.
	{Name: "WarDeclared", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "War declared"},
	{Name: "WarInvalid", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "War invalidated"},
	{Name: "WarRetractedByConcord", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "War retracted by CONCORD"},
	{Name: "WarAdopted", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "War adopted"},
	{Name: "WarInherited", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "War inherited"},
	{Name: "AllyJoinedWarDefenderMsg", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Ally joined the war on the defending side"},

	// ── Corporations (5 = 4 CCP + 1 threshold) ──────────────────────────
	{Name: "CorpAppNewMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "New corporation application"},
	{Name: "CharAppAcceptMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Corporation application accepted"},
	{Name: "CharAppWithdrawMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Corporation application withdrawn"},
	{Name: "CorpNewCEOMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Corporation has a new CEO"},
	// §4.4's third named threshold example ("extraction due"). The source
	// route is the SINGULAR /corporation/... form, verbatim from the live
	// spec — Principle 5; see internal/sync/worker/corporation.go's
	// pagePaginatedRoutes for the same spelling.
	{
		Name: "corporation.moon_extraction.due", Domain: DomainCorporations, Category: CategoryThreshold,
		SourceRoute: "/corporation/{corporation_id}/mining/extractions", SourceMethod: "GET",
		DefaultEnabled: true, Summary: "Moon extraction chunk arriving",
	},

	// ── Sovereignty (4) ─────────────────────────────────────────────────
	{Name: "SovStructureReinforced", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Sovereignty structure reinforced"},
	{Name: "SovStructureDestroyed", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Sovereignty structure destroyed"},
	{Name: "EntosisCaptureStarted", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Entosis capture started"},
	{Name: "SovCommandNodeEventStarted", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Sovereignty command node event started"},

	// ── Contracts (1) ───────────────────────────────────────────────────
	// §4.4's second named threshold example ("expiring contracts"). The
	// corporation contract list is the source rather than the character
	// one because a contract lapsing unnoticed is a corporation-level
	// operational failure; both routes are in the sync set, so either
	// would satisfy the build-time check.
	{
		Name: "corporation.contract.expiring", Domain: DomainContracts, Category: CategoryThreshold,
		SourceRoute: "/corporations/{corporation_id}/contracts", SourceMethod: "GET",
		DefaultEnabled: true, Summary: "Contract about to expire",
	},

	// ── Alliances (1) ───────────────────────────────────────────────────
	{Name: "AllianceCapitalChanged", Domain: DomainAlliances, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Alliance capital system changed"},
}

// Normalize maps a raw CCP notification `type` string onto the catalogue's
// canonical name.
//
// VERIFIED CCP SPEC QUIRK, reported rather than silently absorbed: the
// live spec's notification-type enum contains the value "WarAdopted " with
// a TRAILING SPACE (internal/esi/catalogue/embedded/openapi.snapshot.json;
// it is the only enum member with leading or trailing whitespace, and
// TestLiveSpecEnumWhitespaceQuirk pins that fact so a future spec ingest
// that fixes it upstream is noticed rather than silently absorbed). CCP
// almost certainly ships the same trailing space in the notification
// payload's `type` field, since the enum is generated from the same
// source. A catalogue keyed on the trimmed name would therefore never
// match a real WarAdopted notification, and it would land on the
// unknown-types board forever.
//
// Trimming here (rather than storing the space-bearing name) keeps the
// quirk out of the database, out of routing rules an operator has to type,
// and out of the seed file — while still matching whatever CCP sends. It
// is intentionally symmetric: a trimmed payload matches too, so the fix
// survives CCP correcting the spec.
func Normalize(ccpType string) string { return strings.TrimSpace(ccpType) }

// byName indexes Catalogue for O(1) lookup. Built once at init; the
// catalogue is immutable after package initialisation.
var byName = func() map[string]AlertType {
	m := make(map[string]AlertType, len(Catalogue))
	for _, t := range Catalogue {
		m[t.Name] = t
	}
	return m
}()

// ByName resolves a raw CCP notification type (or a hangar.* name) to its
// catalogue entry, applying Normalize first. ok is false for a type this
// catalogue does not know — which is a normal, expected outcome (Principle
// 14), not an error: internal/alerting.Interpret routes it to the
// unknown-types board and the generic renderer.
func ByName(name string) (AlertType, bool) {
	t, ok := byName[Normalize(name)]
	return t, ok
}

// Names returns every seeded alert type name, sorted — a stable ordering
// for the seed-file comparison test and for admin listings.
func Names() []string {
	out := make([]string, 0, len(Catalogue))
	for _, t := range Catalogue {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// CountByDomain tallies Catalogue by domain.
func CountByDomain() map[Domain]int {
	counts := make(map[Domain]int, len(Domains))
	for _, t := range Catalogue {
		counts[t.Domain]++
	}
	return counts
}

// SkyhookNames is the "including 5 Skyhook types" subset, identified by
// the upstream naming convention CCP itself uses (every skyhook
// notification type starts with "Skyhook"). Deriving it from the prefix
// rather than keeping a second hand-maintained list means adding a sixth
// Skyhook type cannot silently pass the Skyhook assertion.
func SkyhookNames() []string {
	var out []string
	for _, t := range Catalogue {
		if strings.HasPrefix(t.Name, "Skyhook") {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}
