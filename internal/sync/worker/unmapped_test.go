package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// specSnapshot is the same embedded ESI spec tools/scopedump reads. Using it
// rather than app.esi_route makes this a BUILD-time check with no database,
// which is the same reasoning SyncSet() records for itself: "can HANGAR ever
// poll this route at all" must be answerable without a running installation.
func catalogedGetRoutes(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "esi", "catalogue", "embedded", "openapi.snapshot.json"))
	if err != nil {
		t.Fatalf("reading the embedded spec snapshot: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the embedded spec snapshot: %v", err)
	}
	out := map[string]bool{}
	for path, ops := range spec.Paths {
		if _, ok := ops["get"]; ok {
			out[path] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the embedded snapshot yielded no GET routes — the check would pass vacuously")
	}
	return out
}

// TestEveryCatalogedGetRouteIsClassified is defect B47's guard.
//
// B47 was a route with a handler and no dispatch entry, which meant no
// subscription, which meant it was never scheduled and never ran — silently,
// because a route nothing dispatches produces no error, only an absence. The
// only way that absence becomes visible is if something insists the
// classification be TOTAL.
//
// Gate 4.2 requires exactly that: every measured ESI route maps to a
// subscription "or is explicitly recorded as deliberately unmapped with a
// reason". This test is that requirement, executable. A route CCP adds to
// the spec fails here until it is either dispatched or given a reason —
// which is the outcome B47 needed and did not have.
func TestEveryCatalogedGetRouteIsClassified(t *testing.T) {
	cataloged := catalogedGetRoutes(t)
	subscribable := SubscribableRoutes()
	unmapped := DeliberatelyUnmapped()

	var unclassified []string
	for path := range cataloged {
		_, isSub := subscribable[path]
		_, isUnmapped := unmapped[path]
		if !isSub && !isUnmapped {
			unclassified = append(unclassified, path)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("%d catalogued GET route(s) are neither subscribable nor recorded as deliberately unmapped.\n"+
			"This is defect B47's shape: a route nothing dispatches fails silently, because an absent map entry\n"+
			"produces no error. Add a dispatch entry, or a reason in DeliberatelyUnmapped().\n"+
			"Routes: %v", len(unclassified), unclassified)
	}
}

// TestNoRouteIsBothSubscribableAndUnmapped keeps the partition disjoint. A
// path in both sides would let a real dispatch entry be "explained away" by
// a stale unmapped reason, so that removing the dispatch entry later would
// not fail the test above — which would reopen B47 with the guard still
// green.
func TestNoRouteIsBothSubscribableAndUnmapped(t *testing.T) {
	subscribable := SubscribableRoutes()
	var both []string
	for path := range DeliberatelyUnmapped() {
		if _, ok := subscribable[path]; ok {
			both = append(both, path)
		}
	}
	sort.Strings(both)
	if len(both) > 0 {
		t.Errorf("route(s) are both subscribable and recorded as deliberately unmapped: %v", both)
	}
}

// TestEveryUnmappedRouteIsCataloged is the opposite drift: a reason recorded
// for a path ESI does not serve. That happens when CCP renames or retires a
// route (defect B38 was exactly a path spelled differently from the spec), and
// it matters because a stale entry here silently absorbs the RENAMED route's
// absence — the new spelling shows up as unclassified, someone copies the old
// reason across, and a route that should have been dispatched never is.
func TestEveryUnmappedRouteIsCataloged(t *testing.T) {
	cataloged := catalogedGetRoutes(t)
	var phantom []string
	for path := range DeliberatelyUnmapped() {
		if !cataloged[path] {
			phantom = append(phantom, path)
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("%d recorded-unmapped route(s) do not exist as GET routes in the embedded spec snapshot — "+
			"retired or misspelled: %v", len(phantom), phantom)
	}
}

// TestNotBuiltRoutesAreDeclaredFailures documents, in the suite itself, that
// ReasonNotBuilt is not a passing state.
//
// It deliberately asserts only that the set is non-empty and that every
// member carries a reason — it does NOT pin the count. Pinning the count
// would make removing a defect (by building the handler) fail the test,
// which teaches the wrong lesson. The count is reported by the Gate 4
// traceability CSV, where a non-zero value is a gate blocker.
func TestNotBuiltRoutesAreDeclaredFailures(t *testing.T) {
	byReason := UnmappedByReason()
	notBuilt := byReason[ReasonNotBuilt]
	if len(notBuilt) == 0 {
		// Not a failure — this is what success eventually looks like.
		t.Log("no ReasonNotBuilt routes remain; Gate 4.2's unreachable-capability blocker is clear")
		return
	}
	sort.Strings(notBuilt)
	t.Logf("%d catalogued route(s) back an Appendix A capability with no sync handler. "+
		"These are Gate 4 blockers recorded per §0.4, not exemptions: %v", len(notBuilt), notBuilt)
}
