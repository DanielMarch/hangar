package scopes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ── DEFECT B37 ───────────────────────────────────────────────────────────
// internal/api/v1/auth.go called BeginLogin with an empty []string{} at
// both call sites, so every authorization URL HANGAR ever produced carried
// `scope=`. EVE SSO's contract for that is explicit: an application
// requesting no scopes authenticates the user and is issued NO REFRESH
// TOKEN. So the whole authenticated sync layer had nothing to call with,
// regardless of what the operator ticked in the developer portal — and
// nothing failed, because a login that returns a session looks like a
// login that worked.
//
// This file derives the set instead of hard-coding it. Two sources, in
// order of authority, mirroring how catalogue.Boot already degrades:
//
//  1. app.esi_route_scope, populated by catalogue ingest from the LIVE
//     spec. This is the one that honours "the spec is the schedule": a
//     scope newly attached upstream to a route already in the sync set is
//     requested at the next login, with no code change.
//  2. the embedded snapshot, when the catalogue has not been ingested yet.
//     `serve` ingests in the background at startup, so a login racing a
//     cold boot must not be handed an empty set — which would silently
//     reproduce B37 on exactly the fresh installation that can least
//     afford it.
//
// A hard-coded Go list was the third option and is the one not taken: it
// would be correct on the day it was written and wrong the first time CCP
// moved a scope, in a way no test would notice.

// FromSpec derives the scopes required to READ the given upstream paths,
// from an OpenAPI 3.x document.
//
// GET-only, for the reason recorded at length in db/queries/esi_route.sql:
// ESI declares write scopes on the same paths as the reads, so sweeping
// every operation on a path asks users for permissions HANGAR cannot use.
//
// A path in `paths` that the spec does not contain is returned in
// `missing` rather than ignored. That is not defensive tidiness — it is
// how defect B38 (two sync-set paths that had been pluralised into
// non-existence) becomes visible instead of silently contributing no
// scopes.
func FromSpec(specBytes []byte, paths []string) (required []string, missing []string, err error) {
	var doc struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(specBytes, &doc); err != nil {
		return nil, nil, fmt.Errorf("scopes: parsing spec: %w", err)
	}

	set := map[string]struct{}{}
	for _, path := range paths {
		// app.esi_route.upstream_path is stored verbatim (Principle 5) and
		// the spec is inconsistent about trailing slashes, so try both
		// rather than normalising one into the other.
		key := strings.TrimSuffix(path, "/")
		ops, ok := doc.Paths[key]
		if !ok {
			ops, ok = doc.Paths[key+"/"]
		}
		if !ok {
			missing = append(missing, path)
			continue
		}
		for method, op := range ops {
			if !strings.EqualFold(method, "get") {
				continue
			}
			for _, requirement := range op.Security {
				for _, list := range requirement {
					for _, scope := range list {
						set[scope] = struct{}{}
					}
				}
			}
		}
	}

	required = make([]string, 0, len(set))
	for scope := range set {
		required = append(required, scope)
	}
	sort.Strings(required)
	sort.Strings(missing)
	return required, missing, nil
}
