package sync

import (
	"math"
	"math/rand"
	"time"
)

// BackoffMultiplier is the adaptive-backoff growth factor (§6.2: "1.5^n
// backoff on consecutive 304s"). It is spec-fixed, not configurable — a
// per-installation multiplier would make the "resets only on 200" contract
// harder to reason about for no operational benefit.
const BackoffMultiplier = 1.5

// maxSafeExponent bounds the loop in Backoff so a pathologically large
// consecutive-304 count (a stuck subscription that never sees a 200) can't
// run math.Pow into +Inf/overflow territory before the cap comparison ever
// runs. 1.5^200 already dwarfs any sane backoff_cap, so clamping the
// exponent here changes no observable behaviour.
const maxSafeExponent = 200

// Backoff computes the event-based cache-mode poll interval after
// consecutive304 consecutive ETag-revalidated 304s: base * 1.5^consecutive304,
// capped at cap. consecutive304 must be driven ONLY by consecutive 304s —
// never by consecutive 403s or any other counter — because resetting
// exclusively on a 200 is the entire point of the mechanism (§6.2). Callers
// reset the count by passing 0 the tick after a 200.
func Backoff(base time.Duration, consecutive304 int, backoffCap time.Duration) time.Duration {
	if base <= 0 {
		base = 0
	}
	if consecutive304 <= 0 || base == 0 {
		if base > backoffCap && backoffCap > 0 {
			return backoffCap
		}
		return base
	}
	n := consecutive304
	if n > maxSafeExponent {
		n = maxSafeExponent
	}
	grown := float64(base) * math.Pow(BackoffMultiplier, float64(n))
	if backoffCap > 0 && (grown > float64(backoffCap) || grown <= 0) {
		return backoffCap
	}
	return time.Duration(grown)
}

// FullJitter returns a uniformly random duration in [0, d). It is the
// "full jitter" the roadmap requires on every computed next_due_at
// (01_ARCHITECTURE.md §6.2) so that many subscriptions sharing the same
// interval don't synchronise into a herd. A nil rnd uses the package-level
// math/rand source (safe for concurrent use since Go 1.20). d <= 0 returns 0.
func FullJitter(d time.Duration, rnd *rand.Rand) time.Duration {
	if d <= 0 {
		return 0
	}
	if rnd == nil {
		return time.Duration(rand.Int63n(int64(d)))
	}
	return time.Duration(rnd.Int63n(int64(d)))
}
