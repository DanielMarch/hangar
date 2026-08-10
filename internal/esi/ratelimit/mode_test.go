package ratelimit

import (
	"context"
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

func (s *fakeStore) SumLedgerEntryCost(ctx context.Context, group, userKey string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, e := range s.entries {
		if e.RateLimitGroup == group && e.UserKey == userKey {
			total += int64(e.Cost)
		}
	}
	return total, nil
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

func (s *fakeStore) RecordServerLedgerReading(ctx context.Context, group, userKey string, serverRemaining *int32) error {
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
}

func (f *fakeReplicaCounter) set(n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count = n
}

func (f *fakeReplicaCounter) CountLiveReplicas(ctx context.Context, liveThreshold time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	require.Equal(t, ModeSolo, g1.Mode())

	// A second heartbeat selects clustered.
	replicas.set(2)
	require.NoError(t, g1.forceModeCheck(ctx))
	require.Equal(t, ModeClustered, g1.Mode())

	// The registry dropping back to one replica (the other's heartbeat
	// having expired) selects solo again.
	replicas.set(1)
	require.NoError(t, g1.forceModeCheck(ctx))
	require.Equal(t, ModeSolo, g1.Mode())
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
	require.Equal(t, ModeClustered, g1.Mode())

	total, err := fake.SumLedgerEntryCost(ctx, req.Group, req.UserKey)
	require.NoError(t, err)
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
	require.Equal(t, ModeSolo, g1.Mode())

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
