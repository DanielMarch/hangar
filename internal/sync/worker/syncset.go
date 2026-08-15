package worker

import (
	"sort"

	"github.com/hangar-project/hangar/internal/sync"
)

// fanoutRoutes maps every DETAIL route to the entity kind whose subscription
// drives it.
//
// ── DEFECT B30 (PHASE 20.5) ──────────────────────────────────────────────
// These paths were previously listed only inside SyncSet() and were
// deliberately EXCLUDED from SubscribableRoutes, on the reasoning that "a
// subscription row has exactly one entity_id and no second identifier
// column, so it cannot express 'for each mail this character has'". The
// premise is true and the conclusion was wrong: the subscription names the
// OWNER and the TEMPLATED path, and the second identifier is enumerated at
// work time from rows the parent list sync already landed — which is exactly
// what every do*Fanout function in this package does, and has done since
// Phase 8.1.
//
// The consequence of excluding them was total: internal/sync/subscribe
// reconciles against SubscribablePathsFor, so no fan-out subscription could
// be created, so the `case starbaseDetailPath:` arms in CharacterWorker.Work
// and CorporationWorker.Work were unreachable on every installation ever
// deployed. Measured at commit 5ebbc56: 70 enabled subscriptions, zero of
// them a fan-out path, and app.starbase_detail — §4.4's named source for
// corporation.starbase.fuel_low — with no writer.
//
// Each entry is a route whose worker has an explicit `case` for it. That is
// the same derivation discipline SubscribableRoutes already applied to the
// three dispatch tables: if a path is here and its worker has no case, the
// worker returns "no handler registered" loudly rather than silently
// producing nothing.
func fanoutRoutes() map[string]sync.EntityKind {
	return map[string]sync.EntityKind{
		// Character-owned detail fan-outs.
		mailBodyPath:            sync.EntityCharacter,
		calendarEventDetailPath: sync.EntityCharacter,
		calendarAttendeesPath:   sync.EntityCharacter,
		planetColonyDetailPath:  sync.EntityCharacter,

		// Corporation-owned detail and per-division fan-outs.
		walletJournalPath:         sync.EntityCorporation,
		walletTransactionsPath:    sync.EntityCorporation,
		starbaseDetailPath:        sync.EntityCorporation,
		skyhookDetailPath:         sync.EntityCorporation,
		sovereigntyHubDetailPath:  sync.EntityCorporation,
		miningObserverRecordsPath: sync.EntityCorporation,
		contractItemsPath:         sync.EntityCorporation,
		contractBidsPath:          sync.EntityCorporation,
		projectContributorsPath:   sync.EntityCorporation,

		// Global fan-out: one subscription, fanning out over the
		// (region_id, type_id) pairs this installation actually tracks.
		marketHistoryPath: sync.EntityGlobal,
	}
}

// SubscribableRoutes maps every route that can carry a sync subscription of
// its own to the entity kind that subscription is scoped to.
//
// ── DERIVED, NEVER RESTATED (defect B42) ─────────────────────────────────
// The partition is read straight out of the three dispatch tables and the
// fan-out registry, because they are the only authority on what this process
// can actually sync. A hand-maintained second list would drift the first
// time a route moved between workers, and the drift would be silent in both
// directions: a route with a subscription and no handler never produces
// data, and a route with a handler and no subscription is never scheduled —
// which is B42, and which is B30 all over again for the fan-out half.
//
// A fresh map is returned per call; callers may mutate it freely.
func SubscribableRoutes() map[string]sync.EntityKind {
	fanout := fanoutRoutes()
	out := make(map[string]sync.EntityKind, len(characterDispatch)+len(corporationDispatch)+len(globalDispatch)+len(fanout))
	for path := range characterDispatch {
		out[path] = sync.EntityCharacter
	}
	for path := range corporationDispatch {
		out[path] = sync.EntityCorporation
	}
	for path := range globalDispatch {
		out[path] = sync.EntityGlobal
	}
	for path, kind := range fanout {
		out[path] = kind
	}
	return out
}

// SubscribablePathsFor returns the subscribable routes for one entity kind,
// sorted, ready to hand to a query taking a text[] of upstream paths.
func SubscribablePathsFor(kind sync.EntityKind) []string {
	var paths []string
	for path, k := range SubscribableRoutes() {
		if k == kind {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// SyncSet returns every app.esi_route.upstream_path this process can
// actually sync.
//
// "The sync set" is 00_SRS_v3.1.md §4.4's own term for exactly this:
// "a threshold alert whose source route is not in the sync set is a
// build-time error". It is deliberately derived from the dispatch tables
// rather than from app.sync_subscription rows — a subscription is
// installation state that varies per deployment and only exists at
// runtime, whereas §4.4 requires a BUILD-time answer to "can HANGAR ever
// poll this route at all", and that is precisely what having a registered
// handler means. A route absent here has no handler, so no subscription to
// it could ever produce data no matter what an operator configures.
//
// ── PHASE 20.5: THIS IS NOW EXACTLY SubscribableRoutes ───────────────────
// It used to be strictly larger — the dispatch tables plus a hand-written
// list of ten fan-out paths, because those could not be subscribed to. Now
// that they can be, the two questions have the same answer and SyncSet is
// derived from the one list rather than restating half of it. The function
// stays, because §4.4's rule is about the SET and callers should keep asking
// for it by the spec's own name; what has gone is the second copy.
//
// A fresh map is returned per call; callers may mutate it freely.
func SyncSet() map[string]bool {
	routes := SubscribableRoutes()
	set := make(map[string]bool, len(routes))
	for path := range routes {
		set[path] = true
	}
	return set
}
