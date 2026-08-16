package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// fakeStore is an in-memory implementation of Store, used to exercise
// flush.go's two mode-transition directions without a real Postgres —
// TestModeTransitionLosesNoEntries only needs to prove the flush logic
// itself preserves the live-cost sum, which doesn't depend on the SQL
// engine. The clustered-mode SQL semantics under real contention are
// separately proven by shared_integration_test.go against a live PG18.
type fakeStore struct {
	mu      sync.Mutex
	buckets map[string]gen.AppEsiLedgerBucket
	entries map[uuid.UUID]gen.AppEsiLedgerEntry
}

func newFakeStore() *fakeStore {
	return &fakeStore{buckets: map[string]gen.AppEsiLedgerBucket{}, entries: map[uuid.UUID]gen.AppEsiLedgerEntry{}}
}

func (s *fakeStore) UpsertLedgerBucket(ctx context.Context, arg gen.UpsertLedgerBucketParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets[bucketKey(arg.RateLimitGroup, arg.UserKey)] = gen.AppEsiLedgerBucket{
		RateLimitGroup: arg.RateLimitGroup, UserKey: arg.UserKey, MaxTokens: arg.MaxTokens, Window: arg.Window,
	}
	return nil
}

func (s *fakeStore) GetLedgerBucketForUpdate(ctx context.Context, group, userKey string) (gen.GetLedgerBucketForUpdateRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[bucketKey(group, userKey)]
	if !ok {
		return gen.GetLedgerBucketForUpdateRow{}, pgx.ErrNoRows
	}
	return gen.GetLedgerBucketForUpdateRow{MaxTokens: b.MaxTokens, Window: b.Window}, nil
}

func (s *fakeStore) ExpireLedgerReservations(ctx context.Context, group, userKey string) ([]gen.AppEsiLedgerEntry, error) {
	return nil, nil // not exercised by the flush-only tests in this file
}

func (s *fakeStore) EvictAgedLedgerEntries(ctx context.Context, group, userKey string, window time.Duration) error {
	return nil
}

// SumSettledLedgerEntryCost implements Store. It excludes reservations, as
// the real query does — reconciliation compares against what the SERVER can
// have counted, and an in-flight request is not that (Phase 20.2).
func (s *fakeStore) SumSettledLedgerEntryCost(ctx context.Context, group, userKey string) (int64, error) {
	return s.sumCosts(group, userKey, false), nil
}

// sumCosts is the test-only total, with a switch for whether reservations
// count. The flush tests assert on the ALL-inclusive total, because a flush
// must carry an in-flight reservation across a mode transition intact.
func (s *fakeStore) sumCosts(group, userKey string, includeReserved bool) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, e := range s.entries {
		if e.RateLimitGroup != group || e.UserKey != userKey {
			continue
		}
		if !includeReserved && e.State == "reserved" {
			continue
		}
		total += int64(e.Cost)
	}
	return total
}

func (s *fakeStore) GetOldestLiveLedgerEntry(ctx context.Context, group, userKey string) (time.Time, error) {
	return time.Time{}, pgx.ErrNoRows
}

func (s *fakeStore) ReserveLedgerEntry(ctx context.Context, group, userKey string, requestTimeout time.Duration) (gen.AppEsiLedgerEntry, error) {
	return gen.AppEsiLedgerEntry{}, pgx.ErrNoRows
}

func (s *fakeStore) SettleLedgerEntry(ctx context.Context, entryID uuid.UUID, cost int16) error {
	return nil
}

func (s *fakeStore) InsertSyntheticLedgerEntry(ctx context.Context, group, userKey string, cost int16) error {
	return nil
}

func (s *fakeStore) EvictOldestLedgerEntries(ctx context.Context, group, userKey string, maxEvict int32) ([]gen.EvictOldestLedgerEntriesRow, error) {
	return nil, nil
}

func (s *fakeStore) DeleteLedgerEntryByID(ctx context.Context, entryID uuid.UUID) error { return nil }

func (s *fakeStore) RecordServerLedgerReading(ctx context.Context, arg gen.RecordServerLedgerReadingParams) error {
	return nil
}

func (s *fakeStore) RecordReconciledLedgerLocal(ctx context.Context, group, userKey string, localRemainingAfter *int32) error {
	return nil
}

func (s *fakeStore) ReduceLedgerEntryCost(ctx context.Context, entryID uuid.UUID, reduceBy int16) error {
	return nil
}

func (s *fakeStore) FlushLedgerEntriesForBucket(ctx context.Context, group, userKey string) ([]gen.AppEsiLedgerEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gen.AppEsiLedgerEntry
	for _, e := range s.entries {
		if e.RateLimitGroup == group && e.UserKey == userKey {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *fakeStore) BulkInsertLedgerEntry(ctx context.Context, arg gen.BulkInsertLedgerEntryParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.entries[arg.EntryID]; exists {
		return nil // ON CONFLICT DO NOTHING
	}
	s.entries[arg.EntryID] = gen.AppEsiLedgerEntry(arg)
	return nil
}

func (s *fakeStore) ListLedgerBuckets(ctx context.Context) ([]gen.AppEsiLedgerBucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]gen.AppEsiLedgerBucket, 0, len(s.buckets))
	for _, b := range s.buckets {
		out = append(out, b)
	}
	return out, nil
}

var _ Store = (*fakeStore)(nil)

// fakeReplicaCounter lets a test control the live-replica count directly,
// simulating heartbeats/expiry without a real app.esi_replica table.
type fakeReplicaCounter struct {
	mu    sync.Mutex
	count int64
	// err, when set, makes every read fail — the "registry unreachable"
	// branch of ensureMode.
	err error
}

func (f *fakeReplicaCounter) set(n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count = n
}

func (f *fakeReplicaCounter) CountLiveReplicas(ctx context.Context, liveThreshold time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

// TestModeSelectedFromReplicaRegistry (roadmap exit criterion): one live
// replica selects solo; a second heartbeat selects clustered; expiry
// (dropping back to one) selects solo again.
func TestModeSelectedFromReplicaRegistry(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	replicas := &fakeReplicaCounter{count: 1}

	// Flush machinery only needs a Store — the in-memory fake — for this
	// test; only the *mode selection* is under test here.
	// TestModeTransitionLosesNoEntries below proves the flush itself is
	// correct, and shared_integration_test.go proves the real clustered
	// SQL path against a live PG18.
	fake := newFakeStore()
	g1 := newGovernor1ForTest(fake, replicas, clock)
	req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: 10, Window: time.Minute, RequestTimeout: 30 * time.Second}

	res, err := g1.Acquire(ctx, req) // solo path; one live replica
	require.NoError(t, err)
	require.NoError(t, g1.Settle(ctx, res, Cost2XX, clock.Now()))
	require.Equal(t, ModeSolo, modeOf(g1))

	// A second heartbeat selects clustered.
	replicas.set(2)
	require.NoError(t, g1.forceModeCheck(ctx))
	require.Equal(t, ModeClustered, modeOf(g1))

	// The registry dropping back to one replica (the other's heartbeat
	// having expired) selects solo again.
	replicas.set(1)
	require.NoError(t, g1.forceModeCheck(ctx))
	require.Equal(t, ModeSolo, modeOf(g1))
}

// TestModeTransitionLosesNoEntries (roadmap exit criterion): both
// transition directions preserve the exact live-cost sum.
func TestModeTransitionLosesNoEntries(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	replicas := &fakeReplicaCounter{count: 1}
	fake := newFakeStore()
	g1 := newGovernor1ForTest(fake, replicas, clock)

	req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: 100, Window: time.Minute, RequestTimeout: 30 * time.Second}

	// Build up solo-side state: a mix of settled entries and one
	// in-flight reservation.
	var sum int
	for i := 0; i < 5; i++ {
		res, err := g1.Acquire(ctx, req)
		require.NoError(t, err)
		require.NoError(t, g1.Settle(ctx, res, Cost2XX, clock.Now()))
		sum += int(Cost2XX)
	}
	inFlight, err := g1.Acquire(ctx, req)
	require.NoError(t, err)
	sum += int(CostReserved) // still outstanding when the transition happens

	// solo -> clustered: force the transition.
	replicas.set(2)
	require.NoError(t, g1.forceModeCheck(ctx))
	require.Equal(t, ModeClustered, modeOf(g1))

	total := fake.sumCosts(req.Group, req.UserKey, true)
	require.EqualValues(t, sum, total, "solo->clustered flush must preserve the exact live-cost sum, including the in-flight reservation")

	// Settle the in-flight reservation directly against the fake store
	// the way the clustered path would (SettleLedgerEntry updates cost
	// in place) — simulate by rewriting the entry.
	fake.mu.Lock()
	e := fake.entries[inFlight.EntryID]
	e.Cost = Cost2XX
	e.State = "settled"
	fake.entries[inFlight.EntryID] = e
	fake.mu.Unlock()
	sum = sum - int(CostReserved) + int(Cost2XX)

	// clustered -> solo: force the transition back.
	replicas.set(1)
	require.NoError(t, g1.forceModeCheck(ctx))
	require.Equal(t, ModeSolo, modeOf(g1))

	entries, reservations, _, _ := g1.solo.snapshot(req.Group, req.UserKey)
	back := 0
	for _, e := range entries {
		back += int(e.cost)
	}
	back += len(reservations) * int(CostReserved)
	require.Equal(t, sum, back, "clustered->solo flush must preserve the exact live-cost sum")
}

// newGovernor1ForTest builds a Governor1 whose clustered ledger's flush
// operations are redirected to store (a fakeStore), without needing a real
// pgx pool — LedgerClustered.Acquire/Settle/Reconcile are never exercised
// in these tests, only the flush direction mode.go drives.
func newGovernor1ForTest(store Store, replicas ReplicaCounter, clock Clock) *Governor1 {
	g1 := NewGovernor1(nil, replicas, clock, nil)
	g1.SetModeCheckInterval(0) // check on every call, for deterministic tests
	g1.testFlushStore = store
	return g1
}

// modeOf is Mode() narrowed to its first return value, for the assertions
// that are about WHICH mode is active and not about whether it has been
// observed yet. TestModeIsUnobservedUntilTheRegistryIsRead below is the one
// that cares about the second value, and it calls Mode() directly.
func modeOf(g *Governor1) Mode {
	m, _ := g.Mode()
	return m
}

// TestModeIsUnobservedUntilTheRegistryIsRead is defect B-10's regression
// test.
//
// NewGovernor1 has to start somewhere and starts in solo. Until ensureMode
// has actually read the replica registry that is an ASSUMPTION, and
// esi_ledger_mode used to publish it in exactly the same shape as a
// reading — which made condition 1.8 ("clustered throughout an N=3 run")
// unsatisfiable in any run that also honours §1.4's required mid-run
// replica restart, because the restarted replica reported solo until its
// first request.
func TestModeIsUnobservedUntilTheRegistryIsRead(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	replicas := &fakeReplicaCounter{count: 3}
	g1 := newGovernor1ForTest(newFakeStore(), replicas, clock)

	mode, observed := g1.Mode()
	require.Equal(t, ModeSolo, mode, "the ledger has to start somewhere")
	require.False(t, observed, "…but nothing has been read yet, and the gauge must be able to tell")

	// One Acquire is all it takes: ensureMode runs BEFORE the ledger is
	// chosen, and on the first call the throttle cannot skip it.
	_, err := g1.Acquire(ctx, AcquireRequest{
		Group: "g", UserKey: "u", MaxTokens: 10, Window: time.Minute, RequestTimeout: 30 * time.Second,
	})
	require.NoError(t, err)

	mode, observed = g1.Mode()
	require.Equal(t, ModeClustered, mode, "three live replicas select clustered")
	require.True(t, observed)
}

// TestModeStaysUnobservedWhenTheRegistryIsUnreachable: holding the starting
// assumption because the registry cannot be read is still holding an
// assumption. Reporting it would be the same lie one layer down.
func TestModeStaysUnobservedWhenTheRegistryIsUnreachable(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	replicas := &fakeReplicaCounter{err: errors.New("registry unreachable")}
	g1 := newGovernor1ForTest(newFakeStore(), replicas, clock)

	_, err := g1.Acquire(ctx, AcquireRequest{
		Group: "g", UserKey: "u", MaxTokens: 10, Window: time.Minute, RequestTimeout: 30 * time.Second,
	})
	require.NoError(t, err, "an unreachable registry must not fail the acquire")

	_, observed := g1.Mode()
	require.False(t, observed)
}
