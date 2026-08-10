package worker

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
