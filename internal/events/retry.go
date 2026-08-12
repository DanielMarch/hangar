package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// RetryPolicy governs a webhook delivery's attempt budget and the
// endpoint-level circuit breaker.
//
// TWO CAPS, NOT ONE, and they answer different questions.
//
// MaxAttempts bounds ONE delivery: "how long do we keep trying to hand
// this particular event to this endpoint?" It stops a single event
// consuming the pump forever.
//
// MaxConsecutiveFailures bounds the ENDPOINT: "at what point do we accept
// that nobody is listening?" Without it, a receiver that has been gone for
// a month still costs MaxAttempts HTTP calls for every single event
// produced, forever — each delivery dead-letters correctly and in
// isolation looks fine, while in aggregate the installation is
// permanently, pointlessly hammering a dead host. That is exactly the
// roadmap's "an endpoint that is permanently down must not retain jobs
// forever", and a per-delivery cap alone does not achieve it.
type RetryPolicy struct {
	// MaxAttempts is the total number of send attempts one delivery gets
	// before it dead-letters. Zero selects DefaultMaxAttempts.
	MaxAttempts int
	// Base is the first retry's delay; each subsequent retry doubles it.
	Base time.Duration
	// Cap bounds the doubling.
	Cap time.Duration
	// MaxConsecutiveFailures is how many failures in a row across ALL
	// deliveries disable the endpoint. Reset to zero by any success. Zero
	// selects DefaultMaxConsecutiveFailures.
	MaxConsecutiveFailures int
	// Lease is how long a claimed delivery is hidden from other
	// dispatchers while its HTTP call is in flight. It must comfortably
	// exceed the HTTP client timeout, or a slow receiver produces a
	// duplicate delivery from a second dispatcher while the first is still
	// waiting. Zero selects DefaultLease.
	Lease time.Duration
}

// Defaults. An exhausted delivery gives up after roughly 15 minutes of
// waiting (1+2+4+8m across five attempts), matching internal/alerting's
// policy so an operator has one mental model for both queues. The endpoint
// breaker trips at 20 consecutive failures: high enough that a receiver
// restart or a brief outage never disables anything, low enough that a
// genuinely dead endpoint is switched off within one working day.
const (
	DefaultMaxAttempts            = 5
	DefaultRetryBase              = time.Minute
	DefaultRetryCap               = time.Hour
	DefaultMaxConsecutiveFailures = 20
	DefaultLease                  = 2 * time.Minute
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

func (p RetryPolicy) capped() time.Duration {
	if p.Cap <= 0 {
		return DefaultRetryCap
	}
	return p.Cap
}

func (p RetryPolicy) maxConsecutiveFailures() int {
	if p.MaxConsecutiveFailures <= 0 {
		return DefaultMaxConsecutiveFailures
	}
	return p.MaxConsecutiveFailures
}

func (p RetryPolicy) lease() time.Duration {
	if p.Lease <= 0 {
		return DefaultLease
	}
	return p.Lease
}

// Backoff returns the delay before attempt number `attempt` (1-based:
// Backoff(1) is the wait after the FIRST failure). Exponential and capped,
// without jitter — the same reasoning internal/alerting records: this
// spaces retries of one delivery against one endpoint, where jitter buys
// nothing and costs test determinism.
func (p RetryPolicy) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := p.base()
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= p.capped() {
			return p.capped()
		}
	}
	if delay > p.capped() {
		return p.capped()
	}
	return delay
}

// Decision is what to do with one delivery after a failed attempt.
type Decision struct {
	// DeadLetter is true when this attempt was the last one for this
	// delivery.
	DeadLetter bool
	// NextRetryAt is when to try again; zero when dead-lettering.
	NextRetryAt time.Time
	// Reason is stored in app.webhook_delivery.error, so the board says WHY.
	Reason string
}

// Decide applies the per-delivery half of the policy. attemptsBefore is the
// row's attempt value BEFORE this attempt.
//
// A permanent failure dead-letters immediately rather than spending the
// remaining budget re-proving the same 410 Gone.
func (p RetryPolicy) Decide(attemptsBefore int, err error, now time.Time) Decision {
	made := attemptsBefore + 1
	if IsPermanent(err) {
		return Decision{
			DeadLetter: true,
			Reason:     fmt.Sprintf("permanent failure on attempt %d, not retryable: %v", made, err),
		}
	}
	if made >= p.maxAttempts() {
		return Decision{
			DeadLetter: true,
			Reason:     fmt.Sprintf("exhausted %d attempts, last error: %v", made, err),
		}
	}
	return Decision{
		NextRetryAt: now.Add(p.Backoff(made)),
		Reason:      fmt.Sprintf("attempt %d of %d failed: %v", made, p.maxAttempts(), err),
	}
}

// Settle writes a Decision back. It is the only place a delivery leaves
// the claimable set on failure, so the invariant "every undelivered
// delivery is either owed another attempt or visibly dead-lettered" holds
// by construction — there is no third, invisible resting state.
func Settle(ctx context.Context, s *store.Store, deliveryID uuid.UUID, status *int16, d Decision) error {
	if d.DeadLetter {
		if err := s.MarkWebhookDeliveryFailed(ctx, deliveryID, status, &d.Reason); err != nil {
			return fmt.Errorf("events: dead-lettering delivery %s: %w", deliveryID, err)
		}
		return nil
	}
	next := d.NextRetryAt
	if err := s.MarkWebhookDeliveryRetry(ctx, gen.MarkWebhookDeliveryRetryParams{
		DeliveryID:     deliveryID,
		ResponseStatus: status,
		NextRetryAt:    &next,
		Error:          &d.Reason,
	}); err != nil {
		return fmt.Errorf("events: scheduling retry for delivery %s: %w", deliveryID, err)
	}
	return nil
}

// DisableReason is the text written to app.webhook_endpoint.disabled_reason
// and mirrored into the security log, phrased for the administrator who
// finds the endpoint switched off and has to decide what to do about it.
func DisableReason(failures int, lastErr error) string {
	return fmt.Sprintf("disabled automatically after %d consecutive delivery failures; last error: %v", failures, lastErr)
}

// SecurityLogAction is the app.security_log action recorded for an
// auto-disable.
const SecurityLogAction = "webhook.endpoint.auto_disabled"

// noteEndpointFailure bumps the endpoint breaker and, if it has tripped,
// disables the endpoint and records the administrator-visible notification.
//
// Returns whether the endpoint was disabled by this call.
//
// The notification goes to app.security_log rather than §4.4's alert
// pipeline: the alert catalogue is a measured parity artefact pinned at 54
// types, and its only category fitting an operational threshold requires
// an ESI source route that a dead webhook endpoint does not have. See
// migration 00041's note, which exists so this is not "fixed" later.
func noteEndpointFailure(ctx context.Context, s *store.Store, endpoint gen.AppWebhookEndpoint, policy RetryPolicy, cause error) (bool, error) {
	failures, err := s.RecordWebhookEndpointFailure(ctx, endpoint.EndpointID)
	if err != nil {
		return false, fmt.Errorf("events: recording failure for endpoint %s: %w", endpoint.EndpointID, err)
	}
	if int(failures) < policy.maxConsecutiveFailures() {
		return false, nil
	}

	reason := DisableReason(int(failures), cause)
	if err := s.DisableWebhookEndpoint(ctx, endpoint.EndpointID, &reason); err != nil {
		return false, fmt.Errorf("events: disabling endpoint %s: %w", endpoint.EndpointID, err)
	}

	// Everything still owed to this endpoint dead-letters with it. See
	// FailOutstandingDeliveriesForEndpoint: a disabled endpoint's queue is
	// unclaimable, so left pending those rows would be retained forever in
	// a state neither the pump nor the board can see.
	queuedReason := fmt.Sprintf("endpoint disabled before this delivery succeeded: %s", reason)
	if err := s.FailOutstandingDeliveriesForEndpoint(ctx, endpoint.EndpointID, &queuedReason); err != nil {
		return true, fmt.Errorf("events: dead-lettering the queue of disabled endpoint %s: %w", endpoint.EndpointID, err)
	}

	detail, err := json.Marshal(map[string]any{
		"endpoint_id":           endpoint.EndpointID,
		"url":                   endpoint.Url,
		"consecutive_failures":  failures,
		"reason":                reason,
		"remaining_deliveries":  "dead-lettered with the endpoint; re-enabling delivers new events, not the backlog",
		"admin_action_required": "verify the receiver, then re-enable the endpoint",
	})
	if err != nil {
		return true, fmt.Errorf("events: encoding security log detail for endpoint %s: %w", endpoint.EndpointID, err)
	}

	target := endpoint.Url
	owner := uuid.NullUUID{UUID: endpoint.OwnerUserID, Valid: true}
	if err := s.RecordSecurityLogEntry(ctx, gen.RecordSecurityLogEntryParams{
		UserID: owner,
		Action: SecurityLogAction,
		Target: &target,
		// No client IP: an auto-disable is HANGAR's own decision, not a
		// request from anywhere. Recording a fabricated address would be
		// worse than recording none.
		IpAddress: nil,
		Detail:    detail,
	}); err != nil {
		return true, fmt.Errorf("events: recording auto-disable of endpoint %s: %w", endpoint.EndpointID, err)
	}
	return true, nil
}
