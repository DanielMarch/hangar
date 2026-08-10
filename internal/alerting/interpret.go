package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
)

// Notification is one row of app.character_notification, as
// internal/sync/handlers wrote it: the CCP type, CCP's own id, and the
// payload column — either the parsed YAML structure or the
// `{"raw": "<verbatim text>"}` wrapper a parse failure produced
// (migration 00035). Both shapes are renderable by
// internal/alerting/render.Generic, which is precisely why the fallback
// path needs no special case here.
type Notification struct {
	Type           string
	NotificationID int64
	Payload        json.RawMessage
	OccurredAt     time.Time
}

// Emitter writes alert events and their deliveries into the outbox. One
// Emitter is safe for concurrent use; it holds no per-event state.
type Emitter struct {
	// Pool is the database handle. Every emit runs in one transaction:
	// §4.4's "transactional outbox" means the event and its delivery rows
	// commit together or not at all, so a crash between them cannot leave
	// an event nobody will ever deliver.
	Pool store.Pool
	// Window is the coalescing window; zero selects DefaultCoalesceWindow.
	// A negative value disables coalescing entirely (every event delivers
	// on its own) — useful for tests and for an installation that would
	// rather have forty messages than one.
	Window time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

// Registration is the alert-type row to create if one does not exist yet.
// Only ever used for a type discovered at runtime — a seeded type already
// has its row, with a source_route_id a runtime registration could not
// supply.
type Registration struct {
	Domain   catalogue.Domain
	Category catalogue.Category
}

// EmitRequest is one alert occurrence, before routing.
type EmitRequest struct {
	// AlertType is the canonical (normalised) alert type name.
	AlertType string
	// Payload is stored verbatim on the event and is what the renderer
	// reads. Never validated against a shape (Principle 14).
	Payload json.RawMessage
	// OccurredAt anchors the coalescing window. It is NOT part of any
	// dedupe hash.
	OccurredAt time.Time
	// Fingerprint produces the dedupe fingerprint for one routed target.
	// A function rather than a value because §4.4's identity is per
	// (occurrence, target): the same notification routed to two audiences
	// is two events, each deduplicated independently.
	Fingerprint func(Target) Fingerprint
	// Register, when non-nil, creates the app.alert_type row first.
	Register *Registration
}

// EmitResult reports what one emit did. Every field is observable by a
// caller that wants to log or assert on the outcome; none of them is an
// error, because none of these outcomes is a failure.
type EmitResult struct {
	AlertType string
	// Known is false when the alert type was not in the build's catalogue.
	Known bool
	// Routed is false when no enabled routing rule matched — a normal
	// state meaning nobody has subscribed to this alert type yet.
	Routed bool
	// EventsRecorded counts events actually inserted (one per target).
	EventsRecorded int
	// EventsDeduplicated counts targets whose event already existed —
	// RecordAlertEvent's ON CONFLICT (dedupe_hash) DO NOTHING fired.
	EventsDeduplicated int
	// DeliveriesEnqueued counts outbox rows created.
	DeliveriesEnqueued int
	// OnUnknownBoard is true when this ingest recorded the type on
	// app.notification_unknown_type.
	OnUnknownBoard bool
}

// IngestNotification is the CCP-notification entry point: catalogue
// lookup, unknown-type handling, then Emit.
//
// ── AN UNRECOGNISED TYPE IS A NORMAL PATH, NOT AN ERROR ─────────────────
// §4.4 is explicit: "CCP notification YAML shape changes must never halt
// the queue. Unrecognised payloads render as generic key/value pairs and
// are logged to the unknown-types board. Per Principle 14, unknown
// notification types are ingested, never rejected." Three things follow,
// and all three happen here:
//
//  1. the type is recorded on app.notification_unknown_type (the board an
//     operator watches), with the payload as a sample;
//  2. an app.alert_type row is created for it, in domain "unknown" and
//     default_enabled=false. This is not cosmetic: app.alert_event.
//     alert_type has a foreign key, so WITHOUT that row the insert would
//     fail and the unrecognised notification would do exactly what §4.4
//     forbids — halt the queue. Registering it also makes the type
//     routable, so an operator who sees it on the board can send it
//     somewhere without waiting for a HANGAR release;
//  3. rendering falls through the template chain to render.Generic, since
//     an unknown type by definition has no template.
//
// A first sighting therefore registers and boards the type but delivers
// nothing, because no routing rule can exist yet for a type nobody had
// seen. The sighting after an operator routes it delivers normally, via
// the generic renderer. That two-step is the real operator workflow, and
// TestUnrecognisedTypeUsesGenericRenderer walks it end to end.
func (e *Emitter) IngestNotification(ctx context.Context, n Notification) (EmitResult, error) {
	name := catalogue.Normalize(n.Type)
	entry, known := catalogue.ByName(name)

	req := EmitRequest{
		AlertType:  name,
		Payload:    n.Payload,
		OccurredAt: n.OccurredAt,
		Fingerprint: func(t Target) Fingerprint {
			return NotificationFingerprint(name, n.NotificationID, t)
		},
	}

	result := EmitResult{AlertType: name, Known: known}

	if !known {
		s := store.New(e.Pool)
		if err := s.RecordUnknownNotificationType(ctx, name, n.Payload); err != nil {
			return result, fmt.Errorf("alerting: recording unknown notification type %q on the board: %w", name, err)
		}
		result.OnUnknownBoard = true
		req.Register = &Registration{
			Domain:   catalogue.DomainUnknown,
			Category: catalogue.CategoryESINotification,
		}
	} else if entry.Category != catalogue.CategoryThreshold {
		// A seeded non-threshold type: register defensively, so an
		// installation whose seed has not run yet still delivers rather
		// than failing a foreign key. DO NOTHING means this can never
		// overwrite the seeded row's domain, category or default_enabled.
		//
		// Threshold types are deliberately excluded: their rows carry a
		// NOT NULL source_route_id (enforced by the threshold_declares_
		// source CHECK) that a runtime registration cannot supply, so
		// inserting one here would violate the constraint. A threshold
		// alert whose row is missing is a seeding problem, and it should
		// surface as one.
		req.Register = &Registration{Domain: entry.Domain, Category: entry.Category}
	}

	emitted, err := e.Emit(ctx, req)
	if err != nil {
		return result, err
	}
	emitted.Known = known
	emitted.OnUnknownBoard = result.OnUnknownBoard
	return emitted, nil
}

// Emit resolves routing and writes the outbox rows for one occurrence, in
// a single transaction.
func (e *Emitter) Emit(ctx context.Context, req EmitRequest) (EmitResult, error) {
	result := EmitResult{AlertType: req.AlertType, Known: true}
	if req.Fingerprint == nil {
		return result, errors.New("alerting: emit requires a Fingerprint function")
	}
	occurredAt := req.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = e.now()
	}

	err := store.WithTx(ctx, e.Pool, func(ctx context.Context, s *store.Store) error {
		if req.Register != nil {
			if err := s.EnsureAlertType(ctx, req.AlertType, string(req.Register.Domain), string(req.Register.Category)); err != nil {
				return fmt.Errorf("alerting: registering alert type %q: %w", req.AlertType, err)
			}
		}

		routing, err := Resolve(ctx, s, req.AlertType)
		if err != nil {
			return err
		}
		if routing.IsEmpty() {
			// Nobody is subscribed. No event, no delivery — an event with
			// no deliveries is a row that can never be acted on, and
			// writing one per unrouted notification would grow
			// app.alert_event without bound for types nobody wants.
			return nil
		}
		result.Routed = true

		window := e.window()
		for _, target := range routing.Targets {
			key := NewCoalesceKey(target, req.AlertType, occurredAt, window)
			coalesceKey := key.String()

			event, err := s.RecordAlertEvent(ctx, gen.RecordAlertEventParams{
				AlertType:   req.AlertType,
				DedupeHash:  req.Fingerprint(target).Hash(),
				CoalesceKey: nullableString(coalesceKey),
				Payload:     req.Payload,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					// ON CONFLICT (dedupe_hash) DO NOTHING fired: this
					// occurrence has already been ingested for this
					// target. Re-reading the same notification on the next
					// poll is the common case, not an error.
					result.EventsDeduplicated++
					continue
				}
				return fmt.Errorf("alerting: recording alert event for %q: %w", req.AlertType, err)
			}
			result.EventsRecorded++

			// The delivery is not claimable until the coalescing window
			// closes, so a burst arrives at the pump as one group.
			var dueAt *time.Time
			if due := key.Due(window); !due.IsZero() {
				d := due
				dueAt = &d
			}

			for _, dest := range routing.Destinations[target] {
				if _, err := s.EnqueueAlertDelivery(ctx, event.EventID, dest.ChannelID, dueAt); err != nil {
					return fmt.Errorf("alerting: enqueueing delivery for event %s: %w", event.EventID, err)
				}
				result.DeliveriesEnqueued++
			}
		}
		return nil
	})
	if err != nil {
		return EmitResult{AlertType: req.AlertType}, err
	}
	return result, nil
}

func (e *Emitter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Emitter) window() time.Duration {
	switch {
	case e.Window < 0:
		return 0 // explicitly disabled
	case e.Window == 0:
		return DefaultCoalesceWindow
	default:
		return e.Window
	}
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
