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

// ExpectedCounts is §4.4's per-domain count table, verbatim:
// "Structures (22, including 5 Skyhook types), Characters (7), HANGAR
// platform events (7), Wars (6), Corporations (5), Sovereignty (4),
// Contracts (1), Alliances (1)". Asserted at build time by
// TestAlertCatalogueSeeds54AcrossEightDomains.
var ExpectedCounts = map[Domain]int{
	DomainStructures:   22,
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

// DocumentedTotal is the total §4.4, the roadmap's Phase 14 design note,
// SRS §17's invariant table and migration 00008's own header all state:
// 54 concrete alert types.
//
// ── REPORTED SPECIFICATION DEFECT (Phase 14) ────────────────────────────
// The eight per-domain counts in ExpectedCounts sum to 53, not 54:
//
//	22 + 7 + 7 + 6 + 5 + 4 + 1 + 1 = 53
//
// The two figures cannot both be right, and the arithmetic is not a
// rounding or an off-by-one in this file — it is the same eight numbers,
// verbatim and identical, in 00_SRS_v3.1.md §4.4, 03_IMPLEMENTATION_
// ROADMAP.md's Phase 14 design note, and Phase 14's own prompt seed. Every
// one of those three states "54" in the same breath as a breakdown summing
// to 53.
//
// WHICH SIDE IS WRONG IS KNOWN — and it is not the total. docs/BASELINE.md
// §4 ("Concrete alert types") records Phase 0 INDEPENDENTLY MEASURING the
// real upstream: eveseat/notifications pinned at commit 844f7de7746b8c516
// 1a0ad61cc7690af61eaf092, deduped by (category, base filename) across the
// Discord/Slack/Mail channel subdirectories, excluding the three top-level
// abstract bases, Structures/Traits/, and the two nested per-channel
// abstracts — result: 54, matching SRS Appendix B. That measurement was
// reproduced against a real clone with a recorded command; the per-domain
// breakdown in §4.4 has no such provenance anywhere in the documentation.
//
// So the defect is in the BREAKDOWN: one of the eight domain counts is
// understated by one. Which one is not determinable here — BASELINE.md
// recorded only the total, and eveseat/notifications is not fetchable from
// this build environment (the same constraint Phase 13 hit with MurmurRPC's
// actual .proto). Anyone with a clone can settle it in one command by
// grouping BASELINE.md §4's own pipeline per category:
//
//	find src/Notifications -name "*.php" \
//	     \( -path "*/Discord/*" -o -path "*/Slack/*" -o -path "*/Mail/*" \) \
//	  | grep -v /Traits/ | sed -E 's#/(Discord|Slack|Mail)/#/#' \
//	  | sed 's#^\./##' | sort -u | grep -v '/Abstract' \
//	  | awk -F/ '{print $3}' | sort | uniq -c
//
// The fix is then a one-line change: correct the short domain's entry in
// ExpectedCounts and add the type it names. Until that measurement exists,
// this catalogue seeds 53 — the per-domain counts hit EXACTLY, and no
// 54th type invented into a domain that may not be the one missing it.
// Guessing which domain is short would trade a known, documented shortfall
// for a silent, wrong assignment, and Principle 13 forbids exactly that
// trade.
//
// SeededTotal() below is therefore 53. TestAlertCatalogueSeeds54Across
// EightDomains asserts the per-domain table exactly and asserts the total
// equals its sum; it does NOT assert 54, and it fails loudly with this
// explanation if anyone "fixes" the count by padding a domain.
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
