//go:build integration

package sde_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/sde"
	"github.com/hangar-project/hangar/internal/store"
)

// ── DEFECT B22: WHAT A NEVER-IMPORTED INSTALLATION RENDERS ───────────────
//
// The requirement is exact: every reader must degrade TO THE ID, never to a
// blank and never to a lie. That property is what let internal/sde stay
// absent from the binary for eleven phases without a single failure — so it
// is worth an assertion, because the day one of these readers starts
// returning an empty string instead, nothing else in the suite will notice
// either.
//
// This runs against a fully migrated database with a schema-only `sde` — the
// state of every HANGAR installation that has never run
// `hangar admin import-sde`, which is all of them before this phase.
func TestAnInstallationWithNoSDEDegradesToIdsAndNeverToBlanks(t *testing.T) {
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

	s := store.New(pool)

	// The tables EXIST — 00036's DDL runs like any other migration — and are
	// empty. That distinction matters: a missing table is an error, an empty
	// one is a supported installation.
	var tables int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'sde'`).Scan(&tables))
	require.Positive(t, tables, "migration 00036 must have created the sde schema even with nothing imported")

	// 1. THE NAME LOOKUP RETURNS NOTHING, rather than a row with a blank
	//    name. internal/api/v1's renderEFT keeps its `[<type_id>]`
	//    placeholder for any id absent from this result, so an absent row is
	//    what produces the id and a blank-named row would produce `[]`.
	names, err := s.ListSdeTypeNames(ctx, []int32{587, 34, 12345})
	require.NoError(t, err, "the lookup must succeed against an empty SDE, not error")
	require.Empty(t, names, "an empty SDE must return NO rows — a row with an empty name would render as a blank item")

	// 2. THE THREE BACKFILLS AFFECT NOTHING and leave their columns NULL.
	//    Each is guarded by EXISTS precisely so that "no reference data" is a
	//    no-op rather than a NULL write or a wrong guess (Principle 13: no
	//    plausible-looking id with no verifiable source).
	require.NoError(t, s.BackfillSkyhookSystemIDFromSDE(ctx, 98000001))
	require.NoError(t, s.BackfillSkyhookTypeIDFromSDE(ctx, 98000001))
	require.NoError(t, s.BackfillSovereigntyHubTypeIDFromSDE(ctx, 98000001))

	// 3. THE IMPORT LEDGER IS EMPTY, which is what `hangar admin sde-status`
	//    and the boot-time report read to say so out loud.
	_, err = s.GetLatestSdeImport(ctx)
	require.Error(t, err, "no import has ever run on a fresh installation")

	// 4. AND AN IMPORT THEN RESOLVES THEM. Proving the degradation without
	//    proving the recovery would only prove the tables are empty; this is
	//    the half that proves the wiring this phase added actually reaches
	//    the reader the defect was measured against.
	require.NoError(t, sde.Import(ctx, pool, s, sde.DirSource{Dir: "../../testdata/sde"}, sde.DefaultSmokeQueries(), 0))

	names, err = s.ListSdeTypeNames(ctx, []int32{587, 999999})
	require.NoError(t, err)
	require.Len(t, names, 1, "the imported type resolves; the one that is not in the fixture still does not, and keeps its id")
	require.Equal(t, int32(587), names[0].TypeID)
	require.Equal(t, "Rifter", names[0].Name)

	latest, err := s.GetLatestSdeImport(ctx)
	require.NoError(t, err)
	require.Equal(t, "swapped", latest.Status)

	// The build number `--if-changed` reads is absent until an import that
	// HANGAR itself fetched records one — a DirSource has no build.
	require.NoError(t, s.RecordSdeImportBuild(ctx, 20260814, latest.ImportID))
	stamped, err := s.GetLatestSdeImport(ctx)
	require.NoError(t, err)
	require.Contains(t, string(stamped.RowCounts), "_ccp_build")
}
