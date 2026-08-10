//go:build integration

package handlers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const corporationFixtureDir = "../../../testdata/esi/corporation"

func seedCorporation(t *testing.T, s *store.Store, corporationID int64) {
	t.Helper()
	_, err := s.UpsertCorporation(context.Background(), gen.UpsertCorporationParams{
		CorporationID: corporationID, Name: "Test Corp " + uuid.NewString(), Ticker: "TEST",
	})
	require.NoError(t, err)
}

// TestSecondSyncProducesZeroUpdatedAtChangesCorporation (roadmap exit
// criterion, Phase 8's corporation-domain counterpart to Phase 7's
// TestSecondSyncProducesZeroUpdatedAtChanges): re-syncing identical
// corporation data changes no updated_at, across a full-state-list domain
// (structures), a single-row domain (the corporation sheet itself), and
// the exact-money wallet balance upsert.
func TestSecondSyncProducesZeroUpdatedAtChangesCorporation(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const corporationID int64 = 98000001
	seedCorporation(t, s, corporationID)

	t.Run("corporation_structure (full-state list)", func(t *testing.T) {
		structures, err := handlers.ParseCorporationStructures(mustReadCorpFixture(t, "structures.json"))
		require.NoError(t, err)

		_, err = handlers.SyncCorporationStructures(ctx, s, corporationID, structures)
		require.NoError(t, err)
		first := structureUpdatedAt(t, pool, corporationID, structures[0].StructureID)

		_, err = handlers.SyncCorporationStructures(ctx, s, corporationID, structures)
		require.NoError(t, err)
		second := structureUpdatedAt(t, pool, corporationID, structures[0].StructureID)

		require.Equal(t, first, second, "re-syncing identical structures must not change updated_at")
	})

	t.Run("corporation sheet (single row)", func(t *testing.T) {
		sheet, err := handlers.ParseCorporationSheet(mustReadCorpFixture(t, "corporation_sheet.json"))
		require.NoError(t, err)

		_, err = handlers.SyncCorporationSheet(ctx, s, corporationID, sheet)
		require.NoError(t, err)
		first := corporationUpdatedAt(t, pool, corporationID)

		_, err = handlers.SyncCorporationSheet(ctx, s, corporationID, sheet)
		require.NoError(t, err)
		second := corporationUpdatedAt(t, pool, corporationID)

		require.Equal(t, first, second, "re-syncing an identical corporation sheet must not change updated_at")
	})

	t.Run("wallet balance (exact money upsert)", func(t *testing.T) {
		balances, err := handlers.ParseCorporationWalletBalances(mustReadCorpFixture(t, "wallet_balances.json"))
		require.NoError(t, err)

		_, err = handlers.SyncWalletBalances(ctx, s, "corporation", corporationID, balances)
		require.NoError(t, err)
		first := walletBalanceUpdatedAt(t, pool, corporationID, balances[0].Division)

		_, err = handlers.SyncWalletBalances(ctx, s, "corporation", corporationID, balances)
		require.NoError(t, err)
		second := walletBalanceUpdatedAt(t, pool, corporationID, balances[0].Division)

		require.Equal(t, first, second, "re-syncing an identical wallet balance must not change updated_at")
	})
}

func mustReadCorpFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corporationFixtureDir, name))
	require.NoError(t, err)
	return b
}

func structureUpdatedAt(t *testing.T, pool *pgxpool.Pool, corporationID, structureID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT updated_at FROM app.corporation_structure WHERE corporation_id = $1 AND structure_id = $2`, corporationID, structureID,
	).Scan(&ts))
	return ts
}

func corporationUpdatedAt(t *testing.T, pool *pgxpool.Pool, corporationID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT updated_at FROM app.corporation WHERE corporation_id = $1`, corporationID,
	).Scan(&ts))
	return ts
}

func walletBalanceUpdatedAt(t *testing.T, pool *pgxpool.Pool, ownerID int64, division int16) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT updated_at FROM app.wallet_balance WHERE owner_kind = 'corporation' AND owner_id = $1 AND division = $2`, ownerID, division,
	).Scan(&ts))
	return ts
}
