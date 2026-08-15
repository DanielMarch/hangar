package housekeeping_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/housekeeping"
	"github.com/hangar-project/hangar/internal/telemetry"
)

// fakeStore records what the sweeper asked for and answers with canned
// row counts.
type fakeStore struct {
	sessions, entries, replicas int64
	retentionSeen               time.Duration
	replicaCalls                int
	sessionErr                  error
	entryErr                    error
}

func (f *fakeStore) DeleteExpiredSessions(context.Context) (int64, error) {
	return f.sessions, f.sessionErr
}

func (f *fakeStore) DeleteExpiredEsiCacheEntries(context.Context) (int64, error) {
	return f.entries, f.entryErr
}

func (f *fakeStore) DeleteStaleReplicas(_ context.Context, retention time.Duration) (int64, error) {
	f.replicaCalls++
	f.retentionSeen = retention
	return f.replicas, nil
}

func TestTickSweepsAllThreeAndReportsCounts(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sessions: 19, entries: 4, replicas: 2}
	sweeper := &housekeeping.Sweeper{Store: store, ReplicaRetention: 24 * time.Hour}

	res, err := sweeper.Tick(context.Background())
	require.NoError(t, err)

	// The counts are the point: a sweep that cannot say what it removed is
	// indistinguishable from one that is not running, which is the defect
	// this package closes.
	require.Equal(t, int64(19), res.Sessions)
	require.Equal(t, int64(4), res.CacheEntries)
	require.Equal(t, int64(2), res.Replicas)
	require.Equal(t, int64(25), res.Total())
}

// TestReplicaRetentionIsPassedNegative pins the sign convention. The query
// computes `last_heartbeat <= now() + $1`, so a POSITIVE interval would
// delete every row whose heartbeat is older than a day in the FUTURE —
// i.e. the entire registry, live replicas included, on every tick.
func TestReplicaRetentionIsPassedNegative(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	sweeper := &housekeeping.Sweeper{Store: store, ReplicaRetention: 24 * time.Hour}

	_, err := sweeper.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, -24*time.Hour, store.retentionSeen,
		"the interval is added to now(), so retention must be passed as a negative duration")
}

// TestTickRefusesARetentionShorterThanTheFloor is the guard that keeps a
// janitor from manufacturing a Governor 1 breach.
//
// app.esi_replica is the registry CountLiveReplicas reads to choose solo or
// clustered mode. Deleting the row of a replica whose heartbeat is merely
// late makes each surviving replica believe it is alone, and two solo
// ledgers each spend the whole bucket — Gate 1.1's exact failure, caused by
// housekeeping.
func TestTickRefusesARetentionShorterThanTheFloor(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sessions: 3, entries: 1}
	sweeper := &housekeeping.Sweeper{Store: store, ReplicaRetention: telemetry.LiveThreshold}

	res, err := sweeper.Tick(context.Background())
	require.ErrorIs(t, err, housekeeping.ErrRetentionTooShort)
	require.Zero(t, store.replicaCalls, "an unsafe retention window must issue NO delete against the registry")

	// The two safe sweeps still ran, and their counts are reported
	// alongside the error: a bad replica window must not also stop personal
	// data being deleted.
	require.Equal(t, int64(3), res.Sessions)
	require.Equal(t, int64(1), res.CacheEntries)
	require.Zero(t, res.Replicas)
}

func TestSafeReplicaRetentionTracksTheFloor(t *testing.T) {
	t.Parallel()

	require.False(t, housekeeping.SafeReplicaRetention(0))
	require.False(t, housekeeping.SafeReplicaRetention(telemetry.LiveThreshold),
		"the liveness threshold itself is not a safe retention window — that is the whole distinction")
	require.False(t, housekeeping.SafeReplicaRetention(housekeeping.MinReplicaRetention-time.Second))
	require.True(t, housekeeping.SafeReplicaRetention(housekeeping.MinReplicaRetention))
	require.True(t, housekeeping.SafeReplicaRetention(24*time.Hour))

	require.Greater(t, housekeeping.MinReplicaRetention, housekeeping.LivenessThreshold,
		"the floor must exceed the window CountLiveReplicas treats as live, or a late heartbeat is a deletion")
}

// TestSessionsAreSweptBeforeTheRestPins the ordering. Sessions are the only
// one of the three with a personal-data reason behind it; a failure
// reclaiming cache disk must not be able to leave them in place.
func TestSessionsAreSweptBeforeTheRest(t *testing.T) {
	t.Parallel()

	failure := errors.New("cache table unavailable")
	store := &fakeStore{sessions: 7, entryErr: failure}
	sweeper := &housekeeping.Sweeper{Store: store, ReplicaRetention: 24 * time.Hour}

	res, err := sweeper.Tick(context.Background())
	require.ErrorIs(t, err, failure)
	require.Equal(t, int64(7), res.Sessions, "the session sweep ran and its result must survive the later failure")
	require.Zero(t, store.replicaCalls)
}

func TestSessionSweepFailureIsNamed(t *testing.T) {
	t.Parallel()

	failure := errors.New("connection refused")
	store := &fakeStore{sessionErr: failure}
	sweeper := &housekeeping.Sweeper{Store: store, ReplicaRetention: 24 * time.Hour}

	_, err := sweeper.Tick(context.Background())
	require.ErrorIs(t, err, failure)
	require.Contains(t, err.Error(), "expired sessions")
}
