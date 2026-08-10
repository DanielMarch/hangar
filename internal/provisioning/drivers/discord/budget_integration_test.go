//go:build integration

package discord_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/discord"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool(t testing.TB) *pgxpool.Pool {
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

// TestPauseAt80PercentInvalidBudget (roadmap exit criterion): processing
// halts at 80% of the 10k/10min budget — exercised here against a small
// max (10) so the test doesn't need 8,000 real round trips, and a fixed
// window long enough that the test's own runtime can never cross it
// (avoiding a flaky rollover mid-test).
func TestPauseAt80PercentInvalidBudget(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const max = 10 // warnAt=5 (50%), pauseAt=8 (80%)
	budget := discord.NewInvalidBudget(s, time.Hour, max, 50, 80, discord.SystemClock, slog.Default())
	require.NoError(t, budget.Init(ctx))

	paused, err := budget.IsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused, "a fresh budget must start unpaused")

	// Record invalid responses one at a time, checking the pause state
	// crosses false -> true exactly at the 80% threshold (8th of 10).
	for i := 1; i <= 10; i++ {
		require.NoError(t, budget.RecordInvalid(ctx))
		paused, err := budget.IsPaused(ctx)
		require.NoError(t, err)
		if i < 8 {
			require.Falsef(t, paused, "must not be paused before the 80%% threshold (count=%d)", i)
		} else {
			require.Truef(t, paused, "must be paused at/after the 80%% threshold (count=%d)", i)
		}
	}
}

// TestInvalidBudgetOnlyCountsQualifyingResponses (roadmap edge case):
// ShouldCount is the gate RecordInvalid's callers apply BEFORE calling it
// — a shared-scope 429 must never reach the budget at all.
func TestInvalidBudgetOnlyCountsQualifyingResponses(t *testing.T) {
	require.True(t, discord.ShouldCount(401, ""))
	require.True(t, discord.ShouldCount(403, ""))
	require.True(t, discord.ShouldCount(429, ""))
	require.False(t, discord.ShouldCount(429, "shared"), "a shared-resource 429 must not count against the invalid budget")
	require.True(t, discord.ShouldCount(429, "user"), "a non-shared 429 still counts")
	require.False(t, discord.ShouldCount(200, ""))
	require.False(t, discord.ShouldCount(500, ""))
}

// TestInvalidBudgetResumesOnWindowRollover: once the fixed window rolls
// over, the counter resets and a subsequent low count un-pauses.
func TestInvalidBudgetResumesOnWindowRollover(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const max = 10
	shortWindow := 200 * time.Millisecond
	budget := discord.NewInvalidBudget(s, shortWindow, max, 50, 80, discord.SystemClock, slog.Default())
	require.NoError(t, budget.Init(ctx))

	for i := 0; i < 9; i++ {
		require.NoError(t, budget.RecordInvalid(ctx))
	}
	paused, err := budget.IsPaused(ctx)
	require.NoError(t, err)
	require.True(t, paused)

	time.Sleep(shortWindow + 50*time.Millisecond)
	require.NoError(t, budget.RecordInvalid(ctx)) // the atomic UPDATE rolls the window over, count resets to 1

	paused, err = budget.IsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused, "the window rollover must have un-paused the budget")
}
