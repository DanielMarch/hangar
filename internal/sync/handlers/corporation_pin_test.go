package handlers_test

import (
	"testing"

	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/stretchr/testify/require"
)

// TestCorporationDTOsMatchLiveSpec (roadmap exit criterion, corp-domain
// pin-accuracy analogue of TestCharacterDTOMatchesPin20260804): asserts a
// handful of Phase 8 field-shape facts confirmed directly against the live
// embedded spec (internal/esi/catalogue/embedded/openapi.snapshot.json)
// during implementation, several of which contradict what a naive port
// from the character-domain equivalent would produce.
func TestCorporationDTOsMatchLiveSpec(t *testing.T) {
	t.Run("corporation contacts have no is_blocked field", func(t *testing.T) {
		// Unlike CharacterContactDTO (character_social.go), which the live
		// spec DOES give an is_blocked property — the corp variant's schema
		// has no such property at all, not merely omitted-when-false.
		tags := jsonTags(t, handlers.CorporationContactDTO{})
		require.NotContains(t, tags, "is_blocked", "GET /corporations/{corporation_id}/contacts has no is_blocked field in the live spec")
		require.Contains(t, tags, "is_watched")
	})

	t.Run("corporation orders carry region_id and issued_by", func(t *testing.T) {
		tags := jsonTags(t, handlers.MarketOrderDTO{})
		require.Contains(t, tags, "region_id", "region_id is a required field on GET /corporations/{id}/orders")
		require.Contains(t, tags, "issued_by", "issued_by is a required field on the live spec, parsed for field-loss coverage even though app.market_order has no column for it")
	})

	t.Run("market order history carries state (active vs historical distinction)", func(t *testing.T) {
		tags := jsonTags(t, handlers.MarketOrderHistoryDTO{})
		require.Contains(t, tags, "state", "the roadmap explicitly warns against collapsing the still-active vs historical/delivered distinction")
	})

	t.Run("industry job status is the still-active vs delivered distinction, not two endpoints", func(t *testing.T) {
		tags := jsonTags(t, handlers.IndustryJobDTO{})
		require.Contains(t, tags, "status")
		require.Contains(t, tags, "successful_runs", "successful_runs only appears once a job has actually completed")
	})

	t.Run("corporation members is a bare array of character_id, not an object", func(t *testing.T) {
		// ParseCorporationMembers's signature (int64, not a wrapper struct)
		// IS the assertion here — this subtest exists so a future change to
		// that signature (e.g. wrapping it in a struct to "look like" the
		// other list endpoints) fails loudly at compile time via the line
		// below rather than silently drifting from the live spec.
		var _ func([]byte) ([]int64, error) = handlers.ParseCorporationMembers
	})

	t.Run("skyhook and sovereignty-hub LIST endpoints are wrapped objects, not bare arrays", func(t *testing.T) {
		// Unlike /corporations/{id}/members (bare array) and most other
		// Phase 8 list endpoints, these two wrap their array in a named
		// property ({"skyhooks":[...]}, {"sovereignty_hubs":[...]}).
		require.Contains(t, jsonTags(t, handlers.CorporationSkyhookListDTO{}), "skyhooks")
		require.Contains(t, jsonTags(t, handlers.CorporationSovereigntyHubListDTO{}), "sovereignty_hubs")
	})

	t.Run("wallet journal id field is `id`, not `journal_id`", func(t *testing.T) {
		tags := jsonTags(t, handlers.WalletJournalEntryDTO{})
		require.Contains(t, tags, "id")
		require.NotContains(t, tags, "journal_id", "the wire field is `id`; journal_id is only the app.wallet_journal column name")
	})

	t.Run("mining extraction carries structure_id (Phase 8 fixup, not the original Phase 1b column set)", func(t *testing.T) {
		tags := jsonTags(t, handlers.CorporationMiningExtractionDTO{})
		require.Contains(t, tags, "structure_id", "see 00032_phase8_mining_extraction_structure_id.sql — a required field the original migration lacked a column for")
	})
}
