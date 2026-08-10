package discord

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// GlobalLimiter enforces Discord's flat 50 requests/second ceiling across
// the whole driver (01_ARCHITECTURE.md §9.3) — a hand-rolled fixed-window
// token count rather than pulling in golang.org/x/time/rate as a new
// dependency for one ceiling this package already needs to hand-roll
// everything else around (bucket accounting, invalid budget, Cloudflare
// detection all need the same raw-header access a generic rate library
// has no opinion on).
type GlobalLimiter struct {
	mu           sync.Mutex
	max          int
	windowStart  time.Time
	windowCount  int
	windowLength time.Duration
	pausedUntil  time.Time
	clock        Clock
}

// NewGlobalLimiter builds a limiter allowing max requests per second.
func NewGlobalLimiter(max int, clock Clock) *GlobalLimiter {
	if clock == nil {
		clock = SystemClock
	}
	return &GlobalLimiter{max: max, windowLength: time.Second, clock: clock}
}

// Wait blocks until a slot is available (or ctx is done), then consumes
// one. Discord's own X-RateLimit-Global signal (buckets.go's caller,
// after a response) pauses this same limiter for the Retry-After duration
// via Pause — a global 429 stops EVERYTHING, not just one bucket.
func (l *GlobalLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.clock.Now()
		if now.Before(l.pausedUntil) {
			resumeAt := l.pausedUntil
			l.mu.Unlock()
			select {
			case <-time.After(resumeAt.Sub(now)):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if now.Sub(l.windowStart) >= l.windowLength {
			l.windowStart, l.windowCount = now, 0
		}
		if l.windowCount < l.max {
			l.windowCount++
			l.mu.Unlock()
			return nil
		}
		resumeAt := l.windowStart.Add(l.windowLength)
		l.mu.Unlock()
		wait := resumeAt.Sub(now)
		if wait <= 0 {
			continue
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Pause blocks every future Wait call (across all callers, since the
// limiter is shared) until d has elapsed — the response to an observed
// X-RateLimit-Global (or X-RateLimit-Scope: global) 429.
func (l *GlobalLimiter) Pause(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	resumeAt := l.clock.Now().Add(d)
	if resumeAt.After(l.pausedUntil) {
		l.pausedUntil = resumeAt
	}
}

// bucketState is one Discord rate-limit bucket's last known state, keyed
// by the bucket's OWN identity (the X-RateLimit-Bucket header value) —
// never by URL (01_ARCHITECTURE.md §9.3: "Key on the returned
// X-RateLimit-Bucket, not on the URL").
type bucketState struct {
	remaining int
	resetAt   time.Time
}

// BucketLimiter tracks per-bucket remaining/reset state and the route ->
// bucket mapping discovered from live responses. A route's bucket is
// unknown until its first response, matching every real Discord client —
// there is no way to know a bucket in advance.
type BucketLimiter struct {
	mu          sync.Mutex
	routeBucket map[string]string       // route template -> last known bucket id
	buckets     map[string]*bucketState // bucket id -> state
	clock       Clock
}

// NewBucketLimiter constructs an empty limiter.
func NewBucketLimiter(clock Clock) *BucketLimiter {
	if clock == nil {
		clock = SystemClock
	}
	return &BucketLimiter{
		routeBucket: make(map[string]string),
		buckets:     make(map[string]*bucketState),
		clock:       clock,
	}
}

// Reserve blocks (respecting ctx) if route's last-known bucket is
// currently exhausted, then returns. A route with no known bucket yet
// (first call, or Update was never called for it) never blocks here —
// the response that follows is what teaches this limiter about the
// bucket.
func (l *BucketLimiter) Reserve(ctx context.Context, route string) error {
	l.mu.Lock()
	bucketID, known := l.routeBucket[route]
	if !known {
		l.mu.Unlock()
		return nil
	}
	state := l.buckets[bucketID]
	l.mu.Unlock()
	if state == nil || state.remaining > 0 {
		return nil
	}
	wait := state.resetAt.Sub(l.clock.Now())
	if wait <= 0 {
		return nil
	}
	select {
	case <-time.After(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Update records a response's rate-limit headers against route. bucketID
// empty means the route carries no bucket (some Discord routes don't
// rate-limit at all) — Update is then a no-op, leaving the route
// permanently "unknown" to Reserve.
func (l *BucketLimiter) Update(route, bucketID string, remaining int, resetAfter float64) {
	if bucketID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.routeBucket[route] = bucketID
	l.buckets[bucketID] = &bucketState{
		remaining: remaining,
		resetAt:   l.clock.Now().Add(secondsToDuration(resetAfter)),
	}
}

// parseResetAfter parses Discord's X-RateLimit-Reset-After header, a
// float in seconds (§9.3 edge case: "do not truncate it to an integer").
func parseResetAfter(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// secondsToDuration converts a float-seconds header value (Reset-After,
// Retry-After) to a time.Duration without truncating the fractional part.
func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
