package sso

import "context"

// LifecycleStore is the subset of Store lifecycle operations need.
type LifecycleStore interface {
	InvalidateCharacterToken(ctx context.Context, characterID int64, invalidReason *string) error
}

// RevocationNotifier is fired for every entitlement-reducing token
// invalidation, so a later phase's provisioning engine can drive its
// <60s revocation SLO (§10.2/Gate 2) off it. Phase 5 has no provisioning
// engine to wire up yet — NoopRevocationNotifier is the default — but the
// call site exists now so Phase 11 only has to implement the interface,
// never retrofit a call site into this package.
type RevocationNotifier interface {
	NotifyRevocation(ctx context.Context, characterID int64, reason string)
}

// NoopRevocationNotifier discards every notification. The zero value is
// ready to use.
type NoopRevocationNotifier struct{}

func (NoopRevocationNotifier) NotifyRevocation(context.Context, int64, string) {}

// Lifecycle groups the token-invalidation operations 01_ARCHITECTURE.md
// §7.2/§7.3 describe as entitlement-reducing events: an owner_hash change
// (character transferred to another EVE account) and an invalid_grant
// response from a refresh attempt. Both immediately invalidate every
// stored token for the affected character and notify Revocation.
//
// ── PHASE 20.3: HOW THIS RELATES TO refresh.go, AND WHY BOTH EXIST ──────
// refresh.go invalidates INLINE, inside the pg_advisory_xact_lock
// transaction that discovered the failure, because §7.3 requires the
// invalidation to commit with the read that found it — a Lifecycle call
// there would be a second connection racing the transaction that is about
// to rewrite the same row. That invalidation cannot move, so Lifecycle
// does not replace it.
//
// Lifecycle is for the callers that hold no such transaction:
//
//   - the LOGIN path (callback.go's resolveUser detects an owner-hash
//     change before any token is written). Until 20.3 that path only
//     NOTIFIED and never invalidated, so §7.2's "every stored token for
//     that character must be invalidated immediately" simply did not
//     happen on the one path a transferred character actually takes.
//   - the post-commit hooks in cmd/hangar/work.go. There the STORE call is
//     an idempotent re-assertion of what the transaction already committed
//     and the NOTIFY call is the new work. It is wired through Lifecycle
//     rather than straight to the notifier deliberately: Refresher exposes
//     OnInvalidGrant as a public extension point, and
//     RefreshCharacterToken and EnsureAccessToken each invalidate
//     independently in their own copy of that branch. A future third path
//     that fires the hook without invalidating first is exactly the defect
//     class this phase exists to close, and this makes the hook correct on
//     its own terms rather than dependent on its caller.
//
// The two can no longer DISAGREE about what they write: the invalid_reason
// vocabulary below is the single definition, and refresh.go uses it.
type Lifecycle struct {
	Store      LifecycleStore
	Revocation RevocationNotifier
}

// NewLifecycle constructs a Lifecycle. revocation defaults to
// NoopRevocationNotifier when nil.
func NewLifecycle(store LifecycleStore, revocation RevocationNotifier) *Lifecycle {
	if revocation == nil {
		revocation = NoopRevocationNotifier{}
	}
	return &Lifecycle{Store: store, Revocation: revocation}
}

// ReasonOwnerHashChanged and ReasonInvalidGrant are open-vocabulary values
// for app.character_token.invalid_reason — never an ENUM
// (02_DATABASE_SCHEMA.md's explicit comment on that column).
//
// Exported since Phase 20.3 so refresh.go's inline invalidations use the
// same two strings rather than their own copies. They were duplicated
// string literals in three places, agreeing only by coincidence; an
// operator reading app.character_token.invalid_reason has no way to tell a
// typo'd fourth value from a real one.
const (
	ReasonOwnerHashChanged = "owner_hash_changed"
	ReasonInvalidGrant     = "invalid_grant"
)

// InvalidateForOwnerHashChange handles §7.2's transfer edge case: the
// `owner` claim changed, meaning the character was transferred to a
// different EVE account. Every stored token for the character is
// invalidated immediately and Revocation is notified — this is
// entitlement-reducing (the new owner has none of the old owner's
// HANGAR-side authorization) and therefore a provision-urgent trigger.
func (l *Lifecycle) InvalidateForOwnerHashChange(ctx context.Context, characterID int64) error {
	reason := ReasonOwnerHashChanged
	if err := l.Store.InvalidateCharacterToken(ctx, characterID, &reason); err != nil {
		return err
	}
	l.Revocation.NotifyRevocation(ctx, characterID, ReasonOwnerHashChanged)
	return nil
}

// InvalidateForInvalidGrant handles §7.3's do-not-retry case: EVE SSO
// rejected a refresh attempt outright.
func (l *Lifecycle) InvalidateForInvalidGrant(ctx context.Context, characterID int64) error {
	reason := ReasonInvalidGrant
	if err := l.Store.InvalidateCharacterToken(ctx, characterID, &reason); err != nil {
		return err
	}
	l.Revocation.NotifyRevocation(ctx, characterID, ReasonInvalidGrant)
	return nil
}
