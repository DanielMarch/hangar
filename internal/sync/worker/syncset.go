package worker

import (
	"sort"

	"github.com/hangar-project/hangar/internal/sync"
)

// SubscribableRoutes maps every route that can carry a sync subscription of
// its own to the entity kind that subscription is scoped to.
//
// ── DERIVED, NEVER RESTATED (defect B42) ─────────────────────────────────
// The partition is read straight out of the three dispatch tables, because
// they are the only authority on what this process can actually sync. A
// hand-maintained second list would drift the first time a route moved
// between workers, and the drift would be silent in both directions: a
// route with a subscription and no handler never produces data, and a
// route with a handler and no subscription is never scheduled — which is
// B42 itself.
//
// ── WHY THIS IS NOT SyncSet() ────────────────────────────────────────────
// SyncSet() answers §4.4's build-time question, "can HANGAR ever poll this
// route at all", and therefore includes the detail fan-out paths. This
// answers a different question — "can a row in app.sync_subscription name
// this route" — and the answer for those paths is NO.
//
// Every fan-out path carries a SECOND dynamic path parameter beyond the
// owner: {mail_id}, {division}, {starbase_id}, {skyhook_id},
// {sovereignty_hub_id}, {observer_id}, {contract_id}, {project_id}. A
// subscription row has exactly one entity_id (bigint) and no second
// identifier column, so it cannot express "for each mail this character
// has". Those routes are fetched by the parent route's own sync, which
// knows the id list because it just read it. Giving them subscriptions
// would create rows the planner would claim and the worker could not
// resolve to a URL.
//
// A fresh map is returned per call; callers may mutate it freely.
func SubscribableRoutes() map[string]sync.EntityKind {
	out := make(map[string]sync.EntityKind, len(characterDispatch)+len(corporationDispatch)+len(globalDispatch))
	for path := range characterDispatch {
		out[path] = sync.EntityCharacter
	}
	for path := range corporationDispatch {
		out[path] = sync.EntityCorporation
	}
	for path := range globalDispatch {
		out[path] = sync.EntityGlobal
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
// actually sync: the union of the three workers' dispatch tables
// (characterDispatch, corporationDispatch, globalDispatch) and the detail
// fan-out paths that are handled by a special case rather than a dispatch
// entry (mail bodies, starbase/skyhook/sovereignty-hub detail, mining
// observer records, contract items/bids, project contributions, and the
// per-division wallet routes).
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
// A fresh map is returned per call; callers may mutate it freely.
func SyncSet() map[string]bool {
	set := make(map[string]bool, len(characterDispatch)+len(corporationDispatch)+len(globalDispatch)+16)

	for path := range characterDispatch {
		set[path] = true
	}
	for path := range corporationDispatch {
		set[path] = true
	}
	for path := range globalDispatch {
		set[path] = true
	}

	// Routes handled outside the dispatch maps. Each needs a dynamic
	// per-item path parameter the plain handler shape cannot carry, so
	// each is special-cased in its worker's Work/doSync — but each is
	// every bit as much "in the sync set" as a dispatch-table entry, and
	// the starbase DETAIL route in particular is the source §4.4 names for
	// corporation.starbase.fuel_low.
	for _, path := range []string{
		mailBodyPath,
		walletJournalPath,
		walletTransactionsPath,
		starbaseDetailPath,
		skyhookDetailPath,
		sovereigntyHubDetailPath,
		miningObserverRecordsPath,
		contractItemsPath,
		contractBidsPath,
		projectContributionsPath,
	} {
		set[path] = true
	}

	return set
}
