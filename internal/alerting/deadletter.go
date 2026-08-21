package alerting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// Delivery states, matching app.alert_delivery's CHECK constraint
// (migration 00008).
//
// ── WHY 'failed' IS NEVER WRITTEN ───────────────────────────────────────
// The CHECK allows four states; this phase writes three. A row left in
// 'failed' would be invisible to BOTH the pump (ClaimPendingAlertDeliveries
// only claims 'pending') and the dead-letter board (which lists
// 'dead_letter'), making it an alert that was "neither delivered nor
// dead-lettered" — §4.4's own definition of the single way an alert can be
// lost. A retryable failure therefore stays 'pending' with a future
// next_attempt_at, and an exhausted one goes straight to 'dead_letter'.
// The value stays in the CHECK because removing it would need a migration
// for no functional gain; it is simply unreachable, and this comment is
// here so nobody adds it back thinking it was an oversight.
const (
	StatePending    = "pending"
	StateSent       = "sent"
	StateDeadLetter = "dead_letter"
)

// RetryPolicy governs how many attempts a delivery gets and how far apart
// they are. §4.4 requires an SMTP failure to "retry with backoff and
// eventually dead-letter — never block the queue"; the same policy applies
// to every channel, since a Slack outage and a mail-server outage are the
// same problem from the pump's point of view.
type RetryPolicy struct {
	// MaxAttempts is the total number of send attempts before the
	// delivery dead-letters. Zero selects DefaultMaxAttempts.
	MaxAttempts int
	// Base is the first retry's delay; each subsequent retry doubles it.
	Base time.Duration
	// Cap bounds the doubling.
	Cap time.Duration
	// DeadLetterAfter dead-letters a delivery once it has been queued this
	// long, however few attempts it has made (HANGAR_ALERT_DEAD_LETTER_
	// AFTER, 24h). Orthogonal to MaxAttempts and not redundant with it:
	// with a capped exponential backoff a delivery can sit pending for
	// days while still having attempts left, and an alert nobody has seen
	// in a day is not going to become useful — it belongs on the board
	// where an operator can see it, not in a queue where they cannot.
	// Zero disables the age bound.
	DeadLetterAfter time.Duration
	// Lease is how long a CLAIMED delivery is hidden from other pumps
	// while its send is in flight (HANGAR_ALERT_LEASE, 2m). Zero selects
	// DefaultLease. PHASE 23 (N-10).
	//
	// Two numbers bound it from either side, and both are worth stating
	// because neither is enforced by a type:
	//
	//   * It must comfortably EXCEED the channel timeout — 15s for the
	//     webhook channels, 30s for SMTP — or a slow receiver produces a
	//     duplicate message from a second pump while the first is still
	//     waiting.
	//   * It must exceed the time ONE Tick spends between its claim and
	//     its last settle, which is roughly (groups × channel timeout) in
	//     the worst case. The operator's knob for that is ClaimSize, not
	//     this: a pass that claims fewer deliveries settles them sooner.
	//     Dispatcher.Tick logs a warning when a pass outruns its own
	//     lease, so the condition is observable rather than silent.
	//
	// A LONGER lease is not free either. It is how long a delivery waits
	// after a pump crashes mid-send before another pump may retry it, so
	// it is also the worst-case added latency on an alert during a
	// restart. Two minutes matches internal/events.DefaultLease
	// deliberately: an operator should have one mental model for both
	// queues.
	Lease time.Duration
}

// Defaults chosen so an exhausted delivery dead-letters within about half
// an hour of the first failure (1m + 2m + 4m + 8m ≈ 15m of waiting across
// five attempts) — long enough to ride out a mail server restart or a
// Slack incident, short enough that an operator watching the board learns
// about a genuinely broken channel the same shift.
const (
	DefaultMaxAttempts = 5
	DefaultRetryBase   = time.Minute
	DefaultRetryCap    = time.Hour
	DefaultLease       = 2 * time.Minute
)

func (p RetryPolicy) maxAttempts() int {
	if p.MaxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return p.MaxAttempts
}

func (p RetryPolicy) base() time.Duration {
	if p.Base <= 0 {
		return DefaultRetryBase
	}
	return p.Base
}

func (p RetryPolicy) cap() time.Duration {
	if p.Cap <= 0 {
		return DefaultRetryCap
	}
	return p.Cap
}

// Lease resolves the claim lease, applying DefaultLease to a zero. Exported
// because the dispatcher is not the only legitimate caller: a gate runner
// or an operator tool that claims deliveries directly must use the same
// window the pump does, and a second copy of the default is a second thing
// that can drift.
func (p RetryPolicy) LeaseWindow() time.Duration {
	if p.Lease <= 0 {
		return DefaultLease
	}
	return p.Lease
}

// Backoff returns the delay before attempt number `attempt` (1-based:
// Backoff(1) is the wait after the FIRST failure). Exponential, capped,
// and deliberately WITHOUT jitter: unlike the sync planner's route
// scheduling — where jitter spreads many independent subscriptions across
// the window to avoid a thundering herd against ESI — an alert delivery
// backoff spaces out retries of ONE row against ONE endpoint, where
// jitter buys nothing and costs test determinism.
func (p RetryPolicy) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := p.base()
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= p.cap() {
			return p.cap()
		}
	}
	if delay > p.cap() {
		return p.cap()
	}
	return delay
}

// Decision is what to do with a delivery after a failed attempt.
type Decision struct {
	// DeadLetter is true when this attempt was the last one.
	DeadLetter bool
	// NextAttemptAt is when to retry; zero when dead-lettering.
	NextAttemptAt time.Time
	// Reason explains the decision, and is stored in
	// app.alert_delivery.error so the board says WHY, not just that it
	// failed.
	Reason string
}

// Decide applies the retry policy to one failed attempt. attemptsBefore is
// the row's attempts value BEFORE this attempt (so the attempt just made
// was number attemptsBefore+1); queuedAt is the delivery's created_at.
//
// A permanent error dead-letters immediately rather than burning the
// remaining budget re-proving the same 404 — see channels.PermanentError.
func (p RetryPolicy) Decide(attemptsBefore int, queuedAt time.Time, err error, now time.Time) Decision {
	made := attemptsBefore + 1

	if channels.IsPermanent(err) {
		return Decision{
			DeadLetter: true,
			Reason:     fmt.Sprintf("permanent failure on attempt %d, not retryable: %v", made, err),
		}
	}
	if p.DeadLetterAfter > 0 && !queuedAt.IsZero() && now.Sub(queuedAt) >= p.DeadLetterAfter {
		return Decision{
			DeadLetter: true,
			Reason: fmt.Sprintf("queued for %s (limit %s) after %d attempts, last error: %v",
				now.Sub(queuedAt).Truncate(time.Second), p.DeadLetterAfter, made, err),
		}
	}
	if made >= p.maxAttempts() {
		return Decision{
			DeadLetter: true,
			Reason:     fmt.Sprintf("exhausted %d attempts, last error: %v", made, err),
		}
	}
	return Decision{
		NextAttemptAt: now.Add(p.Backoff(made)),
		Reason:        fmt.Sprintf("attempt %d of %d failed: %v", made, p.maxAttempts(), err),
	}
}

// Settle applies a Decision to the database. It is the ONLY place a
// delivery leaves the pending state on failure, so the invariant "every
// unsuccessful delivery is either still pending with a future attempt, or
// on the dead-letter board" holds by construction.
func Settle(ctx context.Context, s *store.Store, deliveryID uuid.UUID, d Decision) error {
	params := gen.MarkAlertDeliveryFailedParams{
		DeliveryID: deliveryID,
		State:      StatePending,
		Error:      &d.Reason,
	}
	if d.DeadLetter {
		params.State = StateDeadLetter
	} else {
		next := d.NextAttemptAt
		params.NextAttemptAt = &next
	}
	if err := s.MarkAlertDeliveryFailed(ctx, params); err != nil {
		return fmt.Errorf("alerting: settling delivery %s: %w", deliveryID, err)
	}
	return nil
}

// DeadLetterEntry is one row of the admin-visible dead-letter queue.
type DeadLetterEntry = gen.ListDeadLetterAlertDeliveriesRow

// DeadLetterBoard reads the dead-letter queue — §4.4's "admin-visible
// dead-letter queue", and the reason dead-lettering counts as a visible
// outcome rather than a loss. Phase 15's API layer serves this; it lives
// here so the admin surface and the pump agree on what "dead-lettered"
// means without one importing the other's HTTP types.
func DeadLetterBoard(ctx context.Context, s *store.Store, limit int32) ([]DeadLetterEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	entries, err := s.ListDeadLetterAlertDeliveries(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("alerting: reading the dead-letter board: %w", err)
	}
	return entries, nil
}

// DeadLetterCount is the board's badge number.
func DeadLetterCount(ctx context.Context, s *store.Store) (int64, error) {
	n, err := s.CountDeadLetterAlertDeliveries(ctx)
	if err != nil {
		return 0, fmt.Errorf("alerting: counting dead-lettered deliveries: %w", err)
	}
	return n, nil
}

// Requeue returns one dead-lettered delivery to the queue with a fresh
// attempt budget — the administrator's action once the cause is fixed.
func Requeue(ctx context.Context, s *store.Store, deliveryID uuid.UUID) error {
	if err := s.RequeueDeadLetterAlertDelivery(ctx, deliveryID); err != nil {
		return fmt.Errorf("alerting: requeueing delivery %s: %w", deliveryID, err)
	}
	return nil
}
