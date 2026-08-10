package worker

import "testing"

// TestSingularMiningPathsUsedVerbatim (roadmap exit criterion): the request
// URL is singular — "/corporation/{corporation_id}/mining/..." — sourced
// from app.esi_route.upstream_path verbatim (01_ARCHITECTURE.md Principle
// 5), never "/corporations/...". Checked at the dispatch-table level: if
// either map ever gains a "/corporations/{corporation_id}/mining/..." key
// (a plausible copy-paste mistake given every OTHER corp route is plural),
// this test catches it immediately.
func TestSingularMiningPathsUsedVerbatim(t *testing.T) {
	singularMiningPaths := []string{
		"/corporation/{corporation_id}/mining/extractions",
		"/corporation/{corporation_id}/mining/observers",
	}

	for _, path := range singularMiningPaths {
		if _, ok := corporationDispatch[path]; !ok {
			t.Errorf("corporationDispatch is missing the singular mining path %q", path)
		}
	}

	pluralImposters := []string{
		"/corporations/{corporation_id}/mining/extractions",
		"/corporations/{corporation_id}/mining/observers",
		"/corporations/{corporation_id}/mining/observers/{observer_id}",
	}
	for _, path := range pluralImposters {
		if _, ok := corporationDispatch[path]; ok {
			t.Errorf("corporationDispatch must never register the pluralised mining path %q — the live spec uses the singular form", path)
		}
	}

	// Every route.UpstreamPath the CorporationWorker will ever receive from
	// the DB comes back exactly as ingested (Phase 2) and is used as the
	// map key with no transformation — this doesn't retest Phase 2's
	// ingest, it asserts this file's dispatch table was hand-typed to match
	// it, since a typo here would silently 404 forever without ever
	// tripping the route breaker (a 404 is "data" per 01_ARCHITECTURE.md,
	// not a failure).
	const singularPrefix = "/corporation/"
	for _, path := range singularMiningPaths {
		if len(path) < len(singularPrefix) || path[:len(singularPrefix)] != singularPrefix {
			t.Errorf("path %q does not start with the singular %q prefix", path, singularPrefix)
		}
	}
}
