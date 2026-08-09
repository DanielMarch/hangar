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
	return anchor.Add(interval).Add(FullJitter(interval, in.Rand)), nil
}
