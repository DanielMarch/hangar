// Package catalogue is the alert-type catalogue: the closed, build-time
// set of alert types HANGAR seeds into app.alert_type, their eight
// domains, and the threshold alerts' source-route declarations
// (00_SRS_v3.1.md §4.4, 03_IMPLEMENTATION_ROADMAP.md Phase 14).
//
// This package is deliberately dependency-free — no database, no ESI
// client, no store types. The catalogue is data plus the rules that
// validate it, so `make check-alert-sources` can run the threshold check
// with nothing but a Go toolchain (that is what "build-time error" means
// in §4.4: a failing `go test`, not a failing migration).
package catalogue

// Domain is one of §4.4's eight alert domains. It is a plain string type
// rather than a Go enum over an external vocabulary (Principle 14 governs
// vocabularies CCP owns; the DOMAIN set is HANGAR's own taxonomy and is
// legitimately closed) — app.alert_type.domain has no CHECK constraint, so
// a runtime-discovered CCP type can still land outside this set, see
// DomainUnknown below.
type Domain string

const (
	DomainStructures   Domain = "structures"
	DomainCharacters   Domain = "characters"
	DomainPlatform     Domain = "platform"
	DomainWars         Domain = "wars"
	DomainCorporations Domain = "corporations"
	DomainSovereignty  Domain = "sovereignty"
	DomainContracts    Domain = "contracts"
	DomainAlliances    Domain = "alliances"

	// DomainUnknown is NOT one of the eight and is never seeded. It is the
	// domain stamped on a CCP notification type discovered at runtime that
	// this catalogue does not know (internal/alerting.EnsureAlertType) —
	// Principle 14 says such a type is ingested, never rejected, and it
	// needs an app.alert_type row to satisfy app.alert_event's foreign key.
	// Excluded from every count assertion below by construction: the
	// counts iterate Domains, which does not contain it.
	DomainUnknown Domain = "unknown"
)

// Domains is §4.4's eight, in the order the spec lists them.
var Domains = []Domain{
	DomainStructures, DomainCharacters, DomainPlatform, DomainWars,
	DomainCorporations, DomainSovereignty, DomainContracts, DomainAlliances,
}

// ExpectedCounts is the per-domain count table, asserted at build time by
// TestAlertCatalogueSeeds54AcrossEightDomains.
//
// It is §4.4's table with ONE correction, made in Phase 14.1 against a
// direct measurement of the upstream: Structures is 23, not the 22 §4.4
// states. See DocumentedTotal below, and
// testdata/upstream/eveseat_notifications_alerts.txt for the measurement
// itself. The other seven counts are §4.4's, unchanged and confirmed.
var ExpectedCounts = map[Domain]int{
	DomainStructures:   23,
	DomainCharacters:   7,
	DomainPlatform:     7,
	DomainWars:         6,
	DomainCorporations: 5,
	DomainSovereignty:  4,
	DomainContracts:    1,
	DomainAlliances:    1,
}

// ExpectedSkyhookCount is the "including 5 Skyhook types" half of the
// Structures row — asserted separately, since a Structures count of 22
// that contained four or six Skyhook types would satisfy the domain total
// while missing the thing §4.4 actually calls out.
const ExpectedSkyhookCount = 5

// DocumentedTotal is the total 00_SRS_v3.1.md §4.4, the roadmap's Phase 14
// design note, SRS §17's invariant table and migration 00008's own header
// all state: 54 concrete alert types. SeededTotal() equals it.
//
// ── RESOLVED SPECIFICATION DEFECT (raised in Phase 14, fixed in 14.1) ────
// Phase 14 reported that §4.4's eight per-domain counts summed to 53 while
// the same sentence stated 54, and that it could not tell which side was
// wrong because eveseat/notifications was not reachable from that build
// environment. It shipped 53 — per-domain counts exact, defect documented,
// no type invented into a guessed domain.
//
// Phase 14.1 obtained access to the upstream and measured it, applying
// docs/BASELINE.md §4's own recorded pipeline to the same pinned commit
// (844f7de7746b8c5161a0ad61cc7690af61eaf092). The measurement reproduces
// BASELINE's total of 54 exactly and yields the per-domain breakdown
// BASELINE never recorded:
//
//	23 Structures   7 Characters   7 Seat   6 Wars
//	 5 Corporations 4 Sovereignties 1 Contracts 1 Alliances    = 54
//
// So the TOTAL was right and the BREAKDOWN was wrong, exactly as Phase 14
// deduced: §4.4's "Structures (22, including 5 Skyhook types)" understates
// Structures by one. It is 23. The other seven counts are confirmed
// correct, and Skyhook is confirmed to be 5.
//
// A second, independent artefact agrees on the total: upstream's
// src/Config/notifications.alerts.php holds 55 alert keys of which one
// ('test_integration') is marked not visible — 54 again, from a different
// source. It distributes differently (Structures 25, because two keys reuse
// another class's file); the file-based reading is adopted because it is
// BASELINE's recorded method and it agrees with §4.4 in seven domains
// rather than five.
//
// The full measurement, both cross-checks, and an upstream dead-code
// observation are committed in
// testdata/upstream/eveseat_notifications_alerts.txt, which
// TestCatalogueMatchesMeasuredUpstream reads — so this is reproducible
// evidence, not a claim in a comment.
const DocumentedTotal = 54

// SeededTotal is the number of alert types this catalogue actually seeds:
// the sum of ExpectedCounts. See DocumentedTotal for why it is 53.
func SeededTotal() int {
	total := 0
	for _, d := range Domains {
		total += ExpectedCounts[d]
	}
	return total
}

// Category is app.alert_type.category: how an alert type comes into
// being. The three values are migration 00008's CHECK-free but documented
// vocabulary ('esi_notification' | 'domain_event' | 'threshold').
type Category string

const (
	// CategoryESINotification is a CCP notification type, delivered when
	// one arrives in /characters/{character_id}/notifications.
	CategoryESINotification Category = "esi_notification"
	// CategoryDomainEvent is a HANGAR-internal event (the hangar.* rows) —
	// something HANGAR itself observed, not something CCP told us.
	CategoryDomainEvent Category = "domain_event"
	// CategoryThreshold is evaluated by HANGAR against synced detail data
	// (fuel low, contract expiring, extraction due). §4.4: every threshold
	// alert must declare its source route, and a threshold alert whose
	// source route is not in the sync set is a build-time error.
	CategoryThreshold Category = "threshold"
)
