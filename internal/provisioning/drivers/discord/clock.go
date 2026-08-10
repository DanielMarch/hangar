package discord

import "time"

// Clock abstracts time so bucket/budget window arithmetic is testable
// without sleeping — internal/esi/ratelimit.Clock's shape, duplicated
// rather than imported so this package carries no dependency on
// internal/esi (a wholly different upstream; the only thing genuinely
// shared is "installation-wide Postgres-backed counter with a fixed
// window", which budget.go's own doc comment explains).
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock is the production Clock implementation.
var SystemClock Clock = systemClock{}
