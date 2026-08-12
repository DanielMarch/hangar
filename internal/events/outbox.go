// Package events is SRS §4.9: a transactional outbox producing signed
// outbound webhooks for third-party consumers, and the sole extension
// mechanism for out-of-process integrations (§1.3).
//
// outbox.go is the producing half. The one property everything else rests
// on is stated in §4.9's second sentence — "Data mutation and webhook
// enqueue occur in the same transaction" — and it is not a nicety. An
// outbox row written AFTER the mutation commits can be lost to a crash in
// between, so the integration silently misses an event; an outbox row
// written BEFORE the mutation commits can survive a rollback, so the
// integration is told about something that never happened. Only sharing
// the transaction gives at-least-once delivery of things that actually
// occurred, and it is what makes the whole §4.9 contract worth anything.
//
// dispatch.go consumes what this produces; sign.go signs it; retry.go
// decides what to do when a receiver will not take it.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// Type is a HANGAR domain event name.
//
// This is a CLOSED vocabulary, which Principle 14 permits explicitly for
// vocabularies HANGAR itself owns (SRS v3.1, defect B11) — it is not a CCP
// vocabulary and nothing upstream can invent a value for it. Closed is the
// right choice here for a reason specific to webhooks: an endpoint's
// event_filter selects by exactly these strings, so a producer that
// misspells its own event type would not fail, it would deliver to nobody,
// and the integration would look merely quiet rather than broken. Publish
// rejects an unknown Type so that typo is a test failure instead.
//
// 02_DATABASE_SCHEMA.md calls the vocabulary "defined incrementally by
// phase". These are Phase 19's: the access-control changes, which are what
// an out-of-process integration actually has to react to.
type Type string

const (
	// TypeRoleGrantChanged — a role's (permission, effect) grant set
	// changed. Aggregate 'role', AggregateID the role uuid.
	TypeRoleGrantChanged Type = "rbac.role_grant.changed"
	// TypeUserRoleAssigned / TypeUserRoleRevoked — a role was attached to
	// or detached from a user directly. Aggregate 'user'.
	TypeUserRoleAssigned Type = "rbac.user_role.assigned"
	TypeUserRoleRevoked  Type = "rbac.user_role.revoked"
	// TypeSquadRoleChanged — a squad's role set changed, which changes the
	// effective permissions of every member. Aggregate 'squad'.
	TypeSquadRoleChanged Type = "rbac.squad_role.changed"
	// TypeSquadMembershipChanged — a character joined or left a squad.
	// Aggregate 'squad'.
	TypeSquadMembershipChanged Type = "rbac.squad_member.changed"
	// TypeRoleDeleted — a whole role was removed. Aggregate 'role'.
	TypeRoleDeleted Type = "rbac.role.deleted"
)

// knownTypes is the closed set Publish validates against.
var knownTypes = map[Type]bool{
	TypeRoleGrantChanged:       true,
	TypeUserRoleAssigned:       true,
	TypeUserRoleRevoked:        true,
	TypeSquadRoleChanged:       true,
	TypeSquadMembershipChanged: true,
	TypeRoleDeleted:            true,
}

// KnownTypes lists the vocabulary in sorted order — for the admin surface,
// the migration guide, and endpoint event_filter validation.
func KnownTypes() []string {
	out := make([]string, 0, len(knownTypes))
	for t := range knownTypes {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

// Known reports whether t is in the closed vocabulary.
func Known(t Type) bool { return knownTypes[t] }

// Event is one thing that happened, as a producer describes it.
//
// Payload is marshalled to JSON at Publish time rather than being carried
// as raw bytes, so a producer cannot accidentally enqueue a payload that
// fails to encode only once the dispatcher reaches it — by then the
// mutation has committed and there is nothing useful to do about it.
type Event struct {
	// Aggregate is the kind of thing that changed ('role', 'user',
	// 'squad'); AggregateID identifies which one. Together they let a
	// receiver correlate and de-duplicate without parsing Payload.
	Aggregate   string
	AggregateID string
	Type        Type
	Payload     any
}

func (e Event) validate() error {
	if e.Aggregate == "" {
		return fmt.Errorf("events: event has no aggregate")
	}
	if e.AggregateID == "" {
		return fmt.Errorf("events: event has no aggregate id")
	}
	if !Known(e.Type) {
		return fmt.Errorf("events: %q is not in the closed event vocabulary (see events.KnownTypes)", e.Type)
	}
	return nil
}

// Publish writes one outbox row using the handle s was built on.
//
// It is deliberately the caller's job to pass a Store that wraps a
// TRANSACTION — that is the whole point, and it is why this takes a
// *store.Store rather than a store.Pool. Given a pool-backed Store the
// insert commits on its own and §4.9's guarantee is gone. Callers that do
// not already own a transaction should use Transact below, which makes the
// mistake unrepresentable.
func Publish(ctx context.Context, s *store.Store, ev Event) (gen.AppOutboxEvent, error) {
	if err := ev.validate(); err != nil {
		return gen.AppOutboxEvent{}, err
	}
	payload := json.RawMessage("{}")
	if ev.Payload != nil {
		encoded, err := json.Marshal(ev.Payload)
		if err != nil {
			return gen.AppOutboxEvent{}, fmt.Errorf("events: encoding payload for %s: %w", ev.Type, err)
		}
		payload = encoded
	}
	row, err := s.InsertOutboxEvent(ctx, gen.InsertOutboxEventParams{
		Aggregate:   ev.Aggregate,
		AggregateID: ev.AggregateID,
		EventType:   string(ev.Type),
		Payload:     payload,
	})
	if err != nil {
		return gen.AppOutboxEvent{}, fmt.Errorf("events: enqueueing %s for %s %s: %w", ev.Type, ev.Aggregate, ev.AggregateID, err)
	}
	return row, nil
}

// Recorder collects the events a mutation wants to announce. Transact
// hands one to the mutating function and publishes what it collected on
// the SAME transaction, immediately before commit.
//
// Collecting rather than publishing inline is not just ergonomics: it
// means the mutation body cannot publish onto the wrong handle even by
// accident, because it never sees a Store it could publish onto except the
// transactional one, and Record does not touch the database at all.
type Recorder struct {
	events []Event
}

// Record queues an event to be published when the transaction commits.
func (r *Recorder) Record(ev Event) { r.events = append(r.events, ev) }

// Len is how many events have been recorded — for tests and for callers
// that want to skip work when nothing changed.
func (r *Recorder) Len() int { return len(r.events) }

// Transact runs fn inside one transaction and publishes every event fn
// recorded into that same transaction before committing.
//
// This is §4.9's guarantee expressed as a control-flow shape: returning a
// non-nil error from fn — or any failure publishing the events themselves
// — rolls back the mutation AND the outbox rows together, because they are
// literally the same transaction. There is no ordering a caller can choose
// that breaks it, which is the point of routing mutations through here
// rather than trusting each one to remember.
func Transact(ctx context.Context, pool store.Pool, fn func(ctx context.Context, s *store.Store, out *Recorder) error) error {
	var rec Recorder
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if err := fn(ctx, s, &rec); err != nil {
			return err
		}
		for _, ev := range rec.events {
			if _, err := Publish(ctx, s, ev); err != nil {
				return err
			}
		}
		return nil
	})
}

// PendingCount is how many outbox rows are still waiting to be fanned out
// — the admin surface's backlog number, and what a test asserts on to know
// the outbox has drained.
func PendingCount(ctx context.Context, s *store.Store) (int64, error) {
	n, err := s.CountUndispatchedOutboxEvents(ctx)
	if err != nil {
		return 0, fmt.Errorf("events: counting undispatched events: %w", err)
	}
	return n, nil
}

// EventID is a convenience for callers that only want the id back.
func EventID(row gen.AppOutboxEvent) uuid.UUID { return row.EventID }
