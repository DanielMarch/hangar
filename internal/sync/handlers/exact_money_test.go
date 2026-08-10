package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// TestExactMoneyRoundTrip (roadmap exit criterion): NUMERIC(30,2) ->
// decimal.Decimal -> JSON string with no precision loss at 10^20 — a value
// float64 cannot represent exactly (float64 has ~15-17 significant decimal
// digits; 10^20 has 21). Exercised through the actual wallet DTOs
// (wallet.go) rather than a bare decimal.Decimal, so the test proves the
// whole parse path, not just the decimal library in isolation.
func TestExactMoneyRoundTrip(t *testing.T) {
	huge := "100000000000000000000.55" // 10^20 + 0.55, 23 significant digits

	t.Run("wallet balance", func(t *testing.T) {
		body := []byte(huge)
		dto, err := handlers.ParseCharacterWalletBalance(body)
		require.NoError(t, err)
		require.True(t, dto.Balance.Equal(decimal.RequireFromString(huge)), "got %s, want %s", dto.Balance.String(), huge)
		require.Equal(t, huge, dto.Balance.String(), "decimal.String() must reproduce the exact input, no float64 rounding")

		// JSON round trip: decimal.Decimal marshals as a JSON number by
		// default, still exact — re-parse and compare string forms.
		out, err := json.Marshal(dto.Balance)
		require.NoError(t, err)
		var back decimal.Decimal
		require.NoError(t, json.Unmarshal(out, &back))
		require.Equal(t, huge, back.String())
	})

	t.Run("wallet journal amount/balance/tax", func(t *testing.T) {
		raw := `[{"id":1,"ref_type":"bounty_prize","description":"x","date":"2026-08-01T00:00:00Z",` +
			`"amount":` + huge + `,"balance":` + huge + `,"tax":0.01}]`
		entries, err := handlers.ParseWalletJournalPage([]byte(raw))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.True(t, entries[0].Amount.Valid)
		require.Equal(t, huge, entries[0].Amount.Decimal.String())
		require.Equal(t, huge, entries[0].Balance.Decimal.String())
		require.Equal(t, "0.01", entries[0].Tax.Decimal.String())
	})

	t.Run("wallet transaction unit_price", func(t *testing.T) {
		raw := `[{"transaction_id":1,"date":"2026-08-01T00:00:00Z","is_buy":true,"journal_ref_id":2,` +
			`"location_id":1,"quantity":1,"type_id":1,"client_id":1,"unit_price":` + huge + `}]`
		entries, err := handlers.ParseWalletTransactionsPage([]byte(raw))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, huge, entries[0].UnitPrice.String())
	})
}
