package sso_test

// Phase 20.3's B27 wiring, at the unit level: the two reducing events the
// LOGIN path can observe, and the notification that turns each into a
// provision-urgent job.

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/sso"
)

// countingNotifier is the sso.RevocationNotifier double. cmd/hangar's real
// one enqueues to River; this one records, which is all an assertion needs.
type countingNotifier struct {
	characters []int64
	reasons    []string
}

func (n *countingNotifier) NotifyRevocation(_ context.Context, characterID int64, reason string) {
	n.characters = append(n.characters, characterID)
	n.reasons = append(n.reasons, reason)
}

// TestLifecycleNotifiesEveryReducingEvent is Gate 2 §2.3's trigger matrix
// rows 1 and 2 at the unit level: both must INVALIDATE and NOTIFY, and the
// reason recorded on the token must be the same open-vocabulary string the
// notification carries.
//
// The two travelling together is the point. Before 20.3 the notification
// existed (Refresher's hooks, wired to provisioning) and the invalidation
// existed (Lifecycle, wired to nothing), and no path did both for an
// owner-hash change.
func TestLifecycleNotifiesEveryReducingEvent(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid_grant", func(t *testing.T) {
		store := newFakeStore()
		notifier := &countingNotifier{}
		lifecycle := sso.NewLifecycle(store, notifier)

		require.NoError(t, lifecycle.InvalidateForInvalidGrant(ctx, 42))

		require.Equal(t, []int64{42}, notifier.characters)
		require.Equal(t, []string{sso.ReasonInvalidGrant}, notifier.reasons)
		requireInvalidatedWith(t, store, 42, sso.ReasonInvalidGrant)
	})

	t.Run("owner hash change", func(t *testing.T) {
		store := newFakeStore()
		notifier := &countingNotifier{}
		lifecycle := sso.NewLifecycle(store, notifier)

		require.NoError(t, lifecycle.InvalidateForOwnerHashChange(ctx, 7))

		require.Equal(t, []int64{7}, notifier.characters)
		require.Equal(t, []string{sso.ReasonOwnerHashChanged}, notifier.reasons)
		requireInvalidatedWith(t, store, 7, sso.ReasonOwnerHashChanged)
	})
}

// TestLifecycleDefaultsToANoopNotifier keeps NewLifecycle's nil contract:
// a caller that has no provisioning engine still gets the invalidation,
// which is the half that protects the installation.
func TestLifecycleDefaultsToANoopNotifier(t *testing.T) {
	store := newFakeStore()
	lifecycle := sso.NewLifecycle(store, nil)
	require.NoError(t, lifecycle.InvalidateForInvalidGrant(context.Background(), 1))
	requireInvalidatedWith(t, store, 1, sso.ReasonInvalidGrant)
}

// TestScopeReductionFiresTheHook is Gate 2 §2.3's trigger row 3, which had
// no producer of any kind before Phase 20.3.
//
// A re-login with a NARROWER scope set is entitlement-reducing: the token
// stays valid and refreshable, but everything HANGAR derived from the
// withdrawn scopes is now ungrounded. persistToken used to delete the old
// app.character_token_scope rows and insert the new ones without ever
// comparing them, so the reduction was invisible.
func TestScopeReductionFiresTheHook(t *testing.T) {
	sso1 := newFakeSSOServer(t, "test-client-id")
	defer sso1.close()
	store := newFakeStore()
	flow := buildFlow(t, sso1, store)

	var removedSeen [][]string
	flow.OnScopesReduced = func(_ context.Context, _ int64, removed []string) {
		sort.Strings(removed)
		removedSeen = append(removedSeen, removed)
	}

	wide := []string{
		"esi-characters.read_contacts.v1",
		"esi-wallet.read_character_wallet.v1",
		"esi-mail.read_mail.v1",
	}
	sso1.setScopes(wide)
	first := login(t, flow)
	require.Empty(t, removedSeen, "a FIRST login grants scopes; it cannot reduce them")

	// Same character, re-authorised with one scope withdrawn.
	narrow := []string{
		"esi-characters.read_contacts.v1",
		"esi-wallet.read_character_wallet.v1",
	}
	sso1.setScopes(narrow)
	second := login(t, flow)
	require.Equal(t, first.CharacterID, second.CharacterID)

	require.Len(t, removedSeen, 1, "the reduction must fire exactly once")
	require.Equal(t, []string{"esi-mail.read_mail.v1"}, removedSeen[0],
		"the hook must name the scope that was WITHDRAWN, not the whole new set")

	// And the stored set really is the narrow one — the hook observes, it
	// does not veto.
	stored, err := store.ListCharacterTokenScopes(context.Background(), second.CharacterID)
	require.NoError(t, err)
	sort.Strings(stored)
	require.Equal(t, []string{"esi-characters.read_contacts.v1", "esi-wallet.read_character_wallet.v1"}, stored)
}

// TestUnchangedOrWidenedScopesDoNotFireTheHook is the other half, and it
// is the one that keeps the trigger from becoming noise: re-authorising
// with the SAME scopes, or with MORE, revokes nothing. A hook that fired on
// every login would enqueue a revocation recompute per login on an
// installation where nothing had changed.
func TestUnchangedOrWidenedScopesDoNotFireTheHook(t *testing.T) {
	sso1 := newFakeSSOServer(t, "test-client-id")
	defer sso1.close()
	store := newFakeStore()
	flow := buildFlow(t, sso1, store)

	fired := 0
	flow.OnScopesReduced = func(context.Context, int64, []string) { fired++ }

	base := []string{"esi-characters.read_contacts.v1"}
	sso1.setScopes(base)
	login(t, flow)
	require.Zero(t, fired)

	// Identical set.
	login(t, flow)
	require.Zero(t, fired, "re-authorising with the same scopes reduces nothing")

	// Wider set.
	sso1.setScopes(append(append([]string{}, base...), "esi-mail.read_mail.v1"))
	login(t, flow)
	require.Zero(t, fired, "re-authorising with MORE scopes reduces nothing")
}

// login drives one full BeginLogin/HandleCallback round trip.
func login(t *testing.T, flow *sso.Flow) *sso.LoginResult {
	t.Helper()
	pending, err := flow.BeginLogin(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	result, err := flow.HandleCallback(context.Background(), pending.SessionID, "code", mustState(t, pending.RedirectURL))
	require.NoError(t, err)
	return result
}

func requireInvalidatedWith(t *testing.T, store *fakeStore, characterID int64, reason string) {
	t.Helper()
	tok, err := store.GetCharacterToken(context.Background(), characterID)
	require.NoError(t, err)
	require.False(t, tok.Valid, "the token must be marked invalid")
	require.NotNil(t, tok.InvalidReason)
	require.Equal(t, reason, *tok.InvalidReason)
}
