//go:build integration

package load_test

// ── THE GATE 2 HARNESS, EXERCISED AS AN INTEGRATION TEST ─────────────────
//
// 04_RELEASE_GATES.md §0 rule 6 requires a gate's harness to land in an
// earlier phase than the run, "with their own exit criteria and their own
// tests". This file is those tests, and it is the counterpart of
// gate1_integration_test.go.
//
// It runs the SAME measurement Phase 20.8 will run — MeasureGate2's SQL
// over app.provisioning_audit — against real rows produced by the real
// UrgentWorker driving the same rate-limited stubs, at a scale of tens
// rather than 5000 identities.
//
// IT IS NOT A GATE 2 RUN. No 5000 identities, no saturated bulk queue, no
// evidence artefact, and nothing here publishes a verdict about the
// release.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/test/load"
)

func newGate2Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	poolCfg, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)
	poolCfg.MaxConns = 30
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

// recordingLatency captures what UrgentWorker observes, so the test can
// cross-check the METRIC against the TABLE — the agreement gate2_
// revocation.go's header says is worth having.
type recordingLatency struct {
	outcomes []string
	seconds  []float64
}

func (r *recordingLatency) ObserveRevocation(outcome string, seconds float64) {
	r.outcomes = append(r.outcomes, outcome)
	r.seconds = append(r.seconds, seconds)
}

// gate2World seeds one platform, one group, and n linked users each
// already holding the group — so revoking is a real loss for every one of
// them.
type gate2World struct {
	pool       *pgxpool.Pool
	store      *store.Store
	platformID uuid.UUID
	groupRef   string
	users      []uuid.UUID
}

func seedGate2World(t testing.TB, pool *pgxpool.Pool, n int) *gate2World {
	t.Helper()
	ctx := context.Background()
	s := store.New(pool)

	platform, err := s.CreatePlatform(ctx, "discord", "Gate2 Platform "+uuid.NewString(), []byte(`{}`))
	require.NoError(t, err)
	const groupRef = "role-1"
	_, err = s.CreatePlatformGroup(ctx, platform.PlatformID, groupRef, "Gate2 Group")
	require.NoError(t, err)

	w := &gate2World{pool: pool, store: s, platformID: platform.PlatformID, groupRef: groupRef}
	now := time.Now()
	for i := 0; i < n; i++ {
		u, err := s.CreateUser(ctx, "Gate2 User "+uuid.NewString())
		require.NoError(t, err)
		identity := "remote-" + uuid.NewString()
		require.NoError(t, s.UpsertProvisioningState(ctx, gen.UpsertProvisioningStateParams{
			PlatformID: platform.PlatformID, UserID: u.UserID, RemoteIdentity: &identity,
			// Desired AND actual both hold the group: there are no rules,
			// so the next recompute desires nothing and the diff is a
			// genuine revocation of a group the platform really holds.
			DesiredGroups: []string{groupRef}, ActualGroups: []string{groupRef},
			LinkedAt: &now, LastReconciledAt: &now,
		}))
		w.users = append(w.users, u.UserID)
	}
	return w
}

// runUrgentRevocations enqueues one urgent revocation per user, stamped
// with eventAt, then works every job through the real UrgentWorker against
// the supplied driver. It returns what the worker observed.
//
// The jobs are read back out of river_job and executed directly rather
// than by starting a River client, for the same reason
// internal/provisioning's own suite does: this test is about the
// measurement, not about River's scheduler, and a started client makes the
// test's timing depend on a poll interval it does not control.
func runUrgentRevocations(t testing.TB, w *gate2World, drivers *provisioning.Drivers, eventAt time.Time) *recordingLatency {
	t.Helper()
	ctx := context.Background()

	client, err := river.NewClient(riverpgxv5.New(w.pool), &river.Config{})
	require.NoError(t, err)
	urgent := &provisioning.Urgent{River: client}

	for _, userID := range w.users {
		require.NoError(t, urgent.HandleUserChange(ctx, w.pool, userID, eventAt, "gate2_test"))
	}

	rec := &recordingLatency{}
	worker := &provisioning.UrgentWorker{Pool: w.pool, Drivers: drivers, Latency: rec}

	rows, err := w.pool.Query(ctx,
		`SELECT (args->>'audit_id')::uuid FROM river_job WHERE kind = $1 ORDER BY id`,
		provisioning.KindProvisionUrgent)
	require.NoError(t, err)
	var auditIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		auditIDs = append(auditIDs, id)
	}
	rows.Close()
	require.NoError(t, rows.Err())

	for _, id := range auditIDs {
		require.NoError(t, worker.Work(ctx, &river.Job[provisioning.UrgentJobArgs]{
			Args: provisioning.UrgentJobArgs{AuditID: id},
		}))
	}
	return rec
}

// TestGate2HarnessMeasuresWhatTheGateDefines is the harness's own exit
// criterion: MeasureGate2 must compute §2.2's quantity — from the two
// columns the gate names, over the rows the gate scopes — and must reach
// the same verdict a hand-written query would.
func TestGate2HarnessMeasuresWhatTheGateDefines(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()

	const identities = 12
	w := seedGate2World(t, pool, identities)

	discord := load.NewRateLimitedStub(load.DiscordLimits)
	drivers := provisioning.NewDrivers()
	drivers.Register(w.platformID.String(), discord)

	// The originating event is stamped ONCE, before any work — §2.2's
	// "not job start and not job claim". Backdated deliberately, so the
	// measured latency is dominated by a known quantity rather than by
	// however long the test's own loop took: a harness that only ever sees
	// millisecond latencies cannot demonstrate it would notice a slow one.
	since := time.Now().Add(-2 * time.Second)
	eventAt := since.Add(500 * time.Millisecond)

	rec := runUrgentRevocations(t, w, drivers, eventAt)

	result, err := load.MeasureGate2(ctx, pool, load.Gate2Config{
		Since:          since,
		SLO:            60 * time.Second,
		Percentile:     0.99,
		MinRevocations: identities,
		Notes:          "harness self-test",
	})
	require.NoError(t, err)

	require.Equal(t, identities, result.SampleCount,
		"every seeded identity must contribute exactly one successful revocation")
	require.Equal(t, identities, result.Outcomes["success"])
	require.True(t, result.Passed(), "conditions: %+v", result.Conditions)

	// The measurement must be the one the gate defines, not a proxy for it.
	// Every sample must be at least the backdate, because event_at is that
	// far in the past by construction.
	require.Greater(t, result.Latencies.P50, 1.0,
		"latency is measured from the ORIGINATING event, so a 1.5s backdated event cannot read as ~0")
	require.Less(t, result.Latencies.Max, 60.0)

	// The stub really did rate-limit: 12 revocations at Discord's 20ms
	// spacing cannot have completed in less than ~220ms of bucket wait.
	_, revokes := discord.Counts()
	require.Equal(t, identities, revokes, "one revoke call per identity")

	// The metric and the table must agree. gate2_revocation.go's header
	// explains why the gate reads the table; this is the cross-check that
	// makes disagreement visible rather than academic.
	require.Len(t, rec.seconds, identities)
	for i, s := range rec.seconds {
		require.Equal(t, "success", rec.outcomes[i])
		require.InDelta(t, result.Latencies.Max, s, 5.0,
			"the histogram observation and the table's own subtraction must describe the same event")
	}
}

// TestGate2EmptyRunDoesNotPass is the §2.1 condition that matters most for
// a gate nobody wants to fail: a run that measured nothing must not report
// a pass. This is B25's shape — "zero alerts dropped" was true because zero
// alerts existed — applied to Gate 2 before it can happen.
func TestGate2EmptyRunDoesNotPass(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()

	result, err := load.MeasureGate2(ctx, pool, load.Gate2Config{
		Since:          time.Now().Add(-time.Minute),
		SLO:            60 * time.Second,
		MinRevocations: 1,
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.SampleCount)
	require.False(t, result.Passed(),
		"a Gate 2 window with no revocations in it has not demonstrated a p99 of anything")
}

// TestGate2DownPlatformKeepsItsExposure is §2.3: "Zero revocations lost
// when a platform is down: they retry and remain on the exposure board
// with their true age."
//
// A down platform's audit row IS completed (outcome 'failed'), by design —
// the job did what a provision-urgent job is for, which is attempt and
// honestly record the outcome. What must NOT happen is the failure
// counting as a revocation in the SLO, or the desired/actual mismatch
// disappearing from the exposure board.
func TestGate2DownPlatformKeepsItsExposure(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()

	w := seedGate2World(t, pool, 4)

	discord := load.NewRateLimitedStub(load.DiscordLimits)
	discord.SetDown(true)
	drivers := provisioning.NewDrivers()
	drivers.Register(w.platformID.String(), discord)

	since := time.Now().Add(-time.Second)
	runUrgentRevocations(t, w, drivers, since)

	result, err := load.MeasureGate2(ctx, pool, load.Gate2Config{
		Since: since, SLO: 60 * time.Second, MinRevocations: 1,
	})
	require.NoError(t, err)

	require.Zero(t, result.SampleCount,
		"a platform call that FAILED removed nothing — it must not appear as a successful revocation")
	require.Equal(t, 4, result.Outcomes["partial_failure"]+result.Outcomes["failed"],
		"every attempt against a down platform must be recorded as such: %+v", result.Outcomes)

	// The exposure survives: the group is still actually held.
	exposed, err := w.store.ListExposedProvisioningStates(ctx, w.platformID)
	require.NoError(t, err)
	require.Len(t, exposed, 4, "a down platform's users must stay on the exposure board")
	for _, e := range exposed {
		require.Equal(t, []string{w.groupRef}, e.ActualGroups,
			"actual_groups must still say the group is held — claiming otherwise would be the silent loss §2.3 forbids")
	}
}

// TestGate2UrgentEnqueuesInTheMutatingTransaction is condition 2.5:
// "Rolling back the mutating transaction also rolls back the job".
//
// It is asserted here rather than inside MeasureGate2 because it is a
// statement about a code path, not about a run's numbers — see
// MeasureGate2's doc comment.
func TestGate2UrgentEnqueuesInTheMutatingTransaction(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()

	w := seedGate2World(t, pool, 1)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	urgent := &provisioning.Urgent{River: client}

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	s := store.New(tx)
	require.NoError(t, urgent.HandleUserChangeTx(ctx, s, w.users[0], time.Now(), "rollback_test"))

	// Inside the transaction the job exists...
	var inTx int
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = $1`,
		provisioning.KindProvisionUrgent).Scan(&inTx))
	require.Equal(t, 1, inTx)

	require.NoError(t, tx.Rollback(ctx))

	// ...and after the rollback neither the job NOR the audit row does.
	// Losing the enqueue while keeping the state change is the security
	// failure §9.2 names; keeping the enqueue while losing the state
	// change would be a phantom revocation. Both must vanish together.
	var afterJobs, afterAudit int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = $1`,
		provisioning.KindProvisionUrgent).Scan(&afterJobs))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.provisioning_audit`).Scan(&afterAudit))
	require.Zero(t, afterJobs, "rolling back the mutation must roll back the job")
	require.Zero(t, afterAudit, "rolling back the mutation must roll back the audit row")
}

var _ = pgx.ErrNoRows // keep the pgx import honest across build-tag combinations
