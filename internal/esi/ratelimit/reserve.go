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
	Group   string
	UserKey string

	// MaxTokens is the route's REAL, advertised ceiling — the spec's
	// x-rate-limit max-tokens, or the server's own X-Ratelimit-Limit once
	// one has been observed. It is the value PERSISTED as the bucket's
	// max_tokens, and therefore the value every consumer of bucket state
	// reads: Reconcile's "the server always wins" comparison and
	// ListLedgerDivergence's local_remaining both measure against it.
	//
	// It must never carry a call-site policy reduction. See
	// AdmissionMaxTokens.
	MaxTokens int

	// AdmissionMaxTokens is the ceiling THIS CALL may admit against, when
	// the caller holds part of the bucket back for somebody else. Zero —
	// the correct value for every caller that reserves nothing — means
	// "the same as MaxTokens".
	//
	// ── PHASE 20.3, AND THE REASON THIS FIELD EXISTS ────────────────────
	// §4.4's permanent five-token char-notification reserve is a call-site
	// policy: internal/sync/worker.BackgroundRateLimitMax hands the
	// background poller 15-5=10 so an interactive caller always finds
	// headroom. Until 20.3 that reduced number was passed as MaxTokens,
	// and both ledgers implement admission by comparing consumption
	// against the ceiling they have STORED — so the reserve worked only
	// because the fiction was written to app.esi_ledger_bucket.max_tokens
	// (via UpsertLedgerBucket) and to the solo bucket's own field.
	//
	// Two things were wrong with that, and one of them was measurable on
	// a healthy installation:
	//
	//   - esi_ledger_divergence computes local_remaining as
	//     max_tokens - consumed and compares it with the server's
	//     X-Ratelimit-Remaining, which the server reports against 15. A
	//     stored ceiling of 10 therefore produced a PERMANENT divergence
	//     of exactly 5 on char-notification, against Gate 1.3's tolerance
	//     of 1, on an installation with nothing wrong with it.
	//   - a bucket row is shared by every caller, so a per-CALLER ceiling
	//     has no business in it. An interactive caller asking for the real
	//     15 flipped max_tokens back, the next background call flipped it
	//     to 10 again, and UpsertLedgerBucket's IS DISTINCT FROM guard
	//     makes each flip a real write. Two callers, one row, permanent
	//     thrash.
	//
	// Splitting the two values fixes both: what is STORED is the truth,
	// what is ADMITTED against is the policy, and the policy never
	// outlives the call that carried it.
	AdmissionMaxTokens int

	Window         time.Duration
	RequestTimeout time.Duration
}

// admissionCeiling resolves AdmissionMaxTokens' zero value. It deliberately
// does NOT clamp against MaxTokens: each ledger clamps against the ceiling
// it has actually stored, which is the only one that can be trusted to be
// current (the stored value may have been reconciled from the server's
// X-Ratelimit-Limit since this caller read the catalogue).
func (r AcquireRequest) admissionCeiling() int {
	if r.AdmissionMaxTokens <= 0 {
		return r.MaxTokens
	}
	return r.AdmissionMaxTokens
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
