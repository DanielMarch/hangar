//go:build integration

package handlers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	return pool
}

func seedCharacter(t *testing.T, s *store.Store, characterID int64) {
	t.Helper()
	_, err := s.UpsertCharacter(context.Background(), gen.UpsertCharacterParams{
		CharacterID: characterID, Name: "Test Character", OwnerHash: "owner-hash-" + uuid.NewString(),
	})
	require.NoError(t, err)
}

// TestSecondSyncProducesZeroUpdatedAtChanges (roadmap exit criterion):
// re-syncing identical data changes no updated_at — the §3.5 IS DISTINCT
// FROM guard's whole purpose. Exercised across three different upsert
// shapes: a full-state-list-with-prune domain (skills), a single-row
// domain (attributes), and the bespoke character-sheet UPDATE.
func TestSecondSyncProducesZeroUpdatedAtChanges(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const characterID int64 = 2112625428
	seedCharacter(t, s, characterID)

	t.Run("character_skill (full-state-list with prune)", func(t *testing.T) {
		skills, err := handlers.ParseCharacterSkills(mustReadFixture(t, "skills.json"))
		require.NoError(t, err)

		_, err = handlers.SyncCharacterSkills(ctx, s, characterID, skills)
		require.NoError(t, err)
		firstUpdatedAt := skillUpdatedAt(t, pool, characterID, skills.Skills[0].SkillID)

		_, err = handlers.SyncCharacterSkills(ctx, s, characterID, skills)
		require.NoError(t, err)
		secondUpdatedAt := skillUpdatedAt(t, pool, characterID, skills.Skills[0].SkillID)

		require.Equal(t, firstUpdatedAt, secondUpdatedAt, "re-syncing identical skills must not change updated_at")
	})

	t.Run("character_attributes (single row)", func(t *testing.T) {
		attrs, err := handlers.ParseCharacterAttributes(mustReadFixture(t, "attributes.json"))
		require.NoError(t, err)

		_, err = handlers.SyncCharacterAttributes(ctx, s, characterID, attrs)
		require.NoError(t, err)
		first := attributesUpdatedAt(t, pool, characterID)

		_, err = handlers.SyncCharacterAttributes(ctx, s, characterID, attrs)
		require.NoError(t, err)
		second := attributesUpdatedAt(t, pool, characterID)

		require.Equal(t, first, second, "re-syncing identical attributes must not change updated_at")
	})

	t.Run("character sheet (bespoke UPDATE)", func(t *testing.T) {
		sheet, err := handlers.ParseCharacterSheet(mustReadFixture(t, "character_sheet.json"))
		require.NoError(t, err)

		_, err = handlers.SyncCharacterSheet(ctx, s, characterID, sheet)
		require.NoError(t, err)
		first := characterUpdatedAt(t, pool, characterID)

		_, err = handlers.SyncCharacterSheet(ctx, s, characterID, sheet)
		require.NoError(t, err)
		second := characterUpdatedAt(t, pool, characterID)

		require.Equal(t, first, second, "re-syncing an identical character sheet must not change updated_at")
	})
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdataDir, name))
	require.NoError(t, err)
	return b
}

func skillUpdatedAt(t *testing.T, pool *pgxpool.Pool, characterID, skillID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT updated_at FROM app.character_skill WHERE character_id = $1 AND skill_id = $2`, characterID, skillID,
	).Scan(&ts))
	return ts
}

func attributesUpdatedAt(t *testing.T, pool *pgxpool.Pool, characterID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT updated_at FROM app.character_attributes WHERE character_id = $1`, characterID,
	).Scan(&ts))
	return ts
}

func characterUpdatedAt(t *testing.T, pool *pgxpool.Pool, characterID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT updated_at FROM app.character WHERE character_id = $1`, characterID,
	).Scan(&ts))
	return ts
}
