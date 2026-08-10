//go:build integration

package sde_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/sde"
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

// dirSource wraps sde.DirSource but lets a test inject a failure partway
// through the build by returning an error for one named table.
type failingSource struct {
	sde.DirSource
	failOn string
}

func (f failingSource) Open(ctx context.Context, table string) (io.ReadCloser, error) {
	if table == f.failOn {
		return nil, fmt.Errorf("injected failure opening %s", table)
	}
	return f.DirSource.Open(ctx, table)
}

// TestSDEAtomicSwap (roadmap exit criterion): the live `sde` schema is
// completely untouched when a build fails partway through, and the swap
// is atomic on success.
func TestSDEAtomicSwap(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()

	// A pre-existing row in the live `sde` schema, so "untouched on
	// failure" has something concrete to assert against rather than just
	// "still empty".
	_, err := pool.Exec(ctx, `INSERT INTO sde.category (category_id, name, data) VALUES (6, 'Ship', '{}'::jsonb)`)
	require.NoError(t, err)

	t.Run("failed build leaves the live schema completely untouched", func(t *testing.T) {
		src := failingSource{DirSource: sde.DirSource{Dir: "../../testdata/sde"}, failOn: "type"}

		result, buildErr := sde.Build(ctx, pool, src)
		require.Error(t, buildErr, "an injected mid-build failure must propagate")
		require.NoError(t, sde.AbortBuild(ctx, pool), "the failed sde_next build must be droppable")
		_ = result

		var sdeNextExists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'sde_next')`).Scan(&sdeNextExists))
		require.False(t, sdeNextExists, "sde_next must not survive a failed build")

		// The live schema must still answer queries with its pre-import
		// content — the literal property this test exists to check.
		var name string
		require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM sde.category WHERE category_id = 6`).Scan(&name))
		require.Equal(t, "Ship", name)
	})

	t.Run("a clean build swaps atomically", func(t *testing.T) {
		src := sde.DirSource{Dir: "../../testdata/sde"}

		result, buildErr := sde.Build(ctx, pool, src)
		require.NoError(t, buildErr)
		require.NotZero(t, result.RowCounts["type"])

		require.NoError(t, sde.Verify(ctx, pool, result, sde.DefaultSmokeQueries()))
		require.NoError(t, sde.Swap(ctx, pool, 0))

		var sdeOldExists, sdeNextExists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'sde_old')`).Scan(&sdeOldExists))
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'sde_next')`).Scan(&sdeNextExists))
		require.False(t, sdeOldExists, "sde_old must be dropped after the grace period")
		require.False(t, sdeNextExists, "sde_next must no longer exist under that name after the rename")

		// The post-swap `sde` schema must be the NEW content, not the old
		// pre-import row seeded above (its own table was cloned fresh into
		// sde_next, which came with no rows for category beyond the fixture).
		var rifter string
		require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM sde.type WHERE type_id = 587`).Scan(&rifter))
		require.Equal(t, "Rifter", rifter)

		var categoryCount int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sde.category`).Scan(&categoryCount))
		require.Equal(t, 2, categoryCount, "post-swap sde.category must be the freshly imported fixture, not the pre-swap seeded row")
	})
}
