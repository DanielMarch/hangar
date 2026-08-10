// ratelimit.go is a small in-process, per-user fixed-window rate limiter —
// the same shape as internal/api/v1/public_mumble_auth.go's IP-keyed
// limiter (Phase 13), generalised to a uuid.UUID key so Phase 15's
// authenticated routes (support/search in particular — SRS §6.7: "applies
// a per-user rate limit") can reuse it instead of redefining the pattern a
// third time.
package middleware

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// UserRateLimiter is a fixed-window limiter keyed by user id.
type UserRateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	counts map[uuid.UUID]*windowCount
}

type windowCount struct {
	windowStart time.Time
	count       int
}

// NewUserRateLimiter returns a limiter allowing max calls per window, per
// user.
func NewUserRateLimiter(max int, window time.Duration) *UserRateLimiter {
	return &UserRateLimiter{max: max, window: window, counts: make(map[uuid.UUID]*windowCount)}
}

// Allow reports whether userID is still within budget, consuming one unit
// if so.
func (l *UserRateLimiter) Allow(userID uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	wc, ok := l.counts[userID]
	if !ok || now.Sub(wc.windowStart) >= l.window {
		wc = &windowCount{windowStart: now, count: 0}
		l.counts[userID] = wc
	}
	if wc.count >= l.max {
		return false
	}
	wc.count++
	return true
}
