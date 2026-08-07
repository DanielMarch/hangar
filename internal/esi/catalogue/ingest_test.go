package catalogue

import (
	"os"
	"testing"
	"time"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	return b
}

var farFuturePin = mustParseTestDate("2999-01-01") // never blocks anything in these fixtures

func mustParseTestDate(s string) time.Time {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// TestIngestMapsAllOperations — operation count > 0 floor; every operation
// has a row (Phase 2 exit criterion). Runs against both the trimmed
// testdata fixture and the full embedded snapshot (225 real ESI
// operations at the time this snapshot was captured).
func TestIngestMapsAllOperations(t *testing.T) {
	spec := readFixture(t, "../../../testdata/esi/openapi.minimal.json")
	routes, err := ParseSpec(spec, farFuturePin)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least one route from openapi.minimal.json")
	}
	wantOps := []string{
		"GetCorporationCorporationIdMiningExtractions",
		"GetCorporationCorporationIdMiningObservers",
		"GetCorporationCorporationIdMiningObserversObserverId",
		"GetAlliancesAllianceId",
		"GetCharactersAccessListsListing",
		"GetCorporationsCorporationIdProjectsProjectId",
		"GetMarketsPrices",
	}
	got := map[string]bool{}
	for _, r := range routes {
		got[r.OperationID] = true
	}
	for _, op := range wantOps {
		if !got[op] {
			t.Errorf("expected operation %s to have a row; it did not", op)
		}
	}

	embedded, meta, err := LoadEmbeddedSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := meta.DMaxDate()
	if err != nil {
		t.Fatal(err)
	}
	embeddedRoutes, err := ParseSpec(embedded, pin)
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddedRoutes) < 100 {
		t.Fatalf("expected a substantial number of routes from the real ESI snapshot, got %d", len(embeddedRoutes))
	}
}

// TestIngestZeroOperationsIsAFailure — a truncated download that maps zero
// operations must be a failure, not an empty success (roadmap Phase 2 edge
// case): it must never silently wipe an existing catalogue.
func TestIngestZeroOperationsIsAFailure(t *testing.T) {
	emptySpec := []byte(`{"openapi":"3.1.0","info":{"title":"empty","version":"1"},"paths":{}}`)
	_, err := ParseSpec(emptySpec, farFuturePin)
	if err == nil {
		t.Fatal("expected an error for a spec with zero operations, got nil")
	}
}

// TestUpstreamPathStoredVerbatim — the singular-path fixture
// (/corporation/{corporation_id}/mining/extractions) round-trips exactly,
// never pluralised or otherwise derived (Phase 2 exit criterion,
// 01_ARCHITECTURE.md §5.3).
func TestUpstreamPathStoredVerbatim(t *testing.T) {
	spec := readFixture(t, "../../../testdata/esi/openapi.minimal.json")
	routes, err := ParseSpec(spec, farFuturePin)
	if err != nil {
		t.Fatal(err)
	}

	wantPaths := map[string]bool{
		"/corporation/{corporation_id}/mining/extractions":             false,
		"/corporation/{corporation_id}/mining/observers":               false,
		"/corporation/{corporation_id}/mining/observers/{observer_id}": false,
	}
	for _, r := range routes {
		if _, tracked := wantPaths[r.UpstreamPath]; tracked {
			wantPaths[r.UpstreamPath] = true
		}
		// The defect this test guards against: any code path that derives
		// upstream_path from HANGAR's own (plural) "/corporations/..."
		// convention rather than storing the spec's own key verbatim.
		if r.OperationID == "GetCorporationCorporationIdMiningExtractions" && r.UpstreamPath != "/corporation/{corporation_id}/mining/extractions" {
			t.Errorf("upstream_path must be singular 'corporation', got %q", r.UpstreamPath)
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("expected upstream_path %q to round-trip verbatim; it was not present", path)
		}
	}
}

// TestUnknownCacheModeDefaultsToTtlBased — route ingested, value recorded,
// scheduling is ttl-based (Phase 2 exit criterion; Gate 6 condition (d)).
func TestUnknownCacheModeDefaultsToTtlBased(t *testing.T) {
	// SchedulingMode itself: an unrecognised value, and the absence of a
	// value, both resolve to "ttl-based" — only the two HANGAR-recognised
	// alternates ("event-based", "not-cached") take a different path.
	novel := "quantum-entangled"
	if got := SchedulingMode(&novel); got != "ttl-based" {
		t.Errorf("SchedulingMode(%q) = %s, want ttl-based", novel, got)
	}
	if got := SchedulingMode(nil); got != "ttl-based" {
		t.Errorf("SchedulingMode(nil) = %s, want ttl-based", got)
	}
	eventBased := "event-based"
	if got := SchedulingMode(&eventBased); got != "event-based" {
		t.Errorf("SchedulingMode(%q) = %s, want event-based", eventBased, got)
	}

	// The Gate 6 (d) fixture: ingest the synthetic spec's
	// /synthetic/cache-mode operation and confirm the raw value is stored
	// verbatim — never rejected, never coerced to a known value.
	spec := readFixture(t, "../../../test/drift/gate6_synthetic_spec.json")
	routes, err := ParseSpec(spec, farFuturePin)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range routes {
		if r.OperationID != "GetSyntheticCacheMode" {
			continue
		}
		found = true
		if r.CacheMode == nil || *r.CacheMode != "quantum-entangled" {
			t.Errorf("expected cache_mode to be stored verbatim as 'quantum-entangled', got %v", r.CacheMode)
		}
		if SchedulingMode(r.CacheMode) != "ttl-based" {
			t.Errorf("an unrecognised cache_mode must still schedule as ttl-based, got %s", SchedulingMode(r.CacheMode))
		}
	}
	if !found {
		t.Fatal("expected GetSyntheticCacheMode in the Gate 6 fixture")
	}
}

// TestGate6UUIDPathIdentifier — Gate 6 condition (b): a uuid path
// identifier types correctly with zero code changes.
func TestGate6UUIDPathIdentifier(t *testing.T) {
	spec := readFixture(t, "../../../test/drift/gate6_synthetic_spec.json")
	routes, err := ParseSpec(spec, farFuturePin)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.OperationID != "GetSyntheticWidgetsWidgetId" {
			continue
		}
		if got := r.IdentifierTypes["widget_id"]; got != "uuid" {
			t.Fatalf("widget_id identifier type = %q, want uuid", got)
		}
		return
	}
	t.Fatal("expected GetSyntheticWidgetsWidgetId in the Gate 6 fixture")
}

// TestGate6NovelScopeGrammar — Gate 6 condition (c): a scope string
// matching neither live grammar is captured verbatim, unrejected.
func TestGate6NovelScopeGrammar(t *testing.T) {
	spec := readFixture(t, "../../../test/drift/gate6_synthetic_spec.json")
	routes, err := ParseSpec(spec, farFuturePin)
	if err != nil {
		t.Fatal(err)
	}
	const novelScope = "esi::synthetic~widget/read@v3"
	for _, r := range routes {
		if r.OperationID != "GetSyntheticScopeGrammar" {
			continue
		}
		if len(r.Scopes) != 1 || r.Scopes[0] != novelScope {
			t.Fatalf("scopes = %v, want [%s] verbatim", r.Scopes, novelScope)
		}
		return
	}
	t.Fatal("expected GetSyntheticScopeGrammar in the Gate 6 fixture")
}

// TestGate6PostPinRouteIsBlocked — Gate 6 condition (a) at the parse
// level: a route dated 30 days past the app pin is blocked_by_pin.
func TestGate6PostPinRouteIsBlocked(t *testing.T) {
	spec := readFixture(t, "../../../test/drift/gate6_synthetic_spec.json")
	pin, err := ParseDate(DefaultCompatibilityPin) // 2026-08-04
	if err != nil {
		t.Fatal(err)
	}
	routes, err := ParseSpec(spec, pin)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.OperationID != "GetSyntheticFutureRoute" {
			continue
		}
		if !r.BlockedByPin {
			t.Fatal("expected GetSyntheticFutureRoute (x-compatibility-date 2026-09-03) to be blocked by pin 2026-08-04")
		}
		return
	}
	t.Fatal("expected GetSyntheticFutureRoute in the Gate 6 fixture")
}
