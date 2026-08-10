//go:build integration

package db_test

import (
	"context"
	"testing"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestApplySeedsPopulatesPermissionsAndRoles (Phase 10 fix): db/seed/*.sql
// existed since Phase 1a with nothing ever applying it against a live
// database — harmless until app.role_grant's FK to app.permission made
// this load-bearing. ApplySeeds (db/seed.go) closes that gap; this test
// proves it actually populates both tables and is safe to re-run.
func TestApplySeedsPopulatesPermissionsAndRoles(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()

	require.NoError(t, hangardb.ApplySeeds(ctx, pool))

	var permissionCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.permission`).Scan(&permissionCount))
	require.Equal(t, len(domain.Permissions), permissionCount, "every permission in the Go closed set must be seeded")

	var roleCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.role WHERE name IN ('admin', 'member')`).Scan(&roleCount))
	require.Equal(t, 2, roleCount, "the built-in admin/member roles must be seeded")

	// Idempotent: applying twice must not error or duplicate rows.
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.permission`).Scan(&permissionCount))
	require.Equal(t, len(domain.Permissions), permissionCount, "re-applying seeds must not duplicate rows")
}
