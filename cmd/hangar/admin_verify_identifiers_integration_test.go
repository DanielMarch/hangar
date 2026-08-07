//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestIdentifierTypesMatchSpec is the Phase 1b exit criterion: every
// identifier column in the migrated app schema matches its declared type,
// including uuid columns (02_DATABASE_SCHEMA.md §3.2, Principle 13). It
// runs the exact check `hangar admin verify-identifier-types` runs, against
// a freshly migrated database, and additionally proves the check actually
// *can* fail by asserting a deliberately mistyped fixture column is caught.
func TestIdentifierTypesMatchSpec(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"),
		tcpostgres.WithUsername("hangar"),
		tcpostgres.WithPassword("hangar"),
	)
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

	mismatches, err := verifyIdentifierTypes(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, mismatches, "every identifier column in the migrated schema must match its declared type: %v", mismatches)

	// Prove the check has teeth: a bare uuid identifier column that is
	// neither self-generated nor a foreign key nor registered must be
	// flagged. app.corporation_project.project_id is exactly this shape —
	// temporarily un-registering it (by checking a synthetic table instead
	// of mutating the registry, which is process-global) demonstrates the
	// negative case without disturbing the real schema.
	_, err = pool.Exec(ctx, `CREATE TABLE app.__fixture_bad_identifier (bogus_id uuid NOT NULL PRIMARY KEY)`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS app.__fixture_bad_identifier`) })

	mismatches, err = verifyIdentifierTypes(ctx, pool)
	require.NoError(t, err)
	require.NotEmpty(t, mismatches, "an unregistered bare uuid identifier column must be caught")
}
