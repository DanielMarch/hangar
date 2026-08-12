package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// DefaultClaimSize bounds one pass of either loop.
const DefaultClaimSize = 200

// maxResponseBody is how much of a receiver's response body is read before
// the rest is discarded. Enough to put a useful excerpt in the error, small
// enough that a receiver answering with a gigabyte cannot exhaust the
// dispatcher.
const maxResponseBody = 4 << 10

// PermanentError marks a failure that will not become a success by being
// retried — a 4xx that is the receiver's settled opinion of the request.
type PermanentError struct {
	Reason string
	Err    error
}

func (e *PermanentError) Error() string {
	if e.Err != nil {
		return e.Reason + ": " + e.Err.Error()
	}
	return e.Reason
}

func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err (or anything it wraps) is permanent.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// Dispatcher turns committed outbox rows into signed HTTP deliveries.
//
// It runs as TWO passes with a durable boundary between them, and the split
// is the reason an event survives a crash:
//
//	fanOut()  — claim undispatched outbox rows, write one webhook_delivery
//	            per matching endpoint, mark the event dispatched. ALL IN ONE
//	            TRANSACTION. A crash anywhere inside it rolls back, and the
//	            event is still undispatched, so the next pass redoes it.
//	deliver() — lease pending deliveries, make the HTTP call, settle. The
//	            lease is committed BEFORE the call and the settle after, so
//	            a crash in between leaves a leased row that becomes
//	            claimable again when the lease expires.
//
// The naive single-pass alternative (read the event, POST it, mark it
// dispatched) loses events for real: a crash after the POST and before the
// mark re-sends, and a crash after marking and before the POST drops the
// event entirely with no record that it ever existed. Neither is fixable
// without the intermediate durable row.
type Dispatcher struct {
	Pool    store.Pool
	Keyring *crypto.Keyring
	// Client is the HTTP client used for deliveries. Defaults to one with
	// a bounded timeout; overridable so tests can substitute a transport
	// without a listening socket.
	Client *http.Client
	Policy RetryPolicy
	// ClaimSize bounds one pass; zero selects DefaultClaimSize.
	ClaimSize int32
	Now       func() time.Time
	Log       *slog.Logger
}

// TickResult summarises one pass, for logging and for tests.
type TickResult struct {
	EventsFannedOut   int
	DeliveriesQueued  int
	Claimed           int
	Sent              int
	Retried           int
	DeadLettered      int
	EndpointsDisabled int
}

// Tick runs one full pass: fan out, then deliver.
func (d *Dispatcher) Tick(ctx context.Context) (TickResult, error) {
	var result TickResult

	events, queued, err := d.FanOut(ctx)
	if err != nil {
		return result, err
	}
	result.EventsFannedOut, result.DeliveriesQueued = events, queued

	delivered, err := d.Deliver(ctx)
	if err != nil {
		return result, err
	}
	result.Claimed = delivered.Claimed
	result.Sent = delivered.Sent
	result.Retried = delivered.Retried
	result.DeadLettered = delivered.DeadLettered
	result.EndpointsDisabled = delivered.EndpointsDisabled
	return result, nil
}

// FanOut claims undispatched outbox events and materialises one
// webhook_delivery row per subscribed endpoint, marking each event
// dispatched — all inside a single transaction.
//
// "Dispatched" here means FANNED OUT, not delivered. That is the useful
// meaning: once the delivery rows exist, the event cannot be lost, because
// every remaining failure mode is a delivery's problem and every delivery
// is individually durable, retried and dead-letterable.
//
// An event that matches NO endpoint is still marked dispatched. It has
// nowhere to go, and leaving it pending forever would grow the table
// without bound and make the backlog metric meaningless — an installation
// with no webhook endpoints configured at all is the common case.
func (d *Dispatcher) FanOut(ctx context.Context) (events, queued int, err error) {
	err = store.WithTx(ctx, d.Pool, func(ctx context.Context, s *store.Store) error {
		claimed, err := s.ClaimUndispatchedOutboxEvents(ctx, d.claimSize())
		if err != nil {
			return fmt.Errorf("events: claiming undispatched events: %w", err)
		}
		for _, event := range claimed {
			endpoints, err := s.ListEndpointsForEvent(ctx, event.EventType)
			if err != nil {
				return fmt.Errorf("events: listing endpoints for %s: %w", event.EventType, err)
			}
			for _, endpoint := range endpoints {
				if _, err := s.EnqueueWebhookDelivery(ctx, endpoint.EndpointID, event.EventID); err != nil {
					return fmt.Errorf("events: queueing delivery of %s to %s: %w", event.EventID, endpoint.EndpointID, err)
				}
				queued++
			}
			if err := s.MarkOutboxEventDispatched(ctx, event.EventID); err != nil {
				return fmt.Errorf("events: marking %s dispatched: %w", event.EventID, err)
			}
			events++
		}
		return nil
	})
	if err != nil {
		// The transaction rolled back: nothing was fanned out, so report
		// nothing rather than the counts the doomed attempt reached.
		return 0, 0, err
	}
	return events, queued, nil
}

// DeliverResult summarises one delivery pass.
type DeliverResult struct {
	Claimed           int
	Sent              int
	Retried           int
	DeadLettered      int
	EndpointsDisabled int
}

// Deliver leases pending deliveries and sends each one.
//
// Nothing here sleeps or retries in place: a failing endpoint costs one
// attempt and a future next_retry_at, and the pass moves on. One dead
// receiver must never stall deliveries to every other endpoint.
func (d *Dispatcher) Deliver(ctx context.Context) (DeliverResult, error) {
	var result DeliverResult
	s := store.New(d.Pool)

	claimed, err := s.LeasePendingWebhookDeliveries(ctx, d.Policy.lease(), d.claimSize())
	if err != nil {
		return result, fmt.Errorf("events: leasing pending deliveries: %w", err)
	}
	result.Claimed = len(claimed)

	for _, delivery := range claimed {
		sent, retried, dead, disabled := d.deliverOne(ctx, s, delivery)
		result.Sent += sent
		result.Retried += retried
		result.DeadLettered += dead
		result.EndpointsDisabled += disabled
	}
	return result, nil
}

func (d *Dispatcher) deliverOne(ctx context.Context, s *store.Store, delivery gen.AppWebhookDelivery) (sent, retried, dead, disabled int) {
	now := d.now()

	endpoint, err := s.GetWebhookEndpoint(ctx, delivery.EndpointID)
	if err != nil {
		return d.settle(ctx, s, delivery, nil, &PermanentError{Reason: "endpoint no longer exists", Err: err}, now, nil)
	}
	event, err := s.GetOutboxEvent(ctx, delivery.EventID)
	if err != nil {
		return d.settle(ctx, s, delivery, nil, &PermanentError{Reason: "outbox event no longer exists", Err: err}, now, &endpoint)
	}

	secret, err := crypto.OpenWebhookSecret(d.Keyring, endpoint.EndpointID, crypto.Sealed{
		KeyVersion: int(endpoint.HmacKeyVersion),
		WrappedDEK: endpoint.HmacWrappedDek,
		Nonce:      endpoint.HmacNonce,
		Ciphertext: endpoint.HmacCiphertext,
	})
	if err != nil {
		// Undecryptable secret: retrying cannot help, and — importantly —
		// this must NOT be silently swallowed. An endpoint whose secret
		// cannot be opened is one whose master key rotated out from under
		// it, which an administrator has to know about.
		return d.settle(ctx, s, delivery, nil, &PermanentError{Reason: "cannot open endpoint HMAC secret", Err: err}, now, &endpoint)
	}

	body, err := Envelope{
		EventID:     event.EventID,
		EventType:   event.EventType,
		Aggregate:   event.Aggregate,
		AggregateID: event.AggregateID,
		OccurredAt:  event.OccurredAt,
		Payload:     event.Payload,
	}.Encode()
	if err != nil {
		return d.settle(ctx, s, delivery, nil, &PermanentError{Reason: "encoding delivery body", Err: err}, now, &endpoint)
	}

	status, err := d.post(ctx, endpoint.Url, body, secret, now, delivery)
	if err != nil {
		if d.Log != nil {
			d.Log.WarnContext(ctx, "events: webhook delivery failed",
				"endpoint_id", endpoint.EndpointID, "event_type", event.EventType,
				"attempt", delivery.Attempt+1, "status", status, "error", err)
		}
		return d.settle(ctx, s, delivery, status, err, now, &endpoint)
	}

	if err := s.MarkWebhookDeliverySent(ctx, delivery.DeliveryID, status); err != nil {
		if d.Log != nil {
			d.Log.ErrorContext(ctx, "events: marking delivery sent failed", "delivery_id", delivery.DeliveryID, "error", err)
		}
		return 0, 0, 0, 0
	}
	// A success clears the endpoint breaker: "consecutive" has to mean
	// consecutive, or a long-lived endpoint eventually accumulates enough
	// unrelated transient failures to be disabled while working perfectly.
	if err := s.ClearWebhookEndpointFailures(ctx, endpoint.EndpointID); err != nil && d.Log != nil {
		d.Log.WarnContext(ctx, "events: clearing endpoint failure count failed", "endpoint_id", endpoint.EndpointID, "error", err)
	}
	return 1, 0, 0, 0
}

// settle applies the retry decision and the endpoint breaker to one failed
// delivery. endpoint may be nil when the failure was that it could not be
// read at all — there is then nothing to bump.
func (d *Dispatcher) settle(ctx context.Context, s *store.Store, delivery gen.AppWebhookDelivery, status *int16, cause error, now time.Time, endpoint *gen.AppWebhookEndpoint) (sent, retried, dead, disabled int) {
	decision := d.Policy.Decide(int(delivery.Attempt), cause, now)
	if err := Settle(ctx, s, delivery.DeliveryID, status, decision); err != nil {
		if d.Log != nil {
			d.Log.ErrorContext(ctx, "events: settling failed delivery failed", "delivery_id", delivery.DeliveryID, "error", err)
		}
		return 0, 0, 0, 0
	}

	if endpoint != nil {
		wasDisabled, err := noteEndpointFailure(ctx, s, *endpoint, d.Policy, cause)
		if err != nil && d.Log != nil {
			d.Log.ErrorContext(ctx, "events: updating endpoint failure state failed", "endpoint_id", endpoint.EndpointID, "error", err)
		}
		if wasDisabled {
			disabled = 1
			if d.Log != nil {
				d.Log.ErrorContext(ctx, "events: webhook endpoint disabled automatically",
					"endpoint_id", endpoint.EndpointID, "url", endpoint.Url, "reason", DisableReason(d.Policy.maxConsecutiveFailures(), cause))
			}
		}
	}

	if decision.DeadLetter {
		return 0, 0, 1, disabled
	}
	return 0, 1, 0, disabled
}

// post makes the signed HTTP call. It returns the response status (nil if
// there was no response at all) and an error describing any non-2xx.
func (d *Dispatcher) post(ctx context.Context, url string, body, secret []byte, now time.Time, delivery gen.AppWebhookDelivery) (*int16, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, &PermanentError{Reason: "endpoint URL is not usable", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hangar-webhooks/1")
	req.Header.Set(SignatureHeader, Sign(secret, body, now))
	// The delivery id is stable across retries, so a receiver can
	// de-duplicate at-least-once delivery without parsing the body.
	req.Header.Set("X-Hangar-Delivery", delivery.DeliveryID.String())
	req.Header.Set("X-Hangar-Attempt", fmt.Sprint(delivery.Attempt+1))

	resp, err := d.client().Do(req)
	if err != nil {
		// Transport failure: no status, and retryable. The receiver may or
		// may not have processed it, which is exactly why the contract is
		// at-least-once and the delivery id is stable.
		return nil, fmt.Errorf("posting to endpoint: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	status := int16(resp.StatusCode)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &status, nil
	}

	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	detail := fmt.Sprintf("endpoint returned %d", resp.StatusCode)
	if trimmed := bytes.TrimSpace(excerpt); len(trimmed) > 0 {
		detail = fmt.Sprintf("%s: %s", detail, trimmed)
	}

	// 4xx is the receiver's settled opinion and retrying it is noise —
	// EXCEPT 408 (the receiver asked for a retry), 425 (too early) and 429
	// (rate limited), which are explicit "try again" answers that happen to
	// live in the 4xx range.
	switch {
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooEarly,
		resp.StatusCode == http.StatusTooManyRequests:
		return &status, errors.New(detail)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return &status, &PermanentError{Reason: detail}
	default:
		return &status, errors.New(detail)
	}
}

// Envelope is the JSON body of a delivery — the contract a third-party
// receiver parses, and the exact bytes the signature covers.
type Envelope struct {
	EventID     uuid.UUID       `json:"event_id"`
	EventType   string          `json:"event_type"`
	Aggregate   string          `json:"aggregate"`
	AggregateID string          `json:"aggregate_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Payload     json.RawMessage `json:"payload"`
}

// Encode produces the delivery body.
//
// The signature is computed over exactly these bytes, so a receiver MUST
// verify against the raw request body it received and never against a
// re-serialisation of a parsed object — any JSON library that reorders
// keys, changes number formatting or alters whitespace produces a
// different byte string and therefore a different HMAC. That is the single
// most common integration mistake, and deploy/verify-webhook-signature.sh
// exists to demonstrate the correct handling.
func (e Envelope) Encode() ([]byte, error) {
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}
	body, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("events: encoding envelope for %s: %w", e.EventID, err)
	}
	return body, nil
}

func (d *Dispatcher) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return defaultClient
}

// defaultClient's timeout must stay comfortably under RetryPolicy.Lease, or
// a slow receiver produces a duplicate delivery from a second dispatcher
// while the first call is still in flight.
var defaultClient = &http.Client{Timeout: 30 * time.Second}

func (d *Dispatcher) claimSize() int32 {
	if d.ClaimSize > 0 {
		return d.ClaimSize
	}
	return DefaultClaimSize
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// DeadLetterEntry is one row of the admin-visible webhook dead-letter
// board.
type DeadLetterEntry = gen.ListDeadLetterWebhookDeliveriesRow

// DeadLetterBoard reads deliveries that will never be retried.
func DeadLetterBoard(ctx context.Context, s *store.Store, limit int32) ([]DeadLetterEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	entries, err := s.ListDeadLetterWebhookDeliveries(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("events: reading the webhook dead-letter board: %w", err)
	}
	return entries, nil
}
