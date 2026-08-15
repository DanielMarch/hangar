package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/sync"
)

// ── DEFECT B30's REGRESSION GUARD ────────────────────────────────────────
//
// The defect was not that a fan-out route had no handler. Every one of them
// had a handler, a `case` in its worker's Work, and passing tests. The
// defect was that no subscription could ever NAME one, so the `case` was
// unreachable on every installation — and nothing anywhere asserted the
// connection between "this worker can handle path P" and "the reconciler
// will create a subscription for path P".
//
// These tests assert exactly that connection, in both directions.

// TestEveryFanoutRouteIsSubscribable is the direct inverse of the defect: a
// route this package fans out over must be one the reconciler creates a
// subscription for, or the fan-out never runs.
func TestEveryFanoutRouteIsSubscribable(t *testing.T) {
	subscribable := SubscribableRoutes()
	for path, kind := range fanoutRoutes() {
		got, ok := subscribable[path]
		require.Truef(t, ok, "fan-out route %s is not subscribable — internal/sync/subscribe will never create a subscription for it, so its worker case is unreachable (this is defect B30)", path)
		require.Equalf(t, kind, got, "fan-out route %s is subscribable as %q but fans out as %q", path, got, kind)
	}
}

// TestNoSubscribableRouteIsOrphaned is the other direction: a path the
// reconciler will create a subscription for must be one some worker can
// actually execute. An orphan produces a subscription the planner claims
// every cycle and the worker rejects with "no handler registered" — visible
// only as a failing job nobody reads.
func TestNoSubscribableRouteIsOrphaned(t *testing.T) {
	fanout := fanoutRoutes()
	for path, kind := range SubscribableRoutes() {
		if _, ok := fanout[path]; ok {
			continue
		}
		switch kind {
		case sync.EntityCharacter:
			_, ok := characterDispatch[path]
			require.Truef(t, ok, "%s is subscribable as a character route with neither a dispatch entry nor a fan-out", path)
		case sync.EntityCorporation:
			_, ok := corporationDispatch[path]
			require.Truef(t, ok, "%s is subscribable as a corporation route with neither a dispatch entry nor a fan-out", path)
		case sync.EntityAlliance:
			_, ok := allianceDispatch[path]
			require.Truef(t, ok, "%s is subscribable as an alliance route with neither a dispatch entry nor a fan-out", path)
		case sync.EntityGlobal:
			_, ok := globalDispatch[path]
			require.Truef(t, ok, "%s is subscribable as a global route with neither a dispatch entry nor a fan-out", path)
		default:
			t.Fatalf("%s is subscribable under unknown entity kind %q", path, kind)
		}
	}
}

// TestSyncSetIsExactlyTheSubscribableSet pins §4.4's set to the one list it
// is derived from. Before 20.5 SyncSet was strictly larger — it carried a
// hand-written second copy of the ten fan-out paths precisely because they
// were NOT subscribable — and that gap is where B30 lived.
func TestSyncSetIsExactlyTheSubscribableSet(t *testing.T) {
	set := SyncSet()
	subscribable := SubscribableRoutes()
	require.Len(t, set, len(subscribable))
	for path := range subscribable {
		require.Truef(t, set[path], "%s is subscribable but absent from the sync set", path)
	}
}

// TestB30RoutesAreAllReachable names the routes behind B30's thirteen
// unreachable handlers explicitly, so a future refactor that drops one is a
// failing test rather than a silent regression to the state this phase
// found.
func TestB30RoutesAreAllReachable(t *testing.T) {
	subscribable := SubscribableRoutes()

	// Twelve of the thirteen symbols sit behind these six routes.
	for path, wantKind := range map[string]sync.EntityKind{
		calendarEventDetailPath: sync.EntityCharacter, // Parse/SyncCalendarEventDetail
		calendarAttendeesPath:   sync.EntityCharacter, // Parse/SyncCalendarAttendees
		planetColonyDetailPath:  sync.EntityCharacter, // Parse/SyncPlanetColonyDetail
		projectContributorsPath: sync.EntityCorporation,
		marketPricesPath:        sync.EntityGlobal, // Parse/SyncMarketPrices
		marketHistoryPath:       sync.EntityGlobal, // Parse/SyncMarketHistory
	} {
		got, ok := subscribable[path]
		require.Truef(t, ok, "B30 route %s is not subscribable", path)
		require.Equal(t, wantKind, got, path)
	}

	// The thirteenth, handlers.ParseAssetNames, is deliberately NOT
	// subscribable: POST /{owner}/{id}/assets/names takes a request body and
	// there is nothing to poll it for. It runs as an enrichment of the
	// assets route, which must therefore itself be subscribable.
	require.Equal(t, sync.EntityCharacter, subscribable[characterAssetsPath],
		"the assets route carries the assets/names enrichment; if it stops being subscribable, handlers.ParseAssetNames is unreachable again")
	require.NotContains(t, subscribable, characterAssetNamesPath,
		"assets/names is a POST with a body — a subscription for it would be a polling schedule for a route that cannot be polled")
}

// TestTheUnnamedAssetSentinelIsNotAName pins what real ESI does, measured
// on the live installation the first time the assets/names enrichment ran:
// an item with no name comes back with the literal string "None", not an
// empty one. Three of four real assets were written with that as their name
// before this was caught.
func TestTheUnnamedAssetSentinelIsNotAName(t *testing.T) {
	require.Equal(t, "None", unnamedAssetSentinel,
		"if CCP changes this sentinel, every unnamed module gets a false name again — and it will look like a name")
}
