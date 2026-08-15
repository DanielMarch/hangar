package handlers

import (
	"context"
	"encoding/json"
	"time"
)

// hooks.go holds the package-level seams through which a sync write
// reaches a LATER-PHASE subsystem, without internal/sync/handlers
// importing one.
//
// ── WHY SEAMS AND NOT IMPORTS ────────────────────────────────────────────
// This is the shape internal/rbac.PermissionsChangedHook established in
// Phase 11 (internal/rbac/hook.go) and for the same two reasons.
//
// The architectural one: internal/sync is Phases 6-9 and must not depend on
// internal/provisioning (Phase 11) or internal/alerting (Phase 14). The
// dependency runs one way — the later package sets the variable — so this
// package compiles, and its whole test suite runs, with no knowledge that
// either exists.
//
// The practical one: the handler signature is fixed by
// internal/sync/worker's dispatch table, which is a package-level map of
// `wrap(Parse, Sync)` pairs built at init. There is no constructor to pass
// a dependency to and no per-worker field to hang one on. A package-level
// variable is not a shortcut here, it is the only seam the dispatch table's
// shape leaves.
//
// ── THE FAILURE MODE THIS SHAPE HAS, STATED PLAINLY ──────────────────────
// A hook nobody sets is silent. Phase 20.3 found exactly that: `serve`
// mounted the entire RBAC mutation surface with PermissionsChangedHook
// still nil, so every role revocation performed through the API enqueued
// nothing, for two phases, with every test passing. Both hooks below are
// therefore set in ONE place — cmd/hangar/revocation.go's
// wireRevocationTriggers and cmd/hangar/alerting.go's wireAlertGeneration —
// which every process role calls, rather than in each role's own boot
// sequence where one can be forgotten.

// ObservedNotification is one row of app.character_notification that a
// sync pass ACTUALLY WROTE — a notification seen for the first time, or one
// whose read state changed.
//
// "Actually wrote" is the load-bearing part. ESI's notifications endpoint
// re-serves the same recent notifications on every poll, so acting on the
// response would re-offer dozens of already-known notifications every ten
// minutes per character. UpsertCharacterNotification's
// `WHERE t.is_read IS DISTINCT FROM EXCLUDED.is_read` guard already
// answers "is this new to us", by returning no row when it is not, and
// SyncCharacterNotifications reads that answer rather than recomputing it.
//
// Re-offering is still SAFE if it happens (a read-state flip does re-offer
// one), because the alert fingerprint is (type, notification_id, target)
// and app.alert_event's UNIQUE constraint absorbs the repeat — it is
// counted as suppressed_by_dedupe, which is one of Gate 3.1's accounting
// terms. Correctness does not depend on this filter; cost does.
type ObservedNotification struct {
	CharacterID int64
	// NotificationID is CCP's own id for the notification — the upstream's
	// primary key, and the semantic field the dedupe fingerprint uses.
	NotificationID int64
	// Type is the raw CCP notification type, unnormalised: an open
	// vocabulary value (Principle 14) that the consumer maps onto the
	// alert catalogue, registering it as unknown if it is not there.
	Type string
	// Payload is app.character_notification.payload verbatim: either the
	// parsed YAML structure or migration 00035's `{"raw": "..."}` wrapper
	// when CCP sent YAML no strict parser accepts. Both are renderable —
	// which is why a parse failure needs no special case downstream.
	Payload json.RawMessage
	// OccurredAt is CCP's own timestamp for the notification, not the time
	// it was synced. It anchors the coalescing window, so a burst of
	// structure notifications from one fleet action rolls up into one
	// message even if HANGAR learned about them minutes later.
	OccurredAt time.Time
}

// NotificationObservedHook, when set, is invoked once per notification
// SyncCharacterNotifications actually wrote. Phase 20.4 (defect B25) sets
// it to internal/alerting's ingest path, which is what finally gives
// §4.4's delivery pipeline something to deliver.
//
// ── ERRORS PROPAGATE, AND THAT IS DELIBERATE ─────────────────────────────
// §4.4's "CCP notification YAML shape changes must never halt the queue" is
// about PAYLOAD SHAPE, and the consumer honours it by construction: an
// unrecognised type is registered and boarded rather than rejected, and an
// unparseable payload renders generically. Neither produces an error here.
//
// What CAN produce one is the database being unable to write the alert
// event — and that is not a payload problem, it is the same class of
// failure as the notification upsert one statement earlier, which already
// propagates. Swallowing it would mean a sync pass reporting success
// having silently dropped a structure-under-attack alert. River retries the
// job; the upsert is idempotent; the fingerprint deduplicates. Failing is
// both safe and honest.
var NotificationObservedHook func(ctx context.Context, n ObservedNotification) error

// AffiliationChange is a character's corporation or alliance changing
// under a character-sheet sync — 04_RELEASE_GATES.md §2.3's trigger row 6,
// "Corporation / alliance departure".
//
// Previous* are nil when HANGAR had no prior record (a character whose
// sheet is being synced for the first time). That is NOT a departure:
// nothing was granted on the strength of an affiliation HANGAR never knew
// about, so there is nothing to take away.
type AffiliationChange struct {
	CharacterID           int64
	PreviousCorporationID *int64
	CorporationID         int64
	PreviousAllianceID    *int64
	AllianceID            *int64
}

// CorporationChanged reports whether the corporation actually moved.
func (c AffiliationChange) CorporationChanged() bool {
	return c.PreviousCorporationID != nil && *c.PreviousCorporationID != c.CorporationID
}

// AllianceChanged reports whether the alliance actually moved, treating
// "had one, now has none" and "had none, now has one" as changes.
func (c AffiliationChange) AllianceChanged() bool {
	switch {
	case c.PreviousAllianceID == nil && c.AllianceID == nil:
		return false
	case c.PreviousAllianceID == nil || c.AllianceID == nil:
		return true
	default:
		return *c.PreviousAllianceID != *c.AllianceID
	}
}

// AffiliationChangedHook, when set, is invoked by SyncCharacterSheet when a
// character's corporation or alliance has changed. Phase 20.4 sets it to
// the urgent-revocation path.
//
// ── WHY THE RBAC HOOK DOES NOT ALREADY COVER THIS ────────────────────────
// It looks as though it should: leaving a corporation reduces entitlements,
// and internal/rbac.PermissionsChangedHook fires on every entitlement
// recomputation. But that hook fires on RBAC MUTATIONS — a role granted, a
// squad joined, a grant withdrawn — and a corporation departure is none of
// those. It is an ESI SYNC WRITE: CCP changed a fact about the world and
// HANGAR wrote it down. No permission was granted or revoked, so nothing
// recomputes, so the hook never fires.
//
// The practical consequence, which is why this is a Gate 2 trigger row and
// not a nicety: a character who leaves the corporation that granted their
// Discord roles keeps those roles until the next BULK reconcile. §2.1's
// bound is 60 seconds at p99; a bulk reconcile is nightly.
//
// Errors propagate, for the same reason internal/rbac's hook propagates: an
// entitlement-reducing event whose revocation could not be enqueued must
// not be reported as a successful sync. The sheet upsert is idempotent and
// River retries.
var AffiliationChangedHook func(ctx context.Context, change AffiliationChange) error
