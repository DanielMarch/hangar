package handlers_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/stretchr/testify/require"
)

// corporationGoldenParsers maps testdata/esi/corporation/<key>.json to the
// Parse function that should consume it with no field loss — Phase 8's
// analogue of golden_test.go's goldenParsers.
var corporationGoldenParsers = map[string]func([]byte) (any, error){
	"corporation_sheet":        func(b []byte) (any, error) { return handlers.ParseCorporationSheet(b) },
	"members":                  func(b []byte) (any, error) { return handlers.ParseCorporationMembers(b) },
	"membertracking":           func(b []byte) (any, error) { return handlers.ParseCorporationMemberTracking(b) },
	"members_titles":           func(b []byte) (any, error) { return handlers.ParseCorporationMemberTitles(b) },
	"titles":                   func(b []byte) (any, error) { return handlers.ParseCorporationTitles(b) },
	"roles":                    func(b []byte) (any, error) { return handlers.ParseCorporationRoles(b) },
	"roles_history":            func(b []byte) (any, error) { return handlers.ParseCorporationRoleHistory(b) },
	"divisions":                func(b []byte) (any, error) { return handlers.ParseCorporationDivisions(b) },
	"shareholders":             func(b []byte) (any, error) { return handlers.ParseCorporationShareholders(b) },
	"facilities":               func(b []byte) (any, error) { return handlers.ParseCorporationFacilities(b) },
	"customs_offices":          func(b []byte) (any, error) { return handlers.ParseCorporationCustomsOffices(b) },
	"container_logs":           func(b []byte) (any, error) { return handlers.ParseCorporationContainerLog(b) },
	"structures":               func(b []byte) (any, error) { return handlers.ParseCorporationStructures(b) },
	"starbases":                func(b []byte) (any, error) { return handlers.ParseCorporationStarbases(b) },
	"starbase_detail":          func(b []byte) (any, error) { return handlers.ParseCorporationStarbaseDetail(b) },
	"skyhook_list":             func(b []byte) (any, error) { return handlers.ParseCorporationSkyhookList(b) },
	"skyhook_detail":           func(b []byte) (any, error) { return handlers.ParseCorporationSkyhookDetail(b) },
	"sovereignty_hub_list":     func(b []byte) (any, error) { return handlers.ParseCorporationSovereigntyHubList(b) },
	"sovereignty_hub_detail":   func(b []byte) (any, error) { return handlers.ParseCorporationSovereigntyHubDetail(b) },
	"alliance_history":         func(b []byte) (any, error) { return handlers.ParseCorporationAllianceHistory(b) },
	"medals":                   func(b []byte) (any, error) { return handlers.ParseCorporationMedals(b) },
	"medals_issued":            func(b []byte) (any, error) { return handlers.ParseCorporationMedalsIssued(b) },
	"standings":                func(b []byte) (any, error) { return handlers.ParseCorporationStandings(b) },
	"contacts":                 func(b []byte) (any, error) { return handlers.ParseCorporationContacts(b) },
	"contact_labels":           func(b []byte) (any, error) { return handlers.ParseCorporationContactLabels(b) },
	"wallet_balances":          func(b []byte) (any, error) { return handlers.ParseCorporationWalletBalances(b) },
	"wallet_balance_character": func(b []byte) (any, error) { return handlers.ParseCharacterWalletBalance(b) },
	"wallet_journal":           func(b []byte) (any, error) { return handlers.ParseWalletJournalPage(b) },
	"wallet_transactions":      func(b []byte) (any, error) { return handlers.ParseWalletTransactionsPage(b) },
	"industry_jobs":            func(b []byte) (any, error) { return handlers.ParseIndustryJobs(b) },
	"blueprints":               func(b []byte) (any, error) { return handlers.ParseBlueprints(b) },
	"mining_ledger_character":  func(b []byte) (any, error) { return handlers.ParseCharacterMiningLedger(b) },
	"mining_extractions":       func(b []byte) (any, error) { return handlers.ParseCorporationMiningExtractions(b) },
	"mining_observers":         func(b []byte) (any, error) { return handlers.ParseCorporationMiningObservers(b) },
	"mining_observer_records":  func(b []byte) (any, error) { return handlers.ParseCorporationMiningObserverRecords(b) },
	"market_orders":            func(b []byte) (any, error) { return handlers.ParseMarketOrders(b) },
	"market_order_history":     func(b []byte) (any, error) { return handlers.ParseMarketOrderHistory(b) },
	"sovereignty_campaigns":    func(b []byte) (any, error) { return handlers.ParseSovereigntyCampaigns(b) },
	"sovereignty_systems":      func(b []byte) (any, error) { return handlers.ParseSovereigntySystems(b) },

	// Phase 9 additions.
	"projects":              func(b []byte) (any, error) { return handlers.ParseCorporationProjects(b) },
	"project_contributions": func(b []byte) (any, error) { return handlers.ParseCorporationProjectContributions(b) },
}

const corporationTestdataDir = "../../../testdata/esi/corporation"

// TestGoldenFileParsesAllCorporationDomains (roadmap exit criterion,
// analogous to Phase 7's TestGoldenFileParsesAllCharacterDomains): every
// recorded ESI response under testdata/esi/corporation parses into its DTO
// with no field loss, and every registered parser has a fixture.
// corporationEnvelopeField names, per fixture, the property a parser
// unwraps. Absent means "the parser returns the whole document".
var corporationEnvelopeField = map[string]string{"projects": "projects"}

func TestGoldenFileParsesAllCorporationDomains(t *testing.T) {
	entries, err := os.ReadDir(corporationTestdataDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "no golden fixtures found under %s", corporationTestdataDir)

	tested := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		key := entry.Name()[:len(entry.Name())-len(".json")]
		t.Run(key, func(t *testing.T) {
			parse, ok := corporationGoldenParsers[key]
			require.Truef(t, ok, "testdata/esi/corporation/%s.json has no registered parser in golden_corporation_test.go's corporationGoldenParsers map", entry.Name())

			raw, err := os.ReadFile(filepath.Join(corporationTestdataDir, entry.Name()))
			require.NoError(t, err)

			dto, err := parse(raw)
			require.NoErrorf(t, err, "parsing %s", entry.Name())

			// `projects` is the one corporation route whose 200 response is
			// an OBJECT wrapping the array (CorporationsProjectsListing),
			// not a bare array — see assertNoFieldLossUnder.
			assertNoFieldLossUnder(t, raw, dto, corporationEnvelopeField[key])
		})
		tested++
	}
	require.Equal(t, len(corporationGoldenParsers), tested, "every registered parser must have a fixture and vice versa")
}
