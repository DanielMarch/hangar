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
// ── WHY THIS TEST ASSERTS 53, NOT 54 ────────────────────────────────────
// The eight per-domain counts this test enforces are 00_SRS_v3.1.md
// §4.4's own, verbatim: Structures 22 (incl. 5 Skyhook), Characters 7,
// platform 7, Wars 6, Corporations 5, Sovereignty 4, Contracts 1,
// Alliances 1. They sum to 53. The same sentence in §4.4 — and
// 03_IMPLEMENTATION_ROADMAP.md's Phase 14 design note, and this phase's
// prompt seed, and migration 00008's header — states the total is 54.
// Both cannot hold; this is a genuine specification defect, reported in
// full on catalogue.DocumentedTotal rather than silently reconciled.
//
// The defect is in the BREAKDOWN, not the total: docs/BASELINE.md §4
// records Phase 0 measuring 54 concrete types against a real clone of
// eveseat/notifications at a pinned commit, so one domain count is
// understated by one. Which one is not determinable here (BASELINE.md
// recorded only the total, and the upstream is not fetchable from this
// build environment), and inventing a 54th type into a guessed domain
// would replace a documented shortfall with a silent, wrong assignment.
//
// The fix, once someone with a clone re-runs BASELINE.md §4's pipeline
// grouped per category (the exact command is in catalogue.DocumentedTotal's
// doc comment), is one line: correct the short domain in
// catalogue.ExpectedCounts and add the type it names. At that point this
// test's arithmetic check starts agreeing with DocumentedTotal on its own,
// and the require.Equal(53) below is what will fail to say so.
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
	require.Equal(t, 53, catalogue.SeededTotal(),
		"the per-domain counts sum to 53 while docs/BASELINE.md §4 measured 54 upstream — see "+
			"catalogue.DocumentedTotal for the reported defect and the command that settles which domain is short")

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

	entry, ok := catalogue.ByName("WarAdopted ")
	require.True(t, ok, "CCP's literal spelling (with the trailing space) must resolve to the catalogue entry")
	require.Equal(t, "WarAdopted", entry.Name, "the catalogue stores the trimmed name")

	entry, ok = catalogue.ByName("WarAdopted")
	require.True(t, ok, "the trimmed spelling must resolve too, so the fix survives CCP correcting the spec")
	require.Equal(t, catalogue.DomainWars, entry.Domain)
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
