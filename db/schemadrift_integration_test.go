//go:build integration

package db_test

import (
	"context"
	"testing"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/stretchr/testify/require"
)

// ── PHASE 23 (N-6) ───────────────────────────────────────────────────────
//
// The parse is tested without a database in db/schemacheck_objects_test.go.
// This is the other half, and it is the half that matters: against a real,
// fully migrated PostgreSQL 18, does the expected set actually MATCH what
// Postgres holds — and does dropping one object of each kind get caught?
//
// The first of those is the load-bearing one. A check whose expected set
// does not match a correct database reports drift permanently, which is
// worse than no check: an operator learns to ignore it, and the day it means
// something they ignore it then too.

// TestAFullyMigratedDatabaseHasNoDrift is the baseline. It must hold for
// every kind, or every other assertion here is measuring noise.
func TestAFullyMigratedDatabaseHasNoDrift(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()

	drift, err := hangardb.MissingObjects(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, drift.Tables, "a freshly migrated database is missing no table")
	require.Empty(t, drift.Columns, "a freshly migrated database is missing no column — "+
		"if this fails, the CREATE TABLE parse is expecting something the migrations do not create")
	require.Empty(t, drift.Indexes, "a freshly migrated database is missing no index — "+
		"if this fails, the index SIGNATURE does not agree with pg_index for at least one declared index, "+
		"and the check would report drift on a correct database forever")
	require.True(t, drift.Empty())
}

// TestADroppedIndexIsCaught is N-6 stated as the thing that used to pass.
// db.MissingTables verified tables, so dropping an index left `migrate up`
// and every serving process reporting the schema current.
func TestADroppedIndexIsCaught(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()

	// A partial index, deliberately: its signature carries a field derived
	// from text after the column list, so it is the one most likely to be
	// matched loosely. `(state, next_attempt_at) WHERE state = 'pending'`
	// is also the index the alert pump's own claim runs on.
	var indexName string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT indexname FROM pg_indexes
		 WHERE schemaname = 'app' AND tablename = 'alert_delivery'
		   AND indexdef LIKE '%state%next_attempt_at%'`).Scan(&indexName))
	_, err := pool.Exec(ctx, `DROP INDEX app.`+indexName)
	require.NoError(t, err)

	drift, err := hangardb.MissingObjects(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, drift.Tables, "the table is still there; only its index went")
	require.Empty(t, drift.Columns)
	require.Len(t, drift.Indexes, 1, "exactly one index is missing, and the signature must identify which")
	require.Equal(t, "app", drift.Indexes[0].Schema)
	require.Equal(t, "alert_delivery", drift.Indexes[0].Table)
	require.Equal(t, []string{"state", "next_attempt_at"}, drift.Indexes[0].Columns)
	require.True(t, drift.Indexes[0].Partial)

	message := hangardb.FormatDrift(drift)
	require.Contains(t, message, "1 index(es)")
	require.Contains(t, message, "app.alert_delivery USING btree (state, next_attempt_at)")
	require.Contains(t, message, "will NOT restore them")
}

// TestADroppedColumnIsCaughtAgainstARealDatabase — the other kind, and the
// one whose consequences are correctness rather than performance.
func TestADroppedColumnIsCaughtAgainstARealDatabase(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `ALTER TABLE app.alert_delivery DROP COLUMN error`)
	require.NoError(t, err)

	drift, err := hangardb.MissingObjects(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, drift.Tables)
	require.Len(t, drift.Columns, 1)
	require.Equal(t, "app.alert_delivery.error", drift.Columns[0].String())
	require.Contains(t, hangardb.FormatDrift(drift), "1 column(s)")
}

// TestADroppedTableReportsItselfOnceAndNotThirtyTimes is the readability
// claim from db/schemadrift.go's header, asserted rather than asserted-in-a-
// comment. A dropped table that also reported each of its columns and each
// of its indexes would bury the one line an operator can act on.
func TestADroppedTableReportsItselfOnceAndNotThirtyTimes(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()

	// CASCADE because app.alert_delivery has foreign keys into it; the
	// point of the test is what the REPORT looks like, not what Postgres
	// permits.
	_, err := pool.Exec(ctx, `DROP TABLE app.alert_event CASCADE`)
	require.NoError(t, err)

	drift, err := hangardb.MissingObjects(ctx, pool)
	require.NoError(t, err)
	require.Len(t, drift.Tables, 1)
	require.Equal(t, "app.alert_event", drift.Tables[0].String())
	require.Empty(t, drift.Columns,
		"a dropped table must not also report each of its columns — the table line is the actionable one")
	require.Empty(t, drift.Indexes, "nor each of its indexes")
	require.Equal(t, 1, drift.Count())
}

// TestTheExpectedSetsAreNotTriviallySmall guards the failure mode every
// derived check has: a parse that silently matches nothing passes against
// any database at all. The numbers are floors, not equalities — a migration
// added next phase must not fail this — but they are high enough that a
// broken parse cannot clear them.
func TestTheExpectedSetsAreNotTriviallySmall(t *testing.T) {
	columns, err := hangardb.ExpectedColumns()
	require.NoError(t, err)
	indexes, err := hangardb.ExpectedIndexes()
	require.NoError(t, err)

	require.Greater(t, len(columns), 1000,
		"the column register parsed %d columns from 163 tables — a plausible schema has far more", len(columns))
	require.Greater(t, len(indexes), 100,
		"the index register parsed %d indexes — db/migrations declares 111 CREATE INDEX statements", len(indexes))
}
