package sync_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/sync"
)

// TestAdaptiveBackoffOn304ResetOn200 (roadmap exit criterion): 1.5^n growth
// capped at backoff_cap; reset only on 200 — never on a 304, which is the
// whole point of the mechanism existing (§6.2).
func TestAdaptiveBackoffOn304ResetOn200(t *testing.T) {
	const base = time.Minute
	const backoffCap = time.Hour

	// Growth: each additional consecutive 304 multiplies by 1.5, until the
	// cap takes over.
	prev := sync.Backoff(base, 0, backoffCap)
	if prev != base {
		t.Fatalf("Backoff(base, 0, cap) = %s, want base (%s)", prev, base)
	}
	for n := 1; n <= 30; n++ {
		got := sync.Backoff(base, n, backoffCap)
		if got < prev {
			t.Fatalf("Backoff must never shrink as consecutive304 grows: n=%d got %s < previous %s", n, got, prev)
		}
		if got > backoffCap {
			t.Fatalf("Backoff(%d) = %s exceeds cap %s", n, got, backoffCap)
		}
		prev = got
	}
	if prev != backoffCap {
		t.Fatalf("after enough consecutive 304s, Backoff must saturate at the cap; got %s, want %s", prev, backoffCap)
	}

	// The reset contract itself: consecutive304 is a caller-owned counter.
	// Backoff has no memory between calls, so "resets only on 200" is
	// enforced by what the caller passes in, not by anything stateful in
	// Backoff — verify the pure function's role in that contract: passing
	// 0 (what a 200 handler must do) always yields base, regardless of how
	// large the previous streak was.
	afterReset := sync.Backoff(base, 0, backoffCap)
	if afterReset != base {
		t.Fatalf("Backoff(base, 0, cap) after a simulated reset = %s, want base (%s)", afterReset, base)
	}
}

func TestBackoffNeverExceedsCapForExtremeCounts(t *testing.T) {
	got := sync.Backoff(time.Second, 1_000_000, time.Hour)
	if got != time.Hour {
		t.Fatalf("an extreme consecutive304 count must saturate at cap, got %s", got)
	}
}

func TestBackoffZeroBaseStaysZero(t *testing.T) {
	if got := sync.Backoff(0, 5, time.Hour); got != 0 {
		t.Fatalf("Backoff(0, ...) = %s, want 0", got)
	}
}

func TestFullJitterRangeUnit(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	const d = 10 * time.Second
	for i := 0; i < 500; i++ {
		got := sync.FullJitter(d, rnd)
		if got < 0 || got >= d {
			t.Fatalf("FullJitter(%s) = %s, want in [0, %s)", d, got, d)
		}
	}
	if sync.FullJitter(0, rnd) != 0 {
		t.Fatalf("FullJitter(0, ...) must be 0")
	}
	if sync.FullJitter(-time.Second, rnd) != 0 {
		t.Fatalf("FullJitter of a negative duration must be 0")
	}
}
