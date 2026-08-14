// Package breaker implements 01_ARCHITECTURE.md §5.8's two circuit
// breakers: a route-scoped 5XX breaker and an entity-scoped 403 breaker.
package breaker

import (
	"strconv"
	"sync"
	"time"
)

// DefaultRouteProbeTTL and DefaultEntityProbeTTL are the half-open probe
// intervals §5.8 calls "the route TTL".
//
// They live here, next to the breakers, because TWO places need to agree on
// them: cmd/hangar, which constructs the breakers, and internal/sync/worker,
// which decides how long to snooze a subscription whose breaker just
// refused a call. A worker snoozing for less than the probe interval wakes
// up only to be refused again and writes a sync_run row for the privilege;
// one snoozing for much more leaves the entity dark long after the circuit
// would have let a probe through. A shared constant is the only way those
// two numbers cannot drift apart.
//
// The entity interval is fifteen times the route one on purpose. A 5XX
// clears when ESI recovers, which can be seconds. Five consecutive 403s
// clear when somebody grants a corporation role in-game — a human action,
// measured in hours — and probing that every minute spends Governor 2's
// installation-wide error budget on a wait that is not ours to shorten.
const (
	DefaultRouteProbeTTL  = 60 * time.Second
	DefaultEntityProbeTTL = 15 * time.Minute
)

// State is the breaker's current disposition.
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// Clock abstracts time for testability, mirroring
// internal/esi/ratelimit.Clock (kept as a separate, identical interface
// rather than an import — this package must not depend on ratelimit, and a
// one-method interface isn't worth a shared package for).
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock is the production Clock.
var SystemClock Clock = systemClock{}

// circuit is one breaker's mutable state.
type circuit struct {
	mu               sync.Mutex
	state            State
	consecutive      int
	openedAt         time.Time
	halfOpenAttempts int
}

// RouteBreaker opens a route after >=10 consecutive 5XX responses on that
// route (§5.8). Route-scoped — not entity-scoped — because a 5XX indicates
// the route itself is unhealthy, not one caller's relationship to it.
type RouteBreaker struct {
	mu        sync.Mutex
	circuits  map[string]*circuit
	threshold int
	probeTTL  time.Duration
	clock     Clock
}

// NewRouteBreaker constructs a 5XX breaker. probeTTL is the route's TTL —
// the half-open probe interval (§5.8).
func NewRouteBreaker(probeTTL time.Duration, clock Clock) *RouteBreaker {
	if clock == nil {
		clock = SystemClock
	}
	return &RouteBreaker{circuits: make(map[string]*circuit), threshold: 10, probeTTL: probeTTL, clock: clock}
}

func (b *RouteBreaker) circuitFor(route string) *circuit {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.circuits[route]
	if !ok {
		c = &circuit{}
		b.circuits[route] = c
	}
	return c
}

// Allow reports whether a request to route may proceed. A half-open
// breaker allows exactly one probe request at a time.
func (b *RouteBreaker) Allow(route string) bool {
	c := b.circuitFor(route)
	c.mu.Lock()
	defer c.mu.Unlock()
	return allowLocked(c, b.probeTTL, b.clock)
}

// RecordSuccess resets the consecutive-failure count and closes the
// breaker — a probe that succeeds while half-open closes the circuit.
func (b *RouteBreaker) RecordSuccess(route string) {
	c := b.circuitFor(route)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecutive = 0
	c.state = StateClosed
}

// RecordFailure records a 5XX. The breaker opens once the consecutive count
// reaches the threshold (10).
func (b *RouteBreaker) RecordFailure(route string) {
	c := b.circuitFor(route)
	c.mu.Lock()
	defer c.mu.Unlock()
	recordFailureLocked(c, b.threshold, b.clock)
}

// State reports the breaker's current state for route (metrics/debugging).
func (b *RouteBreaker) State(route string) State {
	c := b.circuitFor(route)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// EntityBreaker opens on >=5 consecutive 403s for the same (route, entity)
// pair (§5.8). Entity-scoped on purpose: one director losing a corporation
// role must not break the route for every other corporation (Principle 3)
// — a 403 breaker opening is the signal that drives acting-character
// fallback (§6.3, a later phase).
type EntityBreaker struct {
	mu        sync.Mutex
	circuits  map[string]*circuit
	threshold int
	probeTTL  time.Duration
	clock     Clock
}

// NewEntityBreaker constructs a 403 breaker. probeTTL is the route's TTL —
// the half-open probe interval.
func NewEntityBreaker(probeTTL time.Duration, clock Clock) *EntityBreaker {
	if clock == nil {
		clock = SystemClock
	}
	return &EntityBreaker{circuits: make(map[string]*circuit), threshold: 5, probeTTL: probeTTL, clock: clock}
}

func entityKey(route string, entityID int64) string {
	// A NUL-joined key can't collide the way a "-"-joined one could if an
	// entity ID were ever negative and a route name contained a digit
	// sequence resembling one.
	return route + "\x00" + strconv.FormatInt(entityID, 10)
}

func (b *EntityBreaker) circuitFor(route string, entityID int64) *circuit {
	key := entityKey(route, entityID)
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.circuits[key]
	if !ok {
		c = &circuit{}
		b.circuits[key] = c
	}
	return c
}

// Allow reports whether a request for (route, entityID) may proceed.
func (b *EntityBreaker) Allow(route string, entityID int64) bool {
	c := b.circuitFor(route, entityID)
	c.mu.Lock()
	defer c.mu.Unlock()
	return allowLocked(c, b.probeTTL, b.clock)
}

// RecordSuccess resets and closes the breaker for (route, entityID).
func (b *EntityBreaker) RecordSuccess(route string, entityID int64) {
	c := b.circuitFor(route, entityID)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecutive = 0
	c.state = StateClosed
}

// RecordFailure records a 403 for (route, entityID). Opens at the
// threshold (5).
func (b *EntityBreaker) RecordFailure(route string, entityID int64) {
	c := b.circuitFor(route, entityID)
	c.mu.Lock()
	defer c.mu.Unlock()
	recordFailureLocked(c, b.threshold, b.clock)
}

// State reports the breaker's current state for (route, entityID).
func (b *EntityBreaker) State(route string, entityID int64) State {
	c := b.circuitFor(route, entityID)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// allowLocked and recordFailureLocked are shared between both breaker
// types (identical open/half-open/closed state machine; only the failure
// threshold and the key shape differ). Callers must hold c.mu.
func allowLocked(c *circuit, probeTTL time.Duration, clock Clock) bool {
	switch c.state {
	case StateClosed:
		return true
	case StateOpen:
		if clock.Now().Sub(c.openedAt) >= probeTTL {
			c.state = StateHalfOpen
			c.halfOpenAttempts = 1 // this call consumes the one probe slot
			return true
		}
		return false
	case StateHalfOpen:
		// Exactly one probe in flight at a time.
		if c.halfOpenAttempts > 0 {
			return false
		}
		c.halfOpenAttempts++
		return true
	default:
		return true
	}
}

func recordFailureLocked(c *circuit, threshold int, clock Clock) {
	c.consecutive++
	if c.state == StateHalfOpen {
		// A failed probe re-opens immediately and resets the probe
		// clock.
		c.state = StateOpen
		c.openedAt = clock.Now()
		c.consecutive = 0
		return
	}
	if c.consecutive >= threshold {
		c.state = StateOpen
		c.openedAt = clock.Now()
	}
}
