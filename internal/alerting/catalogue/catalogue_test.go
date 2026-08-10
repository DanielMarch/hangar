package catalogue_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	esicatalogue "github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/sync/worker"
	"github.com/stretchr/testify/require"
)

// TestAlertCatalogueSeeds54AcrossEightDomains is Phase 14's first named
// exit criterion: "exact per-domain counts including Wars 6 and Skyhooks
// 5".
//
// ── THE COUNTS THIS ENFORCES, AND WHERE THEY COME FROM ──────────────────
// Seven of the eight are 00_SRS_v3.1.md §4.4's own, verbatim. The eighth,
// Structures, is 23 rather than the 22 §4.4 states — a specification
// defect Phase 14 REPORTED (its eight numbers summed to 53 against a
// stated total of 54) and Phase 14.1 RESOLVED by measuring the upstream
// directly: BASELINE.md §4's own pipeline, applied to the pinned commit,
// reproduces the total of 54 and puts 23 in Structures.
//
// So the total the test asserts is now 54 — DocumentedTotal — and it
// agrees with SeededTotal() rather than contradicting it. See
// catalogue.DocumentedTotal for the full account and
// testdata/upstream/eveseat_notifications_alerts.txt for the measurement.
func TestAlertCatalogueSeeds54AcrossEightDomains(t *testing.T) {
	counts := catalogue.CountByDomain()

	for _, domain := range catalogue.Domains {
		want := catalogue.ExpectedCounts[domain]
		require.Equal(t, want, counts[domain],
			"domain %q must seed exactly %d alert types (00_SRS_v3.1.md §4.4)", domain, want)
	}

	// No type may land outside the eight domains. DomainUnknown exists for
	// runtime-discovered CCP types only and must never be seeded.
	known := make(map[catalogue.Domain]bool, len(catalogue.Domains))
	for _, d := range catalogue.Domains {
		known[d] = true
	}
	for _, entry := range catalogue.Catalogue {
		require.True(t, known[entry.Domain],
			"alert type %q has domain %q, which is not one of §4.4's eight", entry.Name, entry.Domain)
	}

	require.Len(t, catalogue.Catalogue, catalogue.SeededTotal(),
		"the catalogue must seed exactly the sum of the per-domain counts")
	require.Equal(t, catalogue.DocumentedTotal, catalogue.SeededTotal(),
		"the per-domain counts must sum to the documented total of 54 — Phase 14.1 resolved the "+
			"53-vs-54 defect by measuring the upstream; see catalogue.DocumentedTotal")
	require.Equal(t, 54, catalogue.SeededTotal())

	// §4.4 names the Skyhook subset explicitly; a Structures count of 22
	// with four or six Skyhook types would pass the domain total while
	// missing what the spec actually calls out.
	require.Len(t, catalogue.SkyhookNames(), catalogue.ExpectedSkyhookCount,
		"§4.4: Structures includes exactly 5 Skyhook types")

	// Wars are notification-derived (§4.4 [v3.1 — B10]) — every war alert
	// must be a CCP notification type, never a threshold or a HANGAR
	// domain event, because there is no wars endpoint and no wars table to
	// evaluate one against.
	for _, entry := range catalogue.Catalogue {
		if entry.Domain != catalogue.DomainWars {
			continue
		}
		require.Equal(t, catalogue.CategoryESINotification, entry.Category,
			"war alert %q must be notification-derived (§4.4 B10: §6 exposes no wars endpoint and §5.2 no wars table)", entry.Name)
	}

	// Names must be unique — a duplicate would silently shrink the seeded
	// set while leaving len(Catalogue) intact.
	seen := make(map[string]bool, len(catalogue.Catalogue))
	for _, entry := range catalogue.Catalogue {
		require.False(t, seen[entry.Name], "duplicate alert type %q", entry.Name)
		seen[entry.Name] = true
	}
}

// TestThresholdAlertSourceRoutesScheduled is Phase 14's second named exit
// criterion and the check `make check-alert-sources` runs: BUILD-TIME
// proof that every threshold alert's source route is in the sync set.
//
// It needs no database and no network — the sync set is derived from the
// three workers' dispatch tables, which are compile-time data.
func TestThresholdAlertSourceRoutesScheduled(t *testing.T) {
	syncSet := worker.SyncSet()
	require.NotEmpty(t, syncSet, "the sync set must not be empty — worker dispatch tables missing?")

	require.NoError(t, catalogue.ValidateThresholds(syncSet))

	// The two routes §4.4 names explicitly, asserted by name so a rename
	// in either direction is caught here rather than by a silently
	// never-firing alert. Note the starbase source is the DETAIL route:
	// app.starbase_detail.fuels is only populated by the detail fan-out
	// (Phase 8.1), so the list route would be the wrong declaration even
	// though it is also in the sync set.
	sources := map[string]string{}
	for _, th := range catalogue.Thresholds() {
		sources[th.Name] = th.SourceRoute
	}
	require.Equal(t, "/corporations/{corporation_id}/structures", sources["corporation.structure.fuel_low"])
	require.Equal(t, "/corporations/{corporation_id}/starbases/{starbase_id}", sources["corporation.starbase.fuel_low"])

	// And the negative case: an invented route must fail the check, so a
	// green run means the check can actually fail.
	require.Error(t, catalogue.ValidateThresholds(map[string]bool{"/not/a/real/route": true}),
		"validation must reject a threshold whose source route is absent from the sync set")
}

// TestCatalogueTypesExistInLiveSpecEnum verifies the half of the
// catalogue that CAN be verified offline: every CCP notification type name
// seeded must appear in the live ingested spec's own `type` enum. A name
// absent from that enum can never arrive from ESI, so the alert would be
// dead on arrival.
//
// (The other half — which types eveseat/notifications promotes, and into
// which domain — is NOT verifiable here; see seed.go's sourcing note.)
func TestCatalogueTypesExistInLiveSpecEnum(t *testing.T) {
	// Compared through catalogue.Normalize on BOTH sides: the catalogue
	// stores trimmed names and CCP's enum carries one value with a
	// trailing space (see TestLiveSpecEnumWhitespaceQuirk), so a raw
	// string comparison would report a false absence for exactly the type
	// whose quirk Normalize exists to absorb.
	enum := make(map[string]bool, len(liveSpecNotificationTypes(t)))
	for name := range liveSpecNotificationTypes(t) {
		enum[catalogue.Normalize(name)] = true
	}

	for _, entry := range catalogue.Catalogue {
		if entry.Category != catalogue.CategoryESINotification {
			continue
		}
		require.True(t, enum[entry.Name],
			"alert type %q is seeded as a CCP notification but is not in the live spec's notification type enum", entry.Name)
	}
}

// TestLiveSpecEnumWhitespaceQuirk pins a VERIFIED CCP spec defect: the
// notification-type enum's "WarAdopted" entry carries a trailing space.
// catalogue.Normalize trims it so a real WarAdopted notification matches
// the catalogue instead of landing on the unknown-types board forever.
//
// The test asserts the quirk still exists AND that Normalize handles it.
// If CCP ever fixes the spec, this test fails — deliberately: that is a
// signal to re-verify the trimming is still harmless (it is, since
// Normalize is symmetric), not a bug in HANGAR.
func TestLiveSpecEnumWhitespaceQuirk(t *testing.T) {
	enum := liveSpecNotificationTypes(t)

	var quirky []string
	for name := range enum {
		if name != strings.TrimSpace(name) {
			quirky = append(quirky, name)
		}
	}
	sort.Strings(quirky)
	require.Equal(t, []string{"WarAdopted "}, quirky,
		"the live spec's notification type enum is expected to contain exactly one whitespace-bearing value")

	// WarAdopted is not one of the upstream's six war types, so it is not
	// in this catalogue (Phase 14 included it on a guess; 14.1's
	// measurement replaced the guessed set with the upstream's actual
	// one). The quirk still matters: Normalize is what stops ANY type
	// being keyed on an invisible character, and an unseeded type reaches
	// the open-vocabulary path, where the name it registers under must be
	// the trimmed one — otherwise the unknown-types board would show two
	// indistinguishable entries and an operator's routing rule could never
	// match the one carrying the space.
	require.Equal(t, "WarAdopted", catalogue.Normalize("WarAdopted "),
		"the trailing space must be trimmed before any lookup or registration")
	require.Equal(t, "WarAdopted", catalogue.Normalize("WarAdopted"),
		"trimming must be idempotent, so the fix survives CCP correcting the spec")

	_, ok := catalogue.ByName("WarAdopted ")
	require.False(t, ok, "WarAdopted is not in the measured upstream's war set, so it is not seeded")

	// Every seeded name must already be in normal form — a catalogue entry
	// whose own name needed trimming could never be matched.
	for _, entry := range catalogue.Catalogue {
		require.Equal(t, entry.Name, catalogue.Normalize(entry.Name),
			"seeded alert type %q must be in normal form", entry.Name)
	}
}

// TestSeedSQLMatchesGoCatalogue keeps db/seed/alert_types.sql and this
// package in lockstep. The Go catalogue is the build-time source of truth
// (it is what the count and threshold assertions read); the SQL is what
// actually populates a live database. A divergence between them would mean
// the assertions above prove nothing about the running system, so the two
// are compared directly rather than trusted to be edited together.
func TestSeedSQLMatchesGoCatalogue(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "seed", "alert_types.sql"))
	require.NoError(t, err)

	// Every seeded name appears in the file as a single-quoted literal in
	// the first column of a VALUES row.
	quoted := regexp.MustCompile(`\(\s*'([^']+)'\s*,`)
	found := make(map[string]bool)
	for _, m := range quoted.FindAllStringSubmatch(string(raw), -1) {
		found[m[1]] = true
	}

	for _, name := range catalogue.Names() {
		require.True(t, found[name],
			"alert type %q is in the Go catalogue but not seeded by db/seed/alert_types.sql", name)
	}

	// And nothing extra: every quoted first-column literal in the seed
	// file must be a catalogue member.
	for name := range found {
		_, ok := catalogue.ByName(name)
		require.True(t, ok,
			"db/seed/alert_types.sql seeds %q, which is not in the Go catalogue", name)
	}

	require.Len(t, found, len(catalogue.Catalogue),
		"the seed file must contain exactly the catalogue's alert types")
}

// liveSpecNotificationTypes reads the notification `type` enum out of the
// embedded spec snapshot — the same bytes internal/esi/catalogue's offline
// boot path uses, so this test never touches the network.
func liveSpecNotificationTypes(t *testing.T) map[string]bool {
	t.Helper()

	specBytes, _, err := esicatalogue.LoadEmbeddedSnapshot()
	require.NoError(t, err)

	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Items struct {
					Properties struct {
						Type struct {
							Enum []string `json:"enum"`
						} `json:"type"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(specBytes, &spec))

	values := spec.Components.Schemas["CharactersCharacterIdNotificationsGet"].Items.Properties.Type.Enum
	require.NotEmpty(t, values, "the embedded snapshot must carry the notification type enum")

	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// TestCatalogueMatchesMeasuredUpstream is Phase 14.1's provenance check:
// the catalogue is tied to a committed MEASUREMENT of eveseat/notifications
// rather than to judgement.
//
// Phase 14 could not do this — the upstream was unreachable from its build
// environment, so every domain assignment shipped flagged as unverified.
// 14.1 measured the tree at the commit docs/BASELINE.md already pins and
// committed the result to testdata/upstream/. This test reads that file and
// asserts three things:
//
//  1. the per-domain counts match the measurement exactly;
//  2. every CCP notification type HANGAR seeds is one the upstream also
//     carries, in the SAME domain;
//  3. HANGAR's divergences from the upstream are EXACTLY the documented
//     substitutions — a new one fails the build instead of passing quietly.
func TestCatalogueMatchesMeasuredUpstream(t *testing.T) {
	upstream := readMeasuredUpstream(t)
	require.Len(t, upstream, catalogue.DocumentedTotal,
		"the committed measurement must carry all %d upstream entries", catalogue.DocumentedTotal)

	// Upstream's directory names map onto HANGAR's domain vocabulary. Only
	// the spellings differ; the sets are the same eight.
	domainOf := map[string]catalogue.Domain{
		"Structures":    catalogue.DomainStructures,
		"Characters":    catalogue.DomainCharacters,
		"Seat":          catalogue.DomainPlatform,
		"Wars":          catalogue.DomainWars,
		"Corporations":  catalogue.DomainCorporations,
		"Sovereignties": catalogue.DomainSovereignty,
		"Contracts":     catalogue.DomainContracts,
		"Alliances":     catalogue.DomainAlliances,
	}

	// (1) per-domain counts.
	upstreamCounts := map[catalogue.Domain]int{}
	upstreamCCP := map[catalogue.Domain]map[string]bool{}
	for _, entry := range upstream {
		domain, ok := domainOf[entry.domain]
		require.True(t, ok, "measurement names an upstream domain %q with no HANGAR equivalent", entry.domain)
		upstreamCounts[domain]++
		if entry.kind == "ccp" {
			if upstreamCCP[domain] == nil {
				upstreamCCP[domain] = map[string]bool{}
			}
			upstreamCCP[domain][entry.name] = true
		}
	}
	for _, domain := range catalogue.Domains {
		require.Equal(t, upstreamCounts[domain], catalogue.ExpectedCounts[domain],
			"domain %q: HANGAR's count must equal the measured upstream's", domain)
	}
	require.Equal(t, 23, upstreamCounts[catalogue.DomainStructures],
		"the measurement is what corrects §4.4's Structures figure from 22 to 23")

	// (2) every seeded CCP type is the upstream's, in the same domain.
	seededCCP := map[catalogue.Domain]map[string]bool{}
	for _, entry := range catalogue.Catalogue {
		if entry.Category != catalogue.CategoryESINotification {
			continue
		}
		require.True(t, upstreamCCP[entry.Domain][entry.Name],
			"alert type %q is seeded in domain %q but the measured upstream has no such CCP entry there",
			entry.Name, entry.Domain)
		if seededCCP[entry.Domain] == nil {
			seededCCP[entry.Domain] = map[string]bool{}
		}
		seededCCP[entry.Domain][entry.Name] = true
	}

	// (3) the divergences are exactly the documented ones. Anything the
	// upstream carries as a CCP type that HANGAR does not seed must appear
	// here with its reason.
	documentedOmissions := map[string]string{
		"StructureFuelAlert":    "superseded by the corporation.structure.fuel_low threshold (§4.4 mandates it and names its source route)",
		"TowerResourceAlertMsg": "superseded by the corporation.starbase.fuel_low threshold (§4.4 mandates it and names its source route)",
	}
	for domain, names := range upstreamCCP {
		for name := range names {
			if seededCCP[domain][name] {
				continue
			}
			reason, documented := documentedOmissions[name]
			require.True(t, documented,
				"upstream CCP type %q (domain %q) is not seeded and not listed as a documented omission — "+
					"either seed it or document why it is displaced", name, domain)
			require.NotEmpty(t, reason)
		}
	}

	// And no documented omission may be stale: if one gets seeded again,
	// the entry here must go.
	for name := range documentedOmissions {
		_, seeded := catalogue.ByName(name)
		require.False(t, seeded, "%q is documented as omitted but is now seeded — remove the omission entry", name)
	}

	// The non-CCP slots are HANGAR's own substitutions. Each must be a
	// threshold or a domain event — never an esi_notification, which would
	// mean HANGAR is claiming a CCP type the upstream does not have.
	for _, entry := range catalogue.Catalogue {
		if entry.Category == catalogue.CategoryESINotification {
			continue
		}
		require.Contains(t,
			[]catalogue.Category{catalogue.CategoryThreshold, catalogue.CategoryDomainEvent},
			entry.Category, "alert type %q", entry.Name)
	}
}

type upstreamEntry struct{ domain, name, kind string }

// readMeasuredUpstream parses testdata/upstream/eveseat_notifications_alerts.txt
// — the committed measurement, comments stripped.
func readMeasuredUpstream(t *testing.T) []upstreamEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "upstream", "eveseat_notifications_alerts.txt"))
	require.NoError(t, err)

	var out []upstreamEntry
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		require.Len(t, fields, 3, "malformed measurement line: %q", line)
		out = append(out, upstreamEntry{domain: fields[0], name: fields[1], kind: fields[2]})
	}
	return out
}
