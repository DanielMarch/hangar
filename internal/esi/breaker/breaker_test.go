package breaker

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestRouteBreakerOpensAtTenConsecutive5XX (§5.8): the route breaker opens
// on >=10 consecutive 5XX for a route.
func TestRouteBreakerOpensAtTenConsecutive5XX(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	b := NewRouteBreaker(time.Minute, clock)

	for i := 0; i < 9; i++ {
		require.True(t, b.Allow("r"))
		b.RecordFailure("r")
	}
	require.Equal(t, StateClosed, b.State("r"), "9 consecutive failures must not open the breaker")

	require.True(t, b.Allow("r"))
	b.RecordFailure("r") // 10th
	require.Equal(t, StateOpen, b.State("r"))
	require.False(t, b.Allow("r"), "an open breaker must refuse requests")
}

// TestRouteBreakerHalfOpenProbeAtRouteTTL: after the probe TTL elapses, the
// breaker allows exactly one probe; success closes it.
func TestRouteBreakerHalfOpenProbeAtRouteTTL(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ttl := 30 * time.Second
	b := NewRouteBreaker(ttl, clock)
	for i := 0; i < 10; i++ {
		b.RecordFailure("r")
	}
	require.Equal(t, StateOpen, b.State("r"))
	require.False(t, b.Allow("r"))

	clock.Advance(ttl)
	require.True(t, b.Allow("r"), "the probe must be allowed once the TTL elapses")
	require.Equal(t, StateHalfOpen, b.State("r"))
	require.False(t, b.Allow("r"), "only one probe may be in flight while half-open")

	b.RecordSuccess("r")
	require.Equal(t, StateClosed, b.State("r"))
	require.True(t, b.Allow("r"))
}

// TestEntityBreakerOpensAtFiveConsecutive403sScopedPerEntity (§5.8): the
// 403 breaker opens per (route, entity) at 5 consecutive failures, and a
// breaker opening for one entity must not affect a sibling entity on the
// same route (Principle 3).
func TestEntityBreakerOpensAtFiveConsecutive403sScopedPerEntity(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	b := NewEntityBreaker(time.Minute, clock)

	const route = "/corporations/{corporation_id}/structures"
	const corpA, corpB int64 = 1000, 2000

	for i := 0; i < 5; i++ {
		require.True(t, b.Allow(route, corpA))
		b.RecordFailure(route, corpA)
	}
	require.Equal(t, StateOpen, b.State(route, corpA))
	require.False(t, b.Allow(route, corpA))

	// A different corporation on the SAME route must be unaffected.
	require.True(t, b.Allow(route, corpB), "one director losing a role must not break the route for other corporations")
	require.Equal(t, StateClosed, b.State(route, corpB))
}

func TestBreakerFailedProbeReopensAndResetsTTL(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ttl := 10 * time.Second
	b := NewRouteBreaker(ttl, clock)
	for i := 0; i < 10; i++ {
		b.RecordFailure("r")
	}
	clock.Advance(ttl)
	require.True(t, b.Allow("r")) // half-open probe admitted
	b.RecordFailure("r")          // probe itself fails
	require.Equal(t, StateOpen, b.State("r"))
	require.False(t, b.Allow("r"), "immediately after a failed probe, the breaker must be open again")

	clock.Advance(ttl)
	require.True(t, b.Allow("r"), "a fresh TTL after the failed probe must allow another probe")
}
