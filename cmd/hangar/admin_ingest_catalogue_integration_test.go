//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"time"
)

func migratedPool(t *testing.T) *pgxpool.Pool {
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
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

// TestCatalogueIngestPopulatesRoutes is the Phase 18 close-out proof for
// the defect recorded in SRS §0: catalogue.Boot — the whole Phase 2 boot
// sequence — had no caller outside an integration test, so a deployed
// installation never populated app.esi_route. Because app.sync_subscription
// carries a route_id foreign key into that table, an empty catalogue meant
// NOTHING in the ESI sync layer could run, or even be configured.
//
// This test runs the same ingestCatalogue() that `hangar admin
// ingest-catalogue` and serve's startup goroutine both call. It does not
// require network access: catalogue.Boot falls back to the embedded
// snapshot when ESI is unreachable, which is itself part of the contract
// (01_ARCHITECTURE.md §5.1's "Offline boot") and is asserted below.
func TestCatalogueIngestPopulatesRoutes(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	before, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	require.Empty(t, before, "a freshly migrated database has no routes — that is the defect's starting state")

	result, err := ingestCatalogue(ctx, pool, "", logger)
	require.NoError(t, err)
	require.Positive(t, result.Ingested, "the ingest must actually write routes")

	after, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	require.Positive(t, len(after), "app.esi_route is populated — the sync layer now has something to schedule")

	// Schedulable routes are what the planner joins against. An ingest that
	// produced only blocked routes would still leave the sync layer inert.
	schedulable, err := s.ListSchedulableEsiRoutes(ctx)
	require.NoError(t, err)
	require.Positive(t, len(schedulable))

	// D_max is recorded, so AdvancePin has a real ceiling rather than the
	// rollover fallback ([v3.1 — B13]).
	require.True(t, result.DMaxRecorded)
	_, source, err := catalogue.GetDMax(ctx, s, time.Now())
	require.NoError(t, err)
	require.Equal(t, "recorded", source)

	// THE PIN IS NEVER ADVANCED AS A SIDE EFFECT of an ingest
	// (01_ARCHITECTURE.md §5.1). This is the property that makes it safe to
	// run this automatically at every serve startup.
	pin, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, catalogue.DefaultCompatibilityPin, catalogue.FormatDate(pin))

	// Idempotent — serve runs this on every start, and every replica does.
	second, err := ingestCatalogue(ctx, pool, "", logger)
	require.NoError(t, err)
	require.Equal(t, result.Ingested, second.Ingested)
	require.Zero(t, second.Retired, "a re-ingest of the same spec must not retire anything")

	afterSecond, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, afterSecond, len(after))
}
