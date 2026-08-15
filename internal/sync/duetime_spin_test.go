package sync_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/sync"
)

// ── THE 304 SPIN, PINNED (PHASE 20.5) ────────────────────────────────────
//
// A subscription that has been answering 304 for longer than its own poll
// interval recomputed the SAME past due time on every attempt, so the
// planner reclaimed it every 5 seconds forever. Measured live: 62 of 85
// enabled subscriptions stuck more than an hour in the past, one at
// consecutive_304 = 2785.

func ttlRoute(age time.Duration) sync.RouteCacheConfig {
	return sync.RouteCacheConfig{CacheMode: "ttl-based", CacheAge: age}
}

func TestALongRunning304StreamNeverSchedulesInThePast(t *testing.T) {
	now := time.Date(2026, 8, 15, 3, 16, 0, 0, time.UTC)
	// The live shape exactly: a ttl-based route with a one-hour cache age
	// whose last 200 was 25 hours ago and which has 304'd ever since.
	next, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:          ttlRoute(time.Hour),
		Policy:         sync.PolicyConfig{TTLFloor: 60 * time.Second, BackoffCap: 4 * time.Hour},
		LastSuccess:    now.Add(-25 * time.Hour),
		Consecutive304: 2333,
		Now:            now,
	})
	require.NoError(t, err)
	require.True(t, next.After(now),
		"a 304 must schedule the NEXT poll, not re-derive one 24 hours in the past — that is the spin (got %s, now %s)", next, now)
	require.LessOrEqual(t, next.Sub(now), 2*time.Hour,
		"and it must still respect the route's own interval rather than being pushed arbitrarily far out")
}

// TestAnEarlyPollStillAnchorsOnTheUpstreamCacheWindow — the behaviour the
// anchor was written for is preserved. A route polled BEFORE its upstream
// cache expired is re-scheduled from when the data was generated, not from
// when we happened to ask, so HANGAR does not walk its poll times forward
// by one round-trip on every cycle.
func TestAnEarlyPollStillAnchorsOnTheUpstreamCacheWindow(t *testing.T) {
	now := time.Date(2026, 8, 15, 3, 16, 0, 0, time.UTC)
	lastSuccess := now.Add(-10 * time.Minute)
	next, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:       ttlRoute(time.Hour),
		Policy:      sync.PolicyConfig{TTLFloor: 60 * time.Second, BackoffCap: 4 * time.Hour},
		LastSuccess: lastSuccess,
		Now:         now,
	})
	require.NoError(t, err)
	require.True(t, next.After(now))
	require.True(t, next.Before(now.Add(2*time.Hour)))
	// Anchored on last_success, so at most one interval + jitter past it.
	require.True(t, next.Before(lastSuccess.Add(2*time.Hour+time.Second)),
		"an early poll must still be re-anchored on the upstream cache window")
}

// TestARepeatedPlanConverges is the property the spin violated: planning
// again from the time you were just given must move forward, every time.
func TestARepeatedPlanConverges(t *testing.T) {
	now := time.Date(2026, 8, 15, 3, 16, 0, 0, time.UTC)
	lastSuccess := now.Add(-25 * time.Hour)
	for i := range 50 {
		next, err := sync.PlanNextDueAt(sync.DueTimeInput{
			Route:          ttlRoute(time.Hour),
			Policy:         sync.PolicyConfig{TTLFloor: 60 * time.Second, BackoffCap: 4 * time.Hour},
			LastSuccess:    lastSuccess, // never updated — a 304 does not move it
			Consecutive304: i,
			Now:            now,
		})
		require.NoError(t, err)
		require.Truef(t, next.After(now), "iteration %d scheduled at or before now", i)
		now = next // the planner will not look again until then
	}
}
