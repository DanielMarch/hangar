package sync_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/sync"
)

const ttlFloor = 5 * time.Minute

// TestZeroCacheAgeResolvesToTtlFloor (roadmap exit criterion): x-cache-age:
// 0 must never schedule at 0 — it means "CCP declares no TTL contract", and
// HANGAR applies ttl_floor regardless (§6.2).
func TestZeroCacheAgeResolvesToTtlFloor(t *testing.T) {
	for _, mode := range []sync.CacheMode{sync.CacheModeTTLBased, sync.CacheModeEventBased} {
		got := sync.EffectivePollInterval(mode, 0, ttlFloor)
		if got != ttlFloor {
			t.Errorf("EffectivePollInterval(%s, cacheAge=0, ttlFloor=%s) = %s, want ttlFloor", mode, ttlFloor, got)
		}
		if got == 0 {
			t.Errorf("EffectivePollInterval(%s, cacheAge=0, ...) resolved to 0 — must never poll continuously", mode)
		}
	}
}

func TestEffectivePollInterval(t *testing.T) {
	tests := []struct {
		name     string
		mode     sync.CacheMode
		cacheAge time.Duration
		want     time.Duration
	}{
		{"ttl-based below floor uses floor", sync.CacheModeTTLBased, time.Minute, ttlFloor},
		{"ttl-based above floor uses cache-age", sync.CacheModeTTLBased, time.Hour, time.Hour},
		{"event-based below floor uses floor", sync.CacheModeEventBased, time.Minute, ttlFloor},
		{"no-cache ignores cache-age entirely", sync.CacheModeNoCache, 24 * time.Hour, ttlFloor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sync.EffectivePollInterval(tt.mode, tt.cacheAge, ttlFloor); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPlanNextDueAtAnchorsOnLastSuccess(t *testing.T) {
	last := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	// Now is INSIDE the window last+interval, so the anchor still means
	// something and the result is anchored on it.
	//
	// ── AMENDED IN PHASE 20.5 ────────────────────────────────────────────
	// This case previously used now = last + 1h against a 10-minute
	// interval, and asserted a due time of last + [10,20) minutes — i.e.
	// FORTY MINUTES IN THE PAST. The stated reasoning was that "a currently-
	// failing subscription doesn't drift later just because attempts keep
	// failing", which is right for a failing subscription and wrong for the
	// case that actually dominates: a route answering 304, whose
	// last_success_at is frozen by design (§6.2 resets bookkeeping only on
	// 200) and therefore recomputes the same past instant forever. Measured
	// live at commit 5ebbc56: 62 of 85 enabled subscriptions more than an
	// hour in the past, one at consecutive_304 = 2785.
	//
	// The anchor behaviour is unchanged where it means something — that is
	// this test — and clamped where it does not, which is
	// TestALongRunning304StreamNeverSchedulesInThePast in duetime_spin_test.go.
	now := last.Add(3 * time.Minute)
	rnd := rand.New(rand.NewSource(1))

	due, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:       sync.RouteCacheConfig{CacheMode: "ttl-based", CacheAge: 10 * time.Minute},
		Policy:      sync.PolicyConfig{TTLFloor: ttlFloor, BackoffCap: time.Hour},
		LastSuccess: last,
		Now:         now,
		Rand:        rnd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// due must be in [last+interval, last+2*interval) — anchored on
	// last_success, never on "now", so an early poll does not walk the
	// schedule forward by one round-trip on every cycle.
	interval := 10 * time.Minute
	if due.Before(last.Add(interval)) || !due.Before(last.Add(2*interval)) {
		t.Errorf("PlanNextDueAt = %s, want in [%s, %s)", due, last.Add(interval), last.Add(2*interval))
	}
	if !due.After(now) {
		t.Errorf("PlanNextDueAt = %s, which is not after now (%s) — that is the 304 spin", due, now)
	}
}

func TestPlanNextDueAtUsesNowWhenNeverSucceeded(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	due, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:  sync.RouteCacheConfig{CacheMode: "ttl-based", CacheAge: 10 * time.Minute},
		Policy: sync.PolicyConfig{TTLFloor: ttlFloor, BackoffCap: time.Hour},
		Now:    now, // LastSuccess left zero
		Rand:   rand.New(rand.NewSource(1)),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if due.Before(now) {
		t.Errorf("a never-succeeded subscription's next_due_at (%s) must not be before now (%s)", due, now)
	}
}

func TestPlanNextDueAtNoCacheRequiresOptIn(t *testing.T) {
	in := sync.DueTimeInput{
		Route:  sync.RouteCacheConfig{CacheMode: "not-cached"},
		Policy: sync.PolicyConfig{TTLFloor: ttlFloor},
		Now:    time.Now(),
	}
	if _, err := sync.PlanNextDueAt(in); err != sync.ErrNoCacheNotOptedIn {
		t.Errorf("expected ErrNoCacheNotOptedIn, got %v", err)
	}
	in.OptInNoCache = true
	if _, err := sync.PlanNextDueAt(in); err != nil {
		t.Errorf("opted-in no-cache subscription must be schedulable: %v", err)
	}
}

// TestFullJitterRange asserts every computed next_due_at falls inside
// [anchor+interval, anchor+2*interval) — the "+jitter" term is additive on
// top of the full interval, uniform in [0, interval), never negative and
// never so large the subscription is scheduled implausibly far out.
func TestFullJitterRange(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := time.Hour
	for i := 0; i < 200; i++ {
		due, err := sync.PlanNextDueAt(sync.DueTimeInput{
			Route:       sync.RouteCacheConfig{CacheMode: "ttl-based", CacheAge: interval},
			Policy:      sync.PolicyConfig{TTLFloor: time.Minute, BackoffCap: 24 * time.Hour},
			LastSuccess: anchor,
			Now:         anchor,
			Rand:        rnd,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if due.Before(anchor.Add(interval)) || !due.Before(anchor.Add(2*interval)) {
			t.Fatalf("iteration %d: due = %s outside [%s, %s)", i, due, anchor.Add(interval), anchor.Add(2*interval))
		}
	}
}
