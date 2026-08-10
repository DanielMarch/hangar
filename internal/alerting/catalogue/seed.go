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
// PHASE 14.1: this catalogue is now DERIVED FROM A DIRECT MEASUREMENT of
// the upstream, not from judgement. Phase 14 had no access to
// eveseat/notifications and said so, flagging every domain assignment as
// unverified; 14.1 obtained access, measured the tree at the pinned commit
// docs/BASELINE.md already records, and rebuilt this list from it. The
// measurement is committed at
// testdata/upstream/eveseat_notifications_alerts.txt and read back by
// TestCatalogueMatchesMeasuredUpstream.
//
// Two properties are machine-checked, from two different sources:
//
//  1. EVERY CategoryESINotification NAME IS A REAL CCP TYPE — present
//     verbatim in the live ingested spec's own notification `type` enum
//     (internal/esi/catalogue/embedded/openapi.snapshot.json, 254 values).
//     TestCatalogueTypesExistInLiveSpecEnum.
//  2. EVERY CategoryESINotification NAME IS ALSO IN THE UPSTREAM'S OWN
//     SET, in the same domain. TestCatalogueMatchesMeasuredUpstream.
//
// ── WHERE HANGAR DELIBERATELY DIFFERS FROM UPSTREAM ─────────────────────
// The per-domain COUNTS are the upstream's, exactly. The membership is the
// upstream's wherever the upstream entry is a CCP notification type. It
// differs in exactly three places, each a substitution of a HANGAR
// equivalent for an upstream entry that is NOT a CCP notification (SeAT
// computes those from synced data with an observer — the same job
// HANGAR's 'threshold' and 'domain_event' categories do):
//
//   - platform (7): upstream's Seat/ set is SeAT's own platform events
//     (CreatedUser, DisabledToken, EnabledToken, three squad events,
//     TestNotification). HANGAR's seven are HANGAR's own platform events,
//     seeded since Phase 1a and already referenced by operators' routing
//     rules. Same count, same role, different platform.
//   - Corporations (5) and Contracts (1): upstream's `inactive_member` and
//     `contract_created` are observer-computed. HANGAR's equivalents are
//     threshold alerts over the same underlying data, each declaring the
//     synced source route §4.4 requires.
//   - Characters (7): upstream's Killmail and NewMailMessage are
//     observer-computed. HANGAR's equivalents are domain events over its
//     own synced killmail and mail tables. (NewMailMessage is also the
//     upstream dead-code case noted in the measurement file — it has files
//     but no alert key, so nothing upstream can dispatch it.)
//
// And one substitution inside Structures, which is the only place a count
// slot is taken by something other than the upstream's own entry:
//
//   - StructureFuelAlert and TowerResourceAlertMsg (CCP's fixed-schedule
//     fuel warnings for upwell structures and starbases) are replaced by
//     HANGAR's two computed fuel-low thresholds. §4.4 REQUIRES those
//     thresholds and names their source routes
//     (/corporations/{id}/structures and the starbase DETAIL route), and a
//     threshold computed from synced fuel data strictly supersedes CCP's
//     warning: it fires on an operator-chosen margin rather than CCP's
//     fixed one. The two displaced CCP types are not lost — Principle 14's
//     open-vocabulary path (internal/alerting.Emitter.IngestNotification)
//     registers any unseeded type on first sighting and puts it on the
//     unknown-types board, where an operator can route it.
//
// TestCatalogueMatchesMeasuredUpstream asserts these substitutions are
// EXACTLY the ones listed above — a new divergence fails the build rather
// than passing silently.
// ─────────────────────────────────────────────────────────────────────────

// Catalogue is the seeded alert-type set. Order within a domain is
// operational (most severe first) rather than alphabetical, so an operator
// reading `hangar admin alerts list` sees the ones that matter at the top.
var Catalogue = []AlertType{
	// ── Structures (23 = 21 CCP + 2 threshold) ──────────────────────────
	// The upstream's 23, with StructureFuelAlert and TowerResourceAlertMsg
	// replaced by HANGAR's two computed fuel-low thresholds (see the
	// sourcing note above). The 5 Skyhook types §4.4 names explicitly are
	// all present.
	{Name: "StructureUnderAttack", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure under attack"},
	{Name: "StructureLostShields", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure lost its shields"},
	{Name: "StructureLostArmor", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure lost its armour"},
	{Name: "StructureDestroyed", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure destroyed"},
	{Name: "StructureAnchoring", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure anchoring"},
	{Name: "StructureUnanchoring", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure unanchoring"},
	{Name: "StructureServicesOffline", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure services offline"},
	{Name: "StructureWentHighPower", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure went high power"},
	{Name: "StructureWentLowPower", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure went low power"},
	{Name: "OwnershipTransferred", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Structure ownership transferred"},
	{Name: "AllAnchoringMsg", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Alliance structure anchoring"},
	// Starbase (POS) and customs-office family.
	{Name: "TowerAlertMsg", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Starbase under attack"},
	{Name: "OrbitalAttacked", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Customs office under attack"},
	{Name: "OrbitalReinforced", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Customs office reinforced"},
	// Moon mining. Note CCP's own casing: "Moonmining", not "MoonMining"
	// (upstream's PHP class names use the second, its alert KEYS use the
	// first — the keys are what arrives on the wire, so the keys are what
	// this catalogue stores).
	{Name: "MoonminingExtractionStarted", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Moon extraction started"},
	{Name: "MoonminingExtractionFinished", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Moon extraction finished"},
	// Skyhook (5) — §4.4's named subset, confirmed by the measurement.
	{Name: "SkyhookUnderAttack", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook under attack"},
	{Name: "SkyhookLostShields", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook lost its shields"},
	{Name: "SkyhookDestroyed", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook destroyed"},
	{Name: "SkyhookDeployed", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook deployed"},
	{Name: "SkyhookOnline", Domain: DomainStructures, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Skyhook online"},

	// The two fuel thresholds §4.4 mandates, with the two source routes it
	// names. The starbase one uses the DETAIL route because
	// app.starbase_detail.fuels — the actual fuel bay — is only populated
	// by the detail fan-out (Phase 8.1 wired it for exactly this reason);
	// the list route would be the wrong declaration even though it is also
	// in the sync set. corporation.starbase.fuel_low's exact spelling is
	// fixed by 02_DATABASE_SCHEMA.md and migration 00010's own comment.
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

	// ── Characters (7 = 5 CCP + 2 domain event) ─────────────────────────
	{Name: "RaffleCreated", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Raffle created"},
	{Name: "RaffleExpired", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Raffle expired"},
	{Name: "RaffleFinished", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Raffle finished"},
	{Name: "ResearchMissionAvailableMsg", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Research mission available"},
	{Name: "StoryLineMissionAvailableMsg", Domain: DomainCharacters, Category: CategoryESINotification, DefaultEnabled: false, Summary: "Storyline mission available"},
	// HANGAR equivalents of upstream's two observer-computed entries.
	// Domain events rather than thresholds: they fire on a row arriving,
	// not on a value crossing a boundary, so there is no threshold to
	// evaluate and no source route to declare.
	{Name: "character.killmail.received", Domain: DomainCharacters, Category: CategoryDomainEvent, DefaultEnabled: false, Summary: "Killmail recorded"},
	{Name: "character.mail.received", Domain: DomainCharacters, Category: CategoryDomainEvent, DefaultEnabled: false, Summary: "New EVE mail"},

	// ── platform (7) ────────────────────────────────────────────────────
	// HANGAR's own platform events, seeded since Phase 1a. The upstream's
	// Seat/ set is SeAT's equivalent (CreatedUser, DisabledToken,
	// EnabledToken, three squad events, TestNotification) — same count,
	// same role, a different platform's events.
	{Name: "hangar.platform.replica_clustered", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Rate-limit ledger switched to clustered mode"},
	{Name: "hangar.platform.replica_solo", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Rate-limit ledger switched to solo mode"},
	{Name: "hangar.platform.esi_pin_advanced", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "ESI compatibility pin advanced"},
	{Name: "hangar.platform.error_budget_420", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "ESI error budget exhausted (420)"},
	{Name: "hangar.platform.sde_import_failed", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "SDE import failed"},
	{Name: "hangar.provisioning.revocation_exposed", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Access revocation still exposed on a platform"},
	{Name: "hangar.provisioning.driver_unreachable", Domain: DomainPlatform, Category: CategoryDomainEvent, DefaultEnabled: true, Summary: "Provisioning driver unreachable"},

	// ── Wars (6) ────────────────────────────────────────────────────────
	// §4.4 [v3.1 — B10]: "Wars are notification-derived." All six are the
	// upstream's own, all CCP notification types; §6 exposes no wars
	// endpoint and §5.2 defines no wars table, and this phase invents
	// neither. (ESI does expose /wars — that is CCP's API, not HANGAR's
	// §6 surface, and nothing here reads it.)
	{Name: "WarDeclared", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "War declared"},
	{Name: "AllWarDeclaredMsg", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Alliance war declared"},
	{Name: "AllWarInvalidatedMsg", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Alliance war invalidated"},
	{Name: "AllyJoinedWarAggressorMsg", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Ally joined the war on the aggressing side"},
	{Name: "AllyJoinedWarAllyMsg", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Ally joined the war"},
	{Name: "AllyJoinedWarDefenderMsg", Domain: DomainWars, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Ally joined the war on the defending side"},

	// ── Corporations (5 = 4 CCP + 1 threshold) ──────────────────────────
	{Name: "CorpAppNewMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "New corporation application"},
	{Name: "CharLeftCorpMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Character left the corporation"},
	{Name: "CorpAllBillMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Corporation or alliance bill issued"},
	{Name: "BillPaidCorpAllMsg", Domain: DomainCorporations, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Corporation or alliance bill paid"},
	// HANGAR's equivalent of upstream's observer-computed `inactive_member`:
	// a member whose last logon has fallen past the configured window,
	// evaluated from synced member tracking.
	{
		Name: "corporation.member.inactive", Domain: DomainCorporations, Category: CategoryThreshold,
		SourceRoute: "/corporations/{corporation_id}/membertracking", SourceMethod: "GET",
		DefaultEnabled: false, Summary: "Corporation member inactive",
	},

	// ── Sovereignty (4) ─────────────────────────────────────────────────
	{Name: "SovStructureReinforced", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Sovereignty structure reinforced"},
	{Name: "SovStructureDestroyed", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Sovereignty structure destroyed"},
	{Name: "EntosisCaptureStarted", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Entosis capture started"},
	{Name: "SovCommandNodeEventStarted", Domain: DomainSovereignty, Category: CategoryESINotification, DefaultEnabled: true, Summary: "Sovereignty command node event started"},

	// ── Contracts (1) ───────────────────────────────────────────────────
	// HANGAR's equivalent of upstream's observer-computed `contract_created`,
	// and §4.4's "expiring contracts" threshold example. The corporation
	// contract list is the source rather than the character one because a
	// contract lapsing unnoticed is a corporation-level operational
	// failure; both routes are in the sync set.
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
