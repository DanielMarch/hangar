package ratelimit

import "time"

// Clock abstracts time so the in-process ledger's window arithmetic is
// testable without sleeping, and so a clock jump on the host doesn't skew
// live-window accounting (01_ARCHITECTURE.md §5.5's edge case: "use a
// monotonic, injected clock for in-process window arithmetic"). The shared
// (clustered) path deliberately does NOT use this — it uses the database's
// own now() so every replica shares one clock (§5.6).
type Clock interface {
	Now() time.Time
}

// systemClock is the production Clock: time.Now() carries a monotonic
// reading as long as it is never round-tripped through wall-clock-only
// serialisation, which the in-process ledger never does.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock is the production Clock implementation.
var SystemClock Clock = systemClock{}
