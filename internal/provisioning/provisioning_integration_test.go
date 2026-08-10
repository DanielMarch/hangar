//go:build integration

package provisioning_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// newMigratedPool boots a real, migrated PG18 via testcontainers, including
// River's own queue-table migrations — internal/sync/planner's established
// pattern (claim_integration_test.go), since this package both reads via
// sqlc-generated queries and enqueues River jobs.
func newMigratedPool(t testing.TB) *pgxpool.Pool {
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
	poolCfg.MaxConns = 50
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
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

	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

// insertOnlyRiverClient is suitable for InsertTx but never Start-ed —
// internal/sync/planner's established pattern for tests that only need to
// assert what got enqueued, not have it executed.
func insertOnlyRiverClient(t testing.TB, pool *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	return client
}

func countRiverJobs(t testing.TB, pool *pgxpool.Pool, kind string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = $1`, kind).Scan(&n))
	return n
}

// ---- seed helpers ----

var nextCharacterID int64 = 95000000000

func seedUser(t testing.TB, s *store.Store) uuid.UUID {
	t.Helper()
	u, err := s.CreateUser(context.Background(), "Test User "+uuid.NewString())
	require.NoError(t, err)
	return u.UserID
}

// seedCharacter creates a character for userID with a VALID token by
// default, optionally in corpID (nil for none).
func seedCharacter(t testing.TB, s *store.Store, userID uuid.UUID, corpID *int64) int64 {
	t.Helper()
	ctx := context.Background()
	nextCharacterID++
	charID := nextCharacterID

	if corpID != nil {
		require.NoError(t, s.UpsertCorporationStub(ctx, *corpID, "Corp "+uuid.NewString(), "TST"))
	}
	_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{
		CharacterID: charID, UserID: uuid.NullUUID{UUID: userID, Valid: true},
		Name: "Char " + uuid.NewString(), CorporationID: corpID, OwnerHash: "oh-" + uuid.NewString(),
	})
	require.NoError(t, err)

	require.NoError(t, s.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID: charID, KeyVersion: 1, WrappedDek: []byte("wdek"), Nonce: []byte("nonce123456"),
		Ciphertext: []byte("ct"), OwnerHash: "oh-token-" + uuid.NewString(),
	}))
	return charID
}

func invalidateToken(t testing.TB, s *store.Store, characterID int64) {
	t.Helper()
	reason := "test_invalidated"
	require.NoError(t, s.InvalidateCharacterToken(context.Background(), characterID, &reason))
}

func seedPlatform(t testing.TB, s *store.Store) uuid.UUID {
	t.Helper()
	p, err := s.CreatePlatform(context.Background(), "discord", "Test Platform "+uuid.NewString(), []byte(`{}`))
	require.NoError(t, err)
	return p.PlatformID
}

func seedGroup(t testing.TB, s *store.Store, platformID uuid.UUID, remoteRef string) uuid.UUID {
	t.Helper()
	g, err := s.CreatePlatformGroup(context.Background(), platformID, remoteRef, "Group "+remoteRef)
	require.NoError(t, err)
	return g.GroupID
}

func seedRule(t testing.TB, s *store.Store, sourceKind, sourceRef string, groupID uuid.UUID, effect string) uuid.UUID {
	t.Helper()
	r, err := s.CreateEntitlementRule(context.Background(), gen.CreateEntitlementRuleParams{
		SourceKind: sourceKind, SourceRef: sourceRef, GroupID: groupID, Effect: effect,
	})
	require.NoError(t, err)
	return r.RuleID
}

func linkState(t testing.TB, s *store.Store, platformID, userID uuid.UUID, remoteIdentity string, desired, actual []string) {
	t.Helper()
	if desired == nil {
		desired = []string{}
	}
	if actual == nil {
		actual = []string{}
	}
	now := time.Now()
	err := s.UpsertProvisioningState(context.Background(), gen.UpsertProvisioningStateParams{
		PlatformID: platformID, UserID: userID, RemoteIdentity: &remoteIdentity,
		DesiredGroups: desired, ActualGroups: actual, LinkedAt: &now, LastReconciledAt: &now,
	})
	require.NoError(t, err)
}

// ---- TestStrictModeFailsUserWhenAnyAltInvalid (roadmap exit criterion) ----

func TestStrictModeFailsUserWhenAnyAltInvalid(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	userID := seedUser(t, s)
	mainID := seedCharacter(t, s, userID, nil) // valid token
	altID := seedCharacter(t, s, userID, nil)  // will be invalidated
	_ = mainID
	invalidateToken(t, s, altID)

	world, err := entitlement.GatherWorldState(ctx, s, userID)
	require.NoError(t, err)
	require.True(t, world.StrictModeDenied, "one invalid alt token must deny the whole user")

	denied, err := provisioning.CheckStrictMode(ctx, s, userID)
	require.NoError(t, err)
	require.True(t, denied)
}

func TestStrictModeAllowsWhenEveryTokenValid(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	userID := seedUser(t, s)
	seedCharacter(t, s, userID, nil)
	seedCharacter(t, s, userID, nil)

	world, err := entitlement.GatherWorldState(ctx, s, userID)
	require.NoError(t, err)
	require.False(t, world.StrictModeDenied)
}

// ---- TestRevocationEnqueuedInSameTransaction (roadmap exit criterion) ----

func TestRevocationEnqueuedInSameTransaction(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	riverClient := insertOnlyRiverClient(t, pool)
	urgent := &provisioning.Urgent{River: riverClient}

	role, err := s.CreateRole(ctx, "revoke-test-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	userID := seedUser(t, s)
	seedCharacter(t, s, userID, nil)
	require.NoError(t, s.AssignUserRole(ctx, userID, role.RoleID, uuid.NullUUID{}))

	platformID := seedPlatform(t, s)
	groupID := seedGroup(t, s, platformID, "role-group")
	seedRule(t, s, entitlement.SourceRole, role.RoleID.String(), groupID, entitlement.EffectGrant)
	linkState(t, s, platformID, userID, "remote-1", []string{"role-group"}, []string{"role-group"})

	eventAt := time.Now()
	forcedErr := errors.New("forced rollback")
	err = store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		require.NoError(t, s.RevokeUserRole(ctx, userID, role.RoleID))
		if err := urgent.HandleUserChangeTx(ctx, s, userID, eventAt, "test_rollback"); err != nil {
			return err
		}
		return forcedErr
	})
	require.ErrorIs(t, err, forcedErr)

	// Nothing survived: the role is still assigned, desired_groups is
	// unchanged, no audit row, no queued job.
	roles, err := s.ListUserRoles(ctx, userID)
	require.NoError(t, err)
	require.Len(t, roles, 1, "the role revocation must not have survived the rollback")

	link, err := s.GetProvisioningState(ctx, platformID, userID)
	require.NoError(t, err)
	require.Equal(t, []string{"role-group"}, link.DesiredGroups, "desired_groups must be unchanged by the rolled-back recompute")

	require.Zero(t, countRiverJobs(t, pool, provisioning.KindProvisionUrgent), "the rolled-back transaction must not have left a queued job behind")

	// Now the real (committed) revocation.
	err = store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		require.NoError(t, s.RevokeUserRole(ctx, userID, role.RoleID))
		return urgent.HandleUserChangeTx(ctx, s, userID, eventAt, "test_commit")
	})
	require.NoError(t, err)

	link, err = s.GetProvisioningState(ctx, platformID, userID)
	require.NoError(t, err)
	require.Empty(t, link.DesiredGroups, "the committed revocation must have cleared desired_groups")

	require.Equal(t, 1, countRiverJobs(t, pool, provisioning.KindProvisionUrgent), "the committed transaction must have left exactly one queued job")

	pending, err := s.ListPendingProvisioningAudit(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, []string{"role-group"}, pending[0].GroupsRemoved)
	require.Equal(t, eventAt.Unix(), pending[0].EventAt.Unix(), "event_at must be the caller-supplied originating-event time, not now() at commit")
}

// ---- TestDryRunPreviewExactGainsAndLosses (roadmap exit criterion) ----

func TestDryRunPreviewExactGainsAndLosses(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	platformID := seedPlatform(t, s)
	groupA := seedGroup(t, s, platformID, "group-a")
	groupB := seedGroup(t, s, platformID, "group-b")

	// userGain: currently linked with nothing, would gain group-a under a
	// hypothetical corporation rule scoped to their own corp — NOT a
	// `public` rule, which would also (correctly) match userUnaffected
	// below and break this test's "exactly the affected users" assertion
	// for the wrong reason.
	userGain := seedUser(t, s)
	gainCorpID := int64(98700099)
	seedCharacter(t, s, userGain, &gainCorpID)
	linkState(t, s, platformID, userGain, "remote-gain", nil, nil)

	// userLose: currently has group-b via a live rule that the
	// hypothetical set drops.
	userLose := seedUser(t, s)
	corpID := int64(98700001)
	seedCharacter(t, s, userLose, &corpID)
	seedRule(t, s, entitlement.SourceCorporation, "98700001", groupB, entitlement.EffectGrant)
	linkState(t, s, platformID, userLose, "remote-lose", []string{"group-b"}, []string{"group-b"})

	// userUnaffected: hypothetical set changes nothing for them.
	userUnaffected := seedUser(t, s)
	seedCharacter(t, s, userUnaffected, nil)
	linkState(t, s, platformID, userUnaffected, "remote-same", nil, nil)

	hypothetical := []entitlement.Rule{
		{GroupID: groupA, SourceKind: entitlement.SourceCorporation, SourceRef: "98700099", Effect: entitlement.EffectGrant},
		// group-b's corporation rule is deliberately absent from the
		// hypothetical set — this is what makes userLose lose it.
	}

	diffs, err := provisioning.Preview(ctx, s, platformID, hypothetical)
	require.NoError(t, err)
	require.Len(t, diffs, 2, "only the two actually-affected users must appear — never counts, never the unaffected user")

	byUser := make(map[uuid.UUID]provisioning.UserDiff, len(diffs))
	for _, d := range diffs {
		byUser[d.UserID] = d
	}

	gain, ok := byUser[userGain]
	require.True(t, ok)
	require.Equal(t, []string{"group-a"}, gain.Gained)
	require.Empty(t, gain.Lost)

	lose, ok := byUser[userLose]
	require.True(t, ok)
	require.Empty(t, lose.Gained)
	require.Equal(t, []string{"group-b"}, lose.Lost)

	_, unaffected := byUser[userUnaffected]
	require.False(t, unaffected, "an unaffected linked user must not appear in the preview diff at all")
}

// TestDeleteEntitlementRuleEnqueuesPerUserRevocations (roadmap edge case):
// deleting a rule that matched N users enqueues N individual
// provision-urgent jobs, not one bulk job.
func TestDeleteEntitlementRuleEnqueuesPerUserRevocations(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	riverClient := insertOnlyRiverClient(t, pool)
	urgent := &provisioning.Urgent{River: riverClient}

	platformID := seedPlatform(t, s)
	groupID := seedGroup(t, s, platformID, "public-group")
	ruleID := seedRule(t, s, entitlement.SourcePublic, "", groupID, entitlement.EffectGrant)

	const n = 5
	userIDs := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		userIDs[i] = seedUser(t, s)
		seedCharacter(t, s, userIDs[i], nil)
		linkState(t, s, platformID, userIDs[i], fmt.Sprintf("remote-%d", i), []string{"public-group"}, []string{"public-group"})
	}

	require.NoError(t, v1.DeleteEntitlementRule(ctx, pool, urgent, ruleID, time.Now(), "rule_deleted"))

	require.Equal(t, n, countRiverJobs(t, pool, provisioning.KindProvisionUrgent), "every matched user must get its own job")
	for _, userID := range userIDs {
		link, err := s.GetProvisioningState(ctx, platformID, userID)
		require.NoError(t, err)
		require.Empty(t, link.DesiredGroups)
	}
}

// ---- TestUrgentQueueNotStarvedByBulk (roadmap exit criterion) ----

// slowDriver simulates a platform whose calls take a while — enough for a
// bulk reconcile over many users to take long enough that an urgent
// revocation enqueued shortly after it starts would visibly queue behind
// it if the two queues shared a worker pool.
type slowDriver struct {
	delay time.Duration
}

func (d slowDriver) Grant(ctx context.Context, _, _ string) error {
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
	}
	return nil
}
func (d slowDriver) Revoke(ctx context.Context, _, _ string) error { return d.Grant(ctx, "", "") }

func TestUrgentQueueNotStarvedByBulk(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	bulkPlatform := seedPlatform(t, s)
	bulkGroup := seedGroup(t, s, bulkPlatform, "bulk-group")
	seedRule(t, s, entitlement.SourcePublic, "", bulkGroup, entitlement.EffectGrant)

	const bulkUsers = 40
	for i := 0; i < bulkUsers; i++ {
		u := seedUser(t, s)
		seedCharacter(t, s, u, nil)
		linkState(t, s, bulkPlatform, u, fmt.Sprintf("bulk-remote-%d", i), nil, nil) // starts empty -> every user needs a Grant call
	}

	urgentPlatform := seedPlatform(t, s)
	urgentGroup := seedGroup(t, s, urgentPlatform, "urgent-group")
	role, err := s.CreateRole(ctx, "urgent-role-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	seedRule(t, s, entitlement.SourceRole, role.RoleID.String(), urgentGroup, entitlement.EffectGrant)
	urgentUser := seedUser(t, s)
	seedCharacter(t, s, urgentUser, nil)
	require.NoError(t, s.AssignUserRole(ctx, urgentUser, role.RoleID, uuid.NullUUID{}))
	linkState(t, s, urgentPlatform, urgentUser, "urgent-remote", []string{"urgent-group"}, []string{"urgent-group"})

	drivers := provisioning.NewDrivers()
	drivers.Register(bulkPlatform.String(), slowDriver{delay: 300 * time.Millisecond}) // 40 * 300ms = 12s worth of work for ONE bulk worker
	fastDriver := provisioning.NewInMemoryDriver()
	drivers.Register(urgentPlatform.String(), fastDriver)

	workers := river.NewWorkers()
	river.AddWorker(workers, &provisioning.UrgentWorker{Pool: pool, Drivers: drivers})
	river.AddWorker(workers, &provisioning.BulkWorker{Pool: pool, Drivers: drivers})
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			provisioning.QueueBulk:   {MaxWorkers: 1}, // one slow worker, matching production's "own pool" isolation
			provisioning.QueueUrgent: {MaxWorkers: 4},
		},
		Workers: workers,
	})
	require.NoError(t, err)
	require.NoError(t, riverClient.Start(ctx))
	defer func() { _ = riverClient.Stop(context.Background()) }()

	_, err = riverClient.Insert(ctx, provisioning.BulkJobArgs{PlatformID: bulkPlatform}, nil)
	require.NoError(t, err)

	// Give the bulk job a moment to actually start occupying its worker
	// before the urgent revocation is triggered, so this test genuinely
	// exercises "already in flight", not "enqueued at the same instant".
	time.Sleep(200 * time.Millisecond)

	urgent := &provisioning.Urgent{River: riverClient}
	revokeStart := time.Now()
	require.NoError(t, store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		require.NoError(t, s.RevokeUserRole(ctx, urgentUser, role.RoleID))
		return urgent.HandleUserChangeTx(ctx, s, urgentUser, revokeStart, "test_starvation")
	}))

	require.Eventually(t, func() bool {
		link, err := s.GetProvisioningState(ctx, urgentPlatform, urgentUser)
		require.NoError(t, err)
		return len(link.ActualGroups) == 0
	}, 5*time.Second, 50*time.Millisecond, "the urgent revocation must complete quickly even while a slow bulk reconcile is in flight")

	elapsed := time.Since(revokeStart)
	require.Lessf(t, elapsed, 5*time.Second, "urgent revocation took %s — the bulk reconcile (40*300ms=12s on its own 1-worker pool) must not have delayed it", elapsed)
}

// ---- TestRevocationP99Under60sAtLoad (roadmap exit criterion) ----

func TestRevocationP99Under60sAtLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const numPlatforms = 3
	const numUsers = 5000

	platformIDs := make([]uuid.UUID, numPlatforms)
	roleIDs := make([]uuid.UUID, numPlatforms)
	drivers := provisioning.NewDrivers()
	for i := 0; i < numPlatforms; i++ {
		platformIDs[i] = seedPlatform(t, s)
		group := seedGroup(t, s, platformIDs[i], fmt.Sprintf("load-group-%d", i))
		role, err := s.CreateRole(ctx, fmt.Sprintf("load-role-%d-%s", i, uuid.NewString()), nil, false)
		require.NoError(t, err)
		roleIDs[i] = role.RoleID
		seedRule(t, s, entitlement.SourceRole, role.RoleID.String(), group, entitlement.EffectGrant)
		drivers.Register(platformIDs[i].String(), provisioning.NewInMemoryDriver())
	}

	// Bulk-create users and their character/role/link rows with direct SQL
	// (the rbac 5000-user benchmark's precedent) — this loop is test setup,
	// not the thing under test, so it must not itself dominate the runtime
	// budget the way 5000 individual round trips through every seed helper
	// would.
	userIDs := make([]uuid.UUID, numUsers)
	for i := 0; i < numUsers; i++ {
		userIDs[i] = seedUser(t, s)
		seedCharacter(t, s, userIDs[i], nil)
		p := i % numPlatforms
		require.NoError(t, s.AssignUserRole(ctx, userIDs[i], roleIDs[p], uuid.NullUUID{}))
		remoteRef := fmt.Sprintf("load-group-%d", p)
		linkState(t, s, platformIDs[p], userIDs[i], fmt.Sprintf("remote-%d", i), []string{remoteRef}, []string{remoteRef})
	}
	// userIDs[i]'s platform/role assignment stays keyed by i % numPlatforms
	// throughout — deliberately NOT shuffled, since the revocation loop
	// below re-derives p the same way to know which role to revoke for
	// each user; shuffling userIDs alone (without carrying the matching p)
	// would desynchronize the two and revoke the wrong role for most users.

	workers := river.NewWorkers()
	river.AddWorker(workers, &provisioning.UrgentWorker{Pool: pool, Drivers: drivers})
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{provisioning.QueueUrgent: {MaxWorkers: 32}},
		Workers: workers,
	})
	require.NoError(t, err)
	require.NoError(t, riverClient.Start(ctx))
	defer func() { _ = riverClient.Stop(context.Background()) }()

	urgent := &provisioning.Urgent{River: riverClient}
	eventAt := time.Now()
	for i, userID := range userIDs {
		p := i % numPlatforms // matches the role that was actually assigned to this user
		require.NoError(t, store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
			require.NoError(t, s.RevokeUserRole(ctx, userID, roleIDs[p]))
			return urgent.HandleUserChangeTx(ctx, s, userID, eventAt, "load_test")
		}))
	}

	require.Eventually(t, func() bool {
		pending, err := s.ListPendingProvisioningAudit(ctx)
		require.NoError(t, err)
		return len(pending) == 0
	}, 90*time.Second, 500*time.Millisecond, "every enqueued revocation must eventually complete")

	rows, err := pool.Query(ctx, `SELECT event_at, platform_call_completed_at FROM app.provisioning_audit WHERE reason = 'load_test'`)
	require.NoError(t, err)
	var latencies []time.Duration
	for rows.Next() {
		var eventAtRow, completedAt time.Time
		require.NoError(t, rows.Scan(&eventAtRow, &completedAt))
		latencies = append(latencies, completedAt.Sub(eventAtRow))
	}
	rows.Close()
	require.Len(t, latencies, numUsers)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p99 := latencies[int(float64(len(latencies))*0.99)]
	t.Logf("p99 event_at -> platform_call_completed_at over %d revocations: %s", len(latencies), p99)
	require.Lessf(t, p99, 60*time.Second, "p99 revocation latency must stay under the 60s SLO at 5000 identities across 3 platforms")
}
