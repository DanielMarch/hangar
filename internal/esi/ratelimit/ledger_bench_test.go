package ratelimit

import (
	"context"
	"testing"
	"time"
)

// numBenchBuckets and benchClockAdvance together keep
// benchmarkLedgerSoloWorkload from ever hitting insufficient-budget: with
// max_tokens=400 and cost 2 per settle, a bucket can absorb 200
// acquire/settle pairs before its oldest entry must have aged out of the
// window. Cycling round-robin through numBenchBuckets buckets, a given
// bucket is revisited every numBenchBuckets iterations, so advancing the
// clock by benchClockAdvance on every iteration guarantees window elapses
// well before any bucket's 200th visit — with a large safety margin, so
// this measures the acquire/settle hot path, never eviction-driven
// blocking.
const (
	numBenchBuckets   = 256
	benchMaxTokens    = 400 // §5.5's own illustrative bucket size
	benchWindow       = time.Hour
	benchClockAdvance = benchWindow / 25_600 // << benchWindow / (200 * numBenchBuckets)
)

func benchAcquireRequests() []AcquireRequest {
	reqs := make([]AcquireRequest, numBenchBuckets)
	for i := range reqs {
		reqs[i] = AcquireRequest{
			Group:   "bench",
			UserKey: string(rune('a'+i%26)) + string(rune('A'+(i/26)%26)),
			// A realistic ESI group size (§5.5's own note uses ~100 as the
			// illustrative max_tokens). The preallocated heap slice is
			// sized max_tokens+8, so an unrealistically large max_tokens
			// here would benchmark a giant allocation no production
			// bucket ever makes, not the acquire/settle hot path this
			// benchmark exists to measure.
			MaxTokens: benchMaxTokens, Window: benchWindow, RequestTimeout: 30 * time.Second,
		}
	}
	return reqs
}

// BenchmarkLedgerSolo1MOperations (roadmap exit criterion): < 2 seconds for
// 1,000,000 acquire/settle pairs on the in-process path (§5.5's "tens of
// nanoseconds" heap-push/pop budget gives three orders of magnitude of
// headroom over the 2µs/op line this draws).
func BenchmarkLedgerSolo1MOperations(b *testing.B) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ledger := NewLedgerSolo(clock)
	ctx := context.Background()
	reqs := benchAcquireRequests()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clock.Advance(benchClockAdvance)
		req := reqs[i%numBenchBuckets]
		res, err := ledger.Acquire(ctx, req)
		if err != nil {
			b.Fatalf("acquire: %v", err)
		}
		if err := ledger.Settle(ctx, res, Cost2XX, clock.Now()); err != nil {
			b.Fatalf("settle: %v", err)
		}
	}
}

// TestBenchmarkLedgerSolo1MOperationsMeetsBudget runs the same workload as
// BenchmarkLedgerSolo1MOperations as an ordinary test with a fixed
// iteration count, so `go test` (not just `go test -bench`) enforces the
// roadmap's "< 2 seconds for 1,000,000 acquire/settle pairs" pass
// condition in CI, independent of `go test -bench` ever being invoked.
func TestBenchmarkLedgerSolo1MOperationsMeetsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1M-operation timing test in -short mode")
	}
	if raceDetectorEnabled {
		// The race detector's instrumentation overhead (routinely 5-10x)
		// swamps this budget regardless of the ledger's own hot-path
		// performance — measured 12.7s vs the 2s budget under `go test
		// -race`, an artifact of instrumentation, not a regression. The
		// correctness this test's workload exercises is still fully
		// covered by every other (non-timing) test in this package
		// running under -race; only the wall-clock assertion is skipped.
		t.Skip("skipping wall-clock budget assertion under the race detector — see race_off.go")
	}
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ledger := NewLedgerSolo(clock)
	ctx := context.Background()
	reqs := benchAcquireRequests()

	const n = 1_000_000
	start := time.Now()
	for i := 0; i < n; i++ {
		clock.Advance(benchClockAdvance)
		req := reqs[i%numBenchBuckets]
		res, err := ledger.Acquire(ctx, req)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := ledger.Settle(ctx, res, Cost2XX, clock.Now()); err != nil {
			t.Fatalf("settle: %v", err)
		}
	}
	elapsed := time.Since(start)
	t.Logf("%d acquire/settle pairs in %s (%s/op)", n, elapsed, elapsed/n)
	if elapsed > 2*time.Second {
		t.Fatalf("1,000,000 acquire/settle pairs took %s, want < 2s", elapsed)
	}
}
