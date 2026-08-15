package sync

import (
	"math/rand"
	"time"
)

// EffectivePollInterval implements the interval half of §6.2's four-case
// table: max(x-cache-age, ttl_floor) for ttl-based and event-based (which
// also makes x-cache-age:0 resolve to ttl_floor, never 0 — 0 < ttl_floor
// for any sane floor), and ttl_floor alone for no-cache, whose contract
// ("scheduled at ttl_floor only") doesn't reference x-cache-age at all.
// This does not apply event-based's consecutive-304 backoff or jitter —
// see PlanNextDueAt for the full pipeline.
func EffectivePollInterval(mode CacheMode, cacheAge, ttlFloor time.Duration) time.Duration {
	if mode == CacheModeNoCache {
		return ttlFloor
	}
	if cacheAge < ttlFloor {
		return ttlFloor
	}
	return cacheAge
}

// DueTimeInput bundles what PlanNextDueAt needs to compute a subscription's
// next poll time.
type DueTimeInput struct {
	Route  RouteCacheConfig
	Policy PolicyConfig
	// LastSuccess anchors ttl-based/event-based scheduling (§6.2:
	// "last_success + ..."). Zero means "never succeeded", in which case
	// Now is used instead — a brand-new subscription must not be
	// scheduled off the zero time.
	LastSuccess time.Time
	// Consecutive304 drives event-based's 1.5^n backoff. It must be fed
	// by a counter that resets on 200 and ONLY on 200 (§6.2) — that
	// contract lives in the caller (db/queries/sync_subscription.sql's
	// RecordSyncSuccess/RecordSync304), not here.
	Consecutive304 int
	// OptInNoCache mirrors app.sync_subscription.opt_in_no_cache.
	OptInNoCache bool
	Now          time.Time
	// Rand is the full-jitter source. nil uses math/rand's package-level
	// source (safe for concurrent use since Go 1.20).
	Rand *rand.Rand
}

// PlanNextDueAt composes ParseCacheMode, EffectivePollInterval, event-based
// adaptive backoff, and full jitter into the next_due_at a caller should
// persist — the single seam 01_ARCHITECTURE.md §6.2 describes as one
// formula per cache mode. ErrNoCacheNotOptedIn enforces the no-cache
// opt-in gate; every other mode always succeeds.
func PlanNextDueAt(in DueTimeInput) (time.Time, error) {
	mode := ParseCacheMode(in.Route.CacheMode)
	if mode == CacheModeNoCache && !in.OptInNoCache {
		return time.Time{}, ErrNoCacheNotOptedIn
	}

	interval := EffectivePollInterval(mode, in.Route.CacheAge, in.Policy.TTLFloor)
	if mode == CacheModeEventBased {
		interval = Backoff(interval, in.Consecutive304, in.Policy.BackoffCap)
	}

	anchor := in.LastSuccess
	if anchor.IsZero() {
		anchor = in.Now
	}
	next := anchor.Add(interval).Add(FullJitter(interval, in.Rand))

	// ── A DUE TIME IN THE PAST IS A SPIN, NOT A SCHEDULE (PHASE 20.5) ────
	//
	// Found by watching the live installation, not by reading. Measured at
	// commit 5ebbc56: 62 of 85 enabled subscriptions had next_due_at MORE
	// THAN AN HOUR IN THE PAST, one with consecutive_304 = 2785, and
	// app.sync_run held 106,818 rows for an installation that has existed
	// for two days.
	//
	// The mechanism is this line's anchor. On a 304, the worker passes
	// `LastSuccess: sub.LastSuccessAt` — and a 304 never updates
	// last_success_at, because §6.2 is explicit that a 304 "resets adaptive
	// backoff bookkeeping only on 200". So once a route has been answering
	// 304 for longer than its own interval, every subsequent 304 recomputes
	// THE SAME instant in the past, the planner's claim query (which orders
	// by next_due_at) finds it due on every 5-second tick forever, and the
	// subscription polls ESI continuously for data that has not changed.
	//
	// Two consequences, and the second is what surfaced it:
	//
	//   1. Governor 2's error budget is spent on requests nobody asked for
	//      (esi_error_limit_remaining sat at 97, not 100, on an idle
	//      installation), and Gate 1's whole premise is that HANGAR's
	//      request rate is what it intends.
	//
	//   2. A subscription stuck an hour in the past ALWAYS sorts ahead of
	//      one scheduled for the future, and the claim is LIMITed. So a
	//      NEWLY CREATED subscription can never be claimed at all while any
	//      stuck one exists — which is how this was found: Phase 20.5's
	//      fifteen new fan-out subscriptions were created, were due, and
	//      were never once polled.
	//
	// THE FIX IS HERE AND NOT AT THE CALL SITES, deliberately. Anchoring on
	// last_success is CORRECT for the case it was written for: a route whose
	// upstream cache expires an hour after the data was generated should be
	// re-polled an hour after that data, not an hour after we happened to
	// ask. That intent is preserved — the anchor only moves when it would
	// otherwise produce a time already past, which is exactly the case where
	// it has stopped meaning anything. Fixing the three 304 call sites
	// instead would have left the invariant unstated and reintroducible by
	// the fourth.
	//
	// last_success_at is deliberately NOT touched: it means "when did the
	// data last change", it is what the admin board shows, and §6.2's
	// reset-only-on-200 rule is about that column, not about this one.
	if !next.After(in.Now) {
		next = in.Now.Add(interval).Add(FullJitter(interval, in.Rand))
	}
	return next, nil
}
