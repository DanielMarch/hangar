//go:build integration

package v1_test

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
	"github.com/hangar-project/hangar/internal/store"
)

// TestUnknownBoardsAcknowledge is a Phase 18 exit criterion —
// "acknowledging writes `acknowledged_at` and clears the board" — that was
// NAMED in the roadmap's Phase 18 table but never written.
//
// Found during the Phase 19 close-out audit by extracting every `Test*`
// name from docs/03_IMPLEMENTATION_ROADMAP.md and checking each one exists
// somewhere in the tree. It was the only one of 138 with no implementation
// in either the Go or the web suites. Phase 18 did close the related defect
// underneath it (web/src/api/client.test.ts covers `unwrap` treating a 204
// as a success, which is what made the Acknowledge button appear to do
// nothing in a browser), but that is the transport bug, not this criterion:
// nothing asserted that acknowledging actually clears the row from the
// board.
//
// BOTH boards, because they are separate tables with separate queries and
// separate permissions, and the roadmap's criterion is plural:
//
//   - app.notification_unknown_type — CCP notification types this build's
//     catalogue does not know (§4.4, Principle 14);
//   - app.esi_scope — scope strings observed in the wild that no seeded
//     vocabulary lists (§5.1).
//
// The assertion is deliberately end-to-end over the STORE rather than over
// HTTP: the criterion is about the acknowledgement being durable and the
// board reflecting it, and routing it through Huma would test the router
// instead. The two handlers in admin.go are one line each on top of exactly
// these queries.
func TestUnknownBoardsAcknowledge(t *testing.T) {
	ctx := context.Background()
	pool := newUnknownBoardsPool(t)
	s := store.New(pool)

	t.Run("unknown notification types", func(t *testing.T) {
		const unknown = "SomeTypeCCPInventedYesterday"
		const other = "AnotherUnknownType"

		require.NoError(t, s.RecordUnknownNotificationType(ctx, unknown, []byte(`{"a":1}`)))
		require.NoError(t, s.RecordUnknownNotificationType(ctx, other, []byte(`{"b":2}`)))

		board, err := s.ListUnacknowledgedNotificationTypes(ctx)
		require.NoError(t, err)
		require.Len(t, board, 2, "both unrecognised types must reach the board")

		require.NoError(t, s.AcknowledgeNotificationType(ctx, unknown))

		board, err = s.ListUnacknowledgedNotificationTypes(ctx)
		require.NoError(t, err)
		require.Len(t, board, 1, "acknowledging must CLEAR the row from the board — a board that only grows gets ignored")
		require.Equal(t, other, board[0].Type, "acknowledging one type must not clear the others")

		// ...and the acknowledgement is durable, not merely a filter.
		var acknowledgedAt *time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT acknowledged_at FROM app.notification_unknown_type WHERE type = $1`, unknown).
			Scan(&acknowledgedAt))
		require.NotNil(t, acknowledgedAt, "acknowledged_at must be written, not just excluded from a query")

		// Re-acknowledging is idempotent: an operator double-clicking the
		// button must not error, and must not resurrect the row.
		require.NoError(t, s.AcknowledgeNotificationType(ctx, unknown))
		board, err = s.ListUnacknowledgedNotificationTypes(ctx)
		require.NoError(t, err)
		require.Len(t, board, 1)

		// A type that was never on the board is a no-op, not an error —
		// the handler must not 500 on a stale id from a re-submitted form.
		require.NoError(t, s.AcknowledgeNotificationType(ctx, "NeverSeenThisOne"))
	})

	t.Run("newly observed ESI scopes", func(t *testing.T) {
		const novel = "esi-activity.read_campaigns.v1"
		const alsoNovel = "esi-something.else.v2"

		require.NoError(t, s.UpsertEsiScope(ctx, novel))
		require.NoError(t, s.UpsertEsiScope(ctx, alsoNovel))

		board, err := s.ListUnacknowledgedEsiScopes(ctx)
		require.NoError(t, err)
		require.Len(t, board, 2)

		require.NoError(t, s.AcknowledgeEsiScope(ctx, novel))

		board, err = s.ListUnacknowledgedEsiScopes(ctx)
		require.NoError(t, err)
		require.Len(t, board, 1)
		require.Equal(t, alsoNovel, board[0].Scope)

		row, err := s.GetEsiScope(ctx, novel)
		require.NoError(t, err)
		require.NotNil(t, row.AcknowledgedAt)

		// Re-recording an already-acknowledged scope must NOT put it back
		// on the board. Principle 14 says an unknown scope is registered
		// rather than rejected, and UpsertEsiScope runs on every sync that
		// sees the string — so if the ON CONFLICT clause reset
		// acknowledged_at, the board would refill itself and the
		// acknowledge action would be useless in practice while passing
		// every test that only acknowledges once.
		require.NoError(t, s.UpsertEsiScope(ctx, novel))
		board, err = s.ListUnacknowledgedEsiScopes(ctx)
		require.NoError(t, err)
		require.Len(t, board, 1, "re-observing an acknowledged scope must not resurrect it onto the board")
	})
}

func newUnknownBoardsPool(t testing.TB) *pgxpool.Pool {
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
