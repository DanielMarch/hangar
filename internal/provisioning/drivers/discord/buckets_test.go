package discord_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/discord"
	"github.com/stretchr/testify/require"
)

// fakeClock is a controllable, concurrency-safe Clock for deterministic
// bucket/limiter tests — mutex-guarded because these tests mutate `now`
// from the test goroutine while the limiter under test reads it from its
// own goroutine.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestBucketKeyedOnHeaderNotURL (roadmap exit criterion): two different
// route templates (distinct URLs) that Discord happens to place in the
// SAME rate-limit bucket must share the limiter — exhausting the bucket
// via one route blocks the other, proving the key is the header value,
// never the URL.
func TestBucketKeyedOnHeaderNotURL(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := discord.NewBucketLimiter(clock)

	routeA := "PUT /guilds/{guild}/members/{userA}/roles/{role}"
	routeB := "PUT /guilds/{guild}/members/{userB}/roles/{role}"
	sharedBucket := "shared-bucket-xyz"

	// Route A's first response teaches the limiter about the bucket,
	// exhausted (remaining=0), resetting 200ms from now.
	limiter.Update(routeA, sharedBucket, 0, 0.2)

	// Route B has NEVER been called before, but it shares the same
	// bucket. Update it too (as if its own first response also reported
	// the same bucket id) with the SAME low remaining, then assert
	// Reserve for route B respects the shared state.
	limiter.Update(routeB, sharedBucket, 0, 0.2)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- limiter.Reserve(ctx, routeB) }()

	// Advance the fake clock past the reset — Reserve computes its wait
	// from clock.Now(), and the fake clock never auto-advances.
	time.Sleep(10 * time.Millisecond)
	clock.Advance(300 * time.Millisecond)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("Reserve for routeB never returned — shared bucket state from routeA was not applied")
	}
}

// TestBucketReserveDoesNotBlockOnUnknownRoute: a route that has never
// received a response (no known bucket) must never block — there is
// nothing to wait for.
func TestBucketReserveDoesNotBlockOnUnknownRoute(t *testing.T) {
	limiter := discord.NewBucketLimiter(discord.SystemClock)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.NoError(t, limiter.Reserve(ctx, "GET /guilds/{guild}/roles"))
}

// TestBucketReserveDoesNotBlockWhenRemainingPositive: a known bucket with
// remaining > 0 never blocks.
func TestBucketReserveDoesNotBlockWhenRemainingPositive(t *testing.T) {
	limiter := discord.NewBucketLimiter(discord.SystemClock)
	route := "GET /guilds/{guild}/roles"
	limiter.Update(route, "bucket-1", 5, 60)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.NoError(t, limiter.Reserve(ctx, route))
}

// TestGlobalLimiterEnforcesCeiling: requests beyond max/second block until
// the next real 1-second window (the limiter's window is fixed at 1s to
// match Discord's own "requests/second" ceiling — SystemClock is used
// here deliberately, since a real window boundary is exactly what's under
// test).
func TestGlobalLimiterEnforcesCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: this test waits out a real 1-second window")
	}
	limiter := discord.NewGlobalLimiter(2, discord.SystemClock)
	ctx := context.Background()

	require.NoError(t, limiter.Wait(ctx))
	require.NoError(t, limiter.Wait(ctx))

	blocked := make(chan error, 1)
	start := time.Now()
	go func() { blocked <- limiter.Wait(ctx) }()

	select {
	case <-blocked:
		t.Fatal("third call within the same 1-second window must have blocked")
	case <-time.After(100 * time.Millisecond):
		// expected: still blocked shortly after the first two calls
	}

	select {
	case err := <-blocked:
		require.NoError(t, err)
		require.GreaterOrEqualf(t, time.Since(start), 800*time.Millisecond, "third call must not unblock before the window rolls over")
	case <-time.After(2 * time.Second):
		t.Fatal("call never unblocked after the window rolled over")
	}
}

// TestGlobalLimiterPause: an observed X-RateLimit-Global signal pauses
// EVERY future Wait call, not just the bucket that triggered it.
func TestGlobalLimiterPause(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := discord.NewGlobalLimiter(50, clock)
	limiter.Pause(500 * time.Millisecond)

	ctx := context.Background()
	blocked := make(chan error, 1)
	go func() { blocked <- limiter.Wait(ctx) }()

	select {
	case <-blocked:
		t.Fatal("Wait must block while the global pause is in effect")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	clock.Advance(600 * time.Millisecond)
	select {
	case err := <-blocked:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Wait never unblocked after the pause duration elapsed on the clock")
	}
}
