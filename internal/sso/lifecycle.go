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

// invalidReasonOwnerChanged and invalidReasonInvalidGrant are open-vocabulary
// values for app.character_token.invalid_reason — never an ENUM
// (02_DATABASE_SCHEMA.md's explicit comment on that column).
const (
	invalidReasonOwnerChanged = "owner_hash_changed"
	invalidReasonInvalidGrant = "invalid_grant"
)

// InvalidateForOwnerHashChange handles §7.2's transfer edge case: the
// `owner` claim changed, meaning the character was transferred to a
// different EVE account. Every stored token for the character is
// invalidated immediately and Revocation is notified — this is
// entitlement-reducing (the new owner has none of the old owner's
// HANGAR-side authorization) and therefore a provision-urgent trigger.
func (l *Lifecycle) InvalidateForOwnerHashChange(ctx context.Context, characterID int64) error {
	reason := invalidReasonOwnerChanged
	if err := l.Store.InvalidateCharacterToken(ctx, characterID, &reason); err != nil {
		return err
	}
	l.Revocation.NotifyRevocation(ctx, characterID, invalidReasonOwnerChanged)
	return nil
}

// InvalidateForInvalidGrant handles §7.3's do-not-retry case: EVE SSO
// rejected a refresh attempt outright.
func (l *Lifecycle) InvalidateForInvalidGrant(ctx context.Context, characterID int64) error {
	reason := invalidReasonInvalidGrant
	if err := l.Store.InvalidateCharacterToken(ctx, characterID, &reason); err != nil {
		return err
	}
	l.Revocation.NotifyRevocation(ctx, characterID, invalidReasonInvalidGrant)
	return nil
}
