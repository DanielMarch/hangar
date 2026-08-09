//go:build integration

package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// newMigratedPool boots a real, migrated PG18 via testcontainers, following
// the same pattern internal/esi/ratelimit, internal/esi/cache, and
// internal/esi/catalogue use for their Postgres-backed integration suites.
// Unlike those, this package also inserts into river_job, so River's own
// queue-table migrations run first — mirroring cmd/hangar/migrate.go's
// runMigrateUp exactly.
func newMigratedPool(t testing.TB) (*pgxpool.Pool, string) {
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

	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	return pool, connStr
}

// insertOnlyRiverClient builds a River client suitable for InsertTx but
// never Start-ed — exactly what Phase 6's planner needs, since the worker
// that would run "sync_route" jobs doesn't exist until Phase 7+
// (river.Config's own doc: "an insert-only client can be initialized by
// omitting Queues, and not calling Start").
func insertOnlyRiverClient(t testing.TB, pool *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	return client
}

func seedRoute(t testing.TB, pool *pgxpool.Pool, cacheMode string, blockedByPin bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	q := gen.New(pool)
	opID := "test_op_" + uuid.NewString()
	var mode *string
	if cacheMode != "" {
		mode = &cacheMode
	}
	row, err := q.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID:       opID,
		Method:            "GET",
		UpstreamPath:      "/test/" + opID,
		CacheAge:          pgtype.Interval{},
		CacheMode:         mode,
		CompatibilityDate: pgtype.Date{Time: time.Now(), Valid: true},
		BlockedByPin:      blockedByPin,
		SpecFragment:      json.RawMessage(`{}`),
		IdentifierTypes:   json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	return row.RouteID
}

func seedDueSubscription(t testing.TB, pool *pgxpool.Pool, routeID uuid.UUID, entityID int64, dueAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	q := gen.New(pool)
	row, err := q.UpsertSyncSubscription(ctx, gen.UpsertSyncSubscriptionParams{
		EntityKind: "character",
		EntityID:   entityID,
		RouteID:    routeID,
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE app.sync_subscription SET next_due_at = $2 WHERE subscription_id = $1`, row.SubscriptionID, dueAt)
	require.NoError(t, err)
	return row.SubscriptionID
}

// newTestPlanner builds a Planner that has genuinely acquired leadership
// against connStr (the exact production path — TryAcquireLeader — not a
// test-only seam), so every test in this file exercises real advisory-lock
// semantics end to end.
func newTestPlanner(t testing.TB, pool *pgxpool.Pool, connStr string, cfg Config) *Planner {
	t.Helper()
	ctx := context.Background()
	riverClient := insertOnlyRiverClient(t, pool)
	p := New(pool, riverClient, cfg, nil)
	leader, ok, err := TryAcquireLeader(ctx, connStr)
	require.NoError(t, err)
	require.True(t, ok, "test planner must be able to acquire leadership on a fresh database")
	p.leader = leader
	t.Cleanup(func() { _ = leader.Release(context.Background()) })
	return p
}

// TestBlockedByPinExcludedFromScheduling (roadmap exit criterion): a
// blocked_by_pin route's due subscription must never be claimed — checked
// in ClaimDueSubscriptions's own predicate (db/queries/sync_subscription.sql),
// never as a post-claim filter.
func TestBlockedByPinExcludedFromScheduling(t *testing.T) {
	pool, connStr := newMigratedPool(t)
	ctx := context.Background()

	blockedRoute := seedRoute(t, pool, "", true)
	openRoute := seedRoute(t, pool, "", false)
	now := time.Now()
	blockedSub := seedDueSubscription(t, pool, blockedRoute, 1001, now.Add(-time.Minute))
	openSub := seedDueSubscription(t, pool, openRoute, 1002, now.Add(-time.Minute))

	p := newTestPlanner(t, pool, connStr, Config{ClaimBatchSize: 100, ClaimLease: time.Minute})
	result, err := p.claimOnce(ctx)
	require.NoError(t, err)

	require.Contains(t, result.SubscriptionIDs, openSub, "the unblocked route's subscription must be claimed")
	require.NotContains(t, result.SubscriptionIDs, blockedSub, "a blocked_by_pin route's subscription must never be claimed")

	// The blocked subscription's next_due_at must be untouched — it wasn't
	// leased because it was never claimed.
	var stillDue bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT next_due_at <= now() FROM app.sync_subscription WHERE subscription_id = $1`, blockedSub,
	).Scan(&stillDue))
	require.True(t, stillDue)
}

// TestClaimIsAtomicUnderConcurrency (roadmap exit criterion): FOR UPDATE
// SKIP LOCKED with N concurrent claimers against the same due-work pool
// yields zero double-claims. All N goroutines share one Planner/leader —
// this test targets the SQL-level guarantee directly, which is what has to
// hold regardless of how many planner processes end up racing the claim
// query at once.
func TestClaimIsAtomicUnderConcurrency(t *testing.T) {
	pool, connStr := newMigratedPool(t)
	ctx := context.Background()

	route := seedRoute(t, pool, "", false)
	const subscriptionCount = 60
	now := time.Now()
	for i := 0; i < subscriptionCount; i++ {
		seedDueSubscription(t, pool, route, int64(2000+i), now.Add(-time.Minute))
	}

	p := newTestPlanner(t, pool, connStr, Config{ClaimBatchSize: 10, ClaimLease: 10 * time.Minute})

	const concurrency = 8
	var mu sync.Mutex
	seen := make(map[uuid.UUID]int)
	var wg sync.WaitGroup
	for g := 0; g < concurrency; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine keeps claiming until it sees an empty result a
			// few times in a row, so the pool of due work is fully drained
			// even though FOR UPDATE SKIP LOCKED means any single call may
			// legitimately return fewer rows than are actually still due.
			emptyStreak := 0
			for emptyStreak < 3 {
				result, err := p.claimOnce(ctx)
				require.NoError(t, err)
				if result.Claimed() == 0 {
					emptyStreak++
					continue
				}
				emptyStreak = 0
				mu.Lock()
				for _, id := range result.SubscriptionIDs {
					seen[id]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Len(t, seen, subscriptionCount, "every seeded subscription must be claimed exactly once across all goroutines")
	for id, count := range seen {
		require.Equalf(t, 1, count, "subscription %s was claimed %d times — FOR UPDATE SKIP LOCKED double-claim", id, count)
	}
}

// TestPlannerSoakNoDuplicateJobs (roadmap exit criterion): sustained
// operation creates zero duplicate jobs. A literal 30-minute-wall-clock run
// at the production 5s claim interval would produce on the order of 360
// claim ticks total across the whole subscription set — not a practical
// thing to block CI on. This compresses the SAME invariant into a few
// real-time seconds by shrinking ClaimInterval and, deliberately,
// ClaimLease far below what a real deployment would ever use: a tight
// lease forces the same still-active subscription back into "due" on
// nearly every tick, so a handful of subscriptions here accumulate far
// more re-claim *attempts* than 30 minutes at 5s/subscription would ever
// generate — which is the actual thing worth stress-testing (does the
// lease genuinely prevent a reclaim before it expires, and does River's
// unique-job option cleanly absorb the case where it doesn't), not the
// wall-clock figure itself.
func TestPlannerSoakNoDuplicateJobs(t *testing.T) {
	pool, connStr := newMigratedPool(t)
	ctx := context.Background()

	route := seedRoute(t, pool, "", false)
	const subscriptionCount = 12
	now := time.Now()
	for i := 0; i < subscriptionCount; i++ {
		seedDueSubscription(t, pool, route, int64(3000+i), now.Add(-time.Minute))
	}

	const (
		claimInterval = 15 * time.Millisecond
		claimLease    = 40 * time.Millisecond // deliberately tight: see doc comment above
		soakDuration  = 3 * time.Second
	)

	p := newTestPlanner(t, pool, connStr, Config{
		ClaimInterval:  claimInterval,
		ClaimBatchSize: subscriptionCount,
		ClaimLease:     claimLease,
	})

	var mu sync.Mutex
	lastClaimAt := make(map[uuid.UUID]time.Time, subscriptionCount)
	violations := make([]string, 0)
	p.OnClaim = func(result ClaimResult) {
		now := time.Now()
		mu.Lock()
		defer mu.Unlock()
		for _, id := range result.SubscriptionIDs {
			if prev, ok := lastClaimAt[id]; ok {
				if gap := now.Sub(prev); gap < claimLease-5*time.Millisecond { // small scheduling slack
					violations = append(violations, fmt.Sprintf("subscription %s reclaimed after %s, less than the %s lease", id, gap, claimLease))
				}
			}
			lastClaimAt[id] = now
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, soakDuration)
	defer cancel()
	_ = p.Run(runCtx)

	mu.Lock()
	require.Empty(t, violations, "the claim lease must hold: no subscription may be reclaimed before its lease expires")
	mu.Unlock()

	// End-to-end sanity check matching the roadmap's literal wording: no
	// two non-terminal river_job rows ever exist for the same unique key
	// (route_id, entity_kind, entity_id) at once. River's UniqueOpts
	// default ByState already guarantees this at the database level; this
	// assertion is the outcome-level proof that Phase 6's own claim/lease
	// path never even attempts to violate it under sustained, adversarial-
	// timing load.
	rows, err := pool.Query(ctx, `
		SELECT args->>'route_id', args->>'entity_kind', args->>'entity_id', count(*)
		  FROM river_job
		 WHERE kind = $1 AND state NOT IN ('cancelled', 'discarded')
		 GROUP BY 1, 2, 3
		HAVING count(*) > 1
	`, KindSyncRoute)
	require.NoError(t, err)
	defer rows.Close()
	var dupes []string
	for rows.Next() {
		var routeID, entityKind, entityID string
		var count int
		require.NoError(t, rows.Scan(&routeID, &entityKind, &entityID, &count))
		dupes = append(dupes, fmt.Sprintf("route=%s kind=%s entity=%s count=%d", routeID, entityKind, entityID, count))
	}
	require.NoError(t, rows.Err())
	require.Empty(t, dupes, "zero duplicate active sync_route jobs must exist after the soak")
}
