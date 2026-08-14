package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock is a manually-advanced Clock, giving every test deterministic
// window arithmetic with no real sleeping (§5.5's "injected for
// testability" instruction).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

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

func acquireSettle(t *testing.T, l Ledger, ctx context.Context, req AcquireRequest, cost int16, respondedAt time.Time) *Reservation {
	t.Helper()
	res, err := l.Acquire(ctx, req)
	require.NoError(t, err)
	require.NoError(t, l.Settle(ctx, res, cost, respondedAt))
	return res
}

// TestLedgerFidelityAgainstFloatingWindow is the roadmap's headline exit
// test: over a simulated 15-minute window, each individual request's cost
// must return to the budget EXACTLY one window_size after that request —
// never earlier (a refill-bucket bug) and never later (an over-conservative
// bug). It must fail if a continuous-refill model is substituted, which the
// second half of this test proves directly by modelling what a refill
// bucket would have reported and asserting it diverges from the ledger.
func TestLedgerFidelityAgainstFloatingWindow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ledger := NewLedgerSolo(clock)
	ctx := context.Background()

	const maxTokens = 100
	window := 15 * time.Minute
	req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: maxTokens, Window: window, RequestTimeout: 30 * time.Second}

	// A single 2XX request at t=0.
	res := acquireSettle(t, ledger, ctx, req, Cost2XX, clock.Now())
	_ = res

	availableAt := func() int {
		// Force an eviction pass by issuing a zero-cost probe: acquire
		// (which evicts) then abandon via a synthetic settle of 0 would
		// mutate state, so instead reconcile against the current
		// max_tokens with a no-op serverRemaining reading equal to
		// whatever's live — cheapest is to read the bucket directly via
		// the shard, which the ledger exposes through snapshot() for
		// exactly this kind of introspection.
		entries, reservations, _, _ := ledger.snapshot(req.Group, req.UserKey)
		used := 0
		for _, e := range entries {
			used += int(e.cost)
		}
		used += len(reservations) * int(CostReserved)
		return maxTokens - used
	}

	// Immediately after: 2 tokens are consumed.
	require.Equal(t, maxTokens-2, availableAt())

	// One second before the window elapses, the cost must still be live.
	clock.Advance(window - time.Second)
	require.Equal(t, maxTokens-2, availableAt(), "cost must still be charged just before the window elapses")

	// Exactly at (and past) the window boundary, the cost must be gone —
	// "exactly one window_size after that individual request", not later.
	clock.Advance(time.Second)
	require.Equal(t, maxTokens, availableAt(), "cost must return to the budget exactly one window after the request")

	// ---- refill-model divergence ----
	// A continuous-refill bucket refills at maxTokens/window continuously
	// from the moment of consumption, so a fixed time later it reports a
	// DIFFERENT available count than the floating window for a burst
	// pattern. Demonstrate the divergence a refill model would produce,
	// and assert the real ledger does NOT match it.
	clock2 := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ledger2 := NewLedgerSolo(clock2)
	req2 := req

	// Burst: 10 requests at t=0, each costing 2 (total 20).
	for i := 0; i < 10; i++ {
		acquireSettle(t, ledger2, ctx, req2, Cost2XX, clock2.Now())
	}
	entries, reservations, _, _ := ledger2.snapshot(req2.Group, req2.UserKey)
	used := 0
	for _, e := range entries {
		used += int(e.cost)
	}
	used += len(reservations) * int(CostReserved)
	floatingAvailable := maxTokens - used
	require.Equal(t, maxTokens-20, floatingAvailable)

	// Halfway through the window, a refill bucket (rate = maxTokens/window)
	// would have refilled maxTokens*0.5 tokens' worth of headroom already
	// — i.e. it would report significantly MORE available than the
	// floating window, which reports none of the 20 back until the full
	// window elapses per-request. That is exactly the divergence §5.5
	// prohibits a HANGAR implementation from exhibiting.
	clock2.Advance(window / 2)
	entries, reservations, _, _ = ledger2.snapshot(req2.Group, req2.UserKey)
	used = 0
	for _, e := range entries {
		used += int(e.cost)
	}
	used += len(reservations) * int(CostReserved)
	floatingAvailableHalfway := maxTokens - used
	require.Equal(t, maxTokens-20, floatingAvailableHalfway, "the floating window must not have released any of the burst's cost yet")

	refillRatePerNs := float64(maxTokens) / float64(window.Nanoseconds())
	refilled := int(refillRatePerNs * float64((window / 2).Nanoseconds()))
	refillModelAvailable := (maxTokens - 20) + refilled
	require.NotEqual(t, refillModelAvailable, floatingAvailableHalfway,
		"a refill model would diverge from the floating window here — if this ever matches, a refill model has been substituted")
}

// TestPredictiveReservationSurvives4XXRun (roadmap exit criterion): a run
// of 4XX responses must never fail the reservation step — the worst case is
// reserved up front, so consecutive 4XXs are exactly what the design
// expects to be able to overdraw against.
func TestPredictiveReservationSurvives4XXRun(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ledger := NewLedgerSolo(clock)
	ctx := context.Background()
	req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: 10, Window: time.Minute, RequestTimeout: 30 * time.Second}

	// 10 tokens / 5 per reservation = exactly 2 reservations fit before
	// the budget is exhausted purely by reservations, with no settling in
	// between.
	res1, err := ledger.Acquire(ctx, req)
	require.NoError(t, err)
	res2, err := ledger.Acquire(ctx, req)
	require.NoError(t, err)

	// A third acquire while both reservations are outstanding must be
	// refused — this is expected behaviour, not the "survives" part.
	_, err = ledger.Acquire(ctx, req)
	require.Error(t, err)
	var retryErr *RetryAtError
	require.True(t, errors.As(err, &retryErr))

	// Settle both as 4XX (cost 5 each) — overdrawing the window is
	// intentional per §5.5.
	require.NoError(t, ledger.Settle(ctx, res1, Cost4XXOther, clock.Now()))
	require.NoError(t, ledger.Settle(ctx, res2, Cost4XXOther, clock.Now()))

	entries, _, _, _ := ledger.snapshot(req.Group, req.UserKey)
	total := 0
	for _, e := range entries {
		total += int(e.cost)
	}
	require.Equal(t, 10, total, "a run of 4XX responses must be allowed to overdraw exactly to the reserved amount, never rejected at settle time")

	// A run of MANY more 4XX responses, issued and settled sequentially
	// (each reservation released by settle before the next acquire), must
	// never itself error out — the point under test is that predictive
	// reservation never refuses an acquire just because prior settles
	// were all bad outcomes, as long as each is settled before the next
	// is reserved and the window has room.
	clock.Advance(2 * time.Minute) // clear the window from the burst above
	for i := 0; i < 50; i++ {
		res, err := ledger.Acquire(ctx, req)
		require.NoError(t, err, "iteration %d", i)
		require.NoError(t, ledger.Settle(ctx, res, Cost4XXOther, clock.Now()))
		clock.Advance(time.Minute + time.Second) // let each settle's cost expire before the next
	}
}

// TestServerHeadersAlwaysWin (roadmap exit criterion): divergence in both
// directions converges to the server-reported value.
func TestServerHeadersAlwaysWin(t *testing.T) {
	ctx := context.Background()

	t.Run("server reports less than local", func(t *testing.T) {
		clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		ledger := NewLedgerSolo(clock)
		req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: 100, Window: time.Minute, RequestTimeout: 30 * time.Second}
		acquireSettle(t, ledger, ctx, req, Cost2XX, clock.Now()) // local available = 98

		require.NoError(t, ledger.Reconcile(ctx, req.Group, req.UserKey, req.MaxTokens, 80))

		entries, _, _, _ := ledger.snapshot(req.Group, req.UserKey)
		used := 0
		for _, e := range entries {
			used += int(e.cost)
		}
		require.Equal(t, 20, used, "local used must converge to what the server implied (max_tokens - serverRemaining)")
	})

	t.Run("server reports more than local", func(t *testing.T) {
		clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		ledger := NewLedgerSolo(clock)
		req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: 100, Window: time.Minute, RequestTimeout: 30 * time.Second}
		for i := 0; i < 5; i++ {
			acquireSettle(t, ledger, ctx, req, Cost2XX, clock.Now())
		}
		// local used = 10, available = 90

		require.NoError(t, ledger.Reconcile(ctx, req.Group, req.UserKey, req.MaxTokens, 99))

		entries, _, _, _ := ledger.snapshot(req.Group, req.UserKey)
		used := 0
		for _, e := range entries {
			used += int(e.cost)
		}
		require.LessOrEqual(t, used, 1, "local used must converge downward toward what the server implied")
	})

	t.Run("never exceeds max_tokens even if server over-reports", func(t *testing.T) {
		clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		ledger := NewLedgerSolo(clock)
		req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: 100, Window: time.Minute, RequestTimeout: 30 * time.Second}
		acquireSettle(t, ledger, ctx, req, Cost2XX, clock.Now())

		require.NoError(t, ledger.Reconcile(ctx, req.Group, req.UserKey, req.MaxTokens, 500))

		entries, reservations, _, _ := ledger.snapshot(req.Group, req.UserKey)
		used := 0
		for _, e := range entries {
			used += int(e.cost)
		}
		used += len(reservations) * int(CostReserved)
		require.GreaterOrEqual(t, req.MaxTokens-used, 0)
		require.LessOrEqual(t, req.MaxTokens-used, req.MaxTokens, "available must never exceed max_tokens")
	})
}

// TestReconcileIgnoresInFlightReservations — PHASE 20.2, found by running
// the system rather than by reading it.
//
// Within a minute of B29's wiring going live against real ESI, the
// development installation reported an `esi_ledger_divergence` of 10 on
// `char-location` against Gate 1.3's tolerance of 1, on an installation
// with no errors and nothing wrong with it. The cause: reconciliation
// compared the server's X-Ratelimit-Remaining against local availability
// INCLUDING in-flight reservations.
//
// A reservation is HANGAR's prediction of a request that has not finished,
// so the server's reading cannot possibly have counted it. Including it
// makes local look lower than the server, which drives the reconciler to
// evict settled entries — forgiving consumption that really happened. The
// reservation then settles at a cost lower than the reserved 5, local now
// looks HIGHER than the server, and the next response injects a synthetic
// entry to compensate. The two directions chase each other forever; the
// live bucket had accumulated six synthetic entries worth 15.
func TestReconcileIgnoresInFlightReservations(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Now()}
	l := NewLedgerSolo(clock)

	const maxTokens = 100
	req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: maxTokens, Window: time.Minute, RequestTimeout: time.Minute}

	// One settled 2XX: settled consumption is 2, so settled availability is 98.
	settled, err := l.Acquire(ctx, req)
	require.NoError(t, err)
	require.NoError(t, l.Settle(ctx, settled, Cost2XX, clock.Now()))

	// One request still in flight: it holds a worst-case reservation of 5,
	// so ACQUIRE-side availability is 93 — but the server has answered
	// nothing about it, and its own header still says 98.
	inFlight, err := l.Acquire(ctx, req)
	require.NoError(t, err)

	require.NoError(t, l.Reconcile(ctx, "g", "u", maxTokens, 98))

	// Nothing should have changed: the server agrees with settled
	// consumption exactly. Before the fix, the reconciler saw 93 against 98
	// and evicted the settled entry to close a gap that was not there.
	sh := shardFor(l.shards, bucketKey("g", "u"))
	sh.mu.Lock()
	b := sh.buckets[bucketKey("g", "u")]
	settledCost, entries, reservations := b.settledCost(), b.ledger.Len(), len(b.reserved)
	sh.mu.Unlock()

	require.Equal(t, 1, entries, "the settled entry must survive — the server never asked for it to be forgiven")
	require.Equal(t, int(Cost2XX), settledCost, "settled consumption must be untouched")
	require.Equal(t, 1, reservations, "the in-flight reservation must survive reconciliation")

	// And the acquire path must STILL count the reservation — that is the
	// whole point of predictive reservation, and this fix must not weaken it.
	sh.mu.Lock()
	acquireView := b.available()
	sh.mu.Unlock()
	require.Equal(t, maxTokens-int(Cost2XX)-int(CostReserved), acquireView,
		"acquire availability must keep charging the in-flight reservation the worst case")

	_ = inFlight
}

// TestReconcileStillConvergesOnRealDisagreement is the other half: with no
// reservation in flight, "the server always wins" must still work in both
// directions, or the fix above would have turned reconciliation off.
func TestReconcileStillConvergesOnRealDisagreement(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Now()}
	l := NewLedgerSolo(clock)

	const maxTokens = 100
	req := AcquireRequest{Group: "g", UserKey: "u", MaxTokens: maxTokens, Window: time.Minute, RequestTimeout: time.Minute}
	res, err := l.Acquire(ctx, req)
	require.NoError(t, err)
	require.NoError(t, l.Settle(ctx, res, Cost2XX, clock.Now()))

	settledAvailable := func() int {
		sh := shardFor(l.shards, bucketKey("g", "u"))
		sh.mu.Lock()
		defer sh.mu.Unlock()
		return sh.buckets[bucketKey("g", "u")].settledAvailable()
	}
	require.Equal(t, 98, settledAvailable())

	// Server reports LOWER: converge downward within one request.
	require.NoError(t, l.Reconcile(ctx, "g", "u", maxTokens, 90))
	require.Equal(t, 90, settledAvailable(), "a synthetic entry must close the gap the server reported")

	// Server reports HIGHER: converge upward, never above max_tokens.
	require.NoError(t, l.Reconcile(ctx, "g", "u", maxTokens, 200))
	require.LessOrEqual(t, settledAvailable(), maxTokens, "never above max_tokens (§5.5)")
	require.Equal(t, maxTokens, settledAvailable())
}
