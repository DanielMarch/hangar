package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// AcquireRequest identifies the bucket to acquire against. Group and
// UserKey together are the (group, userID) key from §5.5: UserKey is
// "applicationID:characterID" on authenticated routes, or
// "sourceIP"/"sourceIP:applicationID" on unauthenticated ones — the caller
// (the route handler layer, Phase 7+) composes it; this package treats it
// as an opaque string.
type AcquireRequest struct {
	Group          string
	UserKey        string
	MaxTokens      int
	Window         time.Duration
	RequestTimeout time.Duration
}

// Reservation is the handle returned by a successful Acquire. It carries
// everything Settle needs; callers must not construct one directly.
type Reservation struct {
	EntryID  uuid.UUID
	Group    string
	UserKey  string
	IssuedAt time.Time
	Deadline time.Time
}

// ErrRateLimited is returned by Acquire when the bucket has no headroom for
// even the worst-case reservation. RetryAt (below) carries when to retry.
var ErrRateLimited = errors.New("ratelimit: insufficient budget to reserve")

// RetryAtError wraps ErrRateLimited with the moment §5.5 says to retry at:
// the oldest live entry's consumed_at plus the window. The caller (the sync
// scheduler in later phases) snoozes the subscription until then; it must
// never spin.
type RetryAtError struct {
	RetryAt time.Time
}

func (e *RetryAtError) Error() string { return "ratelimit: insufficient budget; retry later" }
func (e *RetryAtError) Unwrap() error { return ErrRateLimited }

// Ledger is Governor 1's acquire/settle/reconcile contract. Both the solo
// (in-process) and clustered (Postgres-backed) implementations satisfy it,
// so callers above this package (and Governor1 in mode.go, which switches
// between them) never need to know which is active.
type Ledger interface {
	// Acquire reserves the worst-case cost for one request. On success it
	// returns a Reservation; on insufficient budget it returns a
	// *RetryAtError (via errors.As) and a nil Reservation.
	Acquire(ctx context.Context, req AcquireRequest) (*Reservation, error)

	// Settle records the observed cost for a reservation, re-stamping
	// consumed_at to the response time (§5.5's B9 correction).
	Settle(ctx context.Context, res *Reservation, cost int16, respondedAt time.Time) error

	// Reconcile applies a server-reported X-Ratelimit-Remaining reading
	// against the bucket — "the server always wins" (§5.5).
	Reconcile(ctx context.Context, group, userKey string, maxTokens int, serverRemaining int) error
}
