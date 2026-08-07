package catalogue

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSpecFetchedAtDMaxNotAppPin — the spec request carries D_max; asserts
// it is NOT the pin (Phase 2 exit criterion; the single most
// misimplementable requirement per 01_ARCHITECTURE.md §5.1). The app pin
// used here (2020-06-15) is deliberately far from D_max (2026-09-01) so
// the two could never be confused by coincidence.
func TestSpecFetchedAtDMaxNotAppPin(t *testing.T) {
	const appPin = "2020-06-15" // never sent anywhere in this test — proves the point by absence
	const dMax = "2026-09-01"

	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case CompatibilityDatesPath:
			if h := r.Header.Get("X-Compatibility-Date"); h != "" {
				t.Errorf("the compatibility-dates discovery call must carry no X-Compatibility-Date header, got %q", h)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"compatibility_dates":["2020-01-01","` + dMax + `","2025-04-01"]}`))
		case OpenAPIPath:
			gotHeader = r.Header.Get("X-Compatibility-Date")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","info":{"title":"t","version":"1"},"paths":{"/x":{"get":{"operationId":"op","x-compatibility-date":"2020-01-01","responses":{}}}}}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer server.Close()

	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC) // after dMax's day, so it isn't clamped away
	gotDMax, specBytes, stale, source, err := fetchSpec(t.Context(), server.Client(), server.URL, now)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Error("a successful live fetch must not be marked stale")
	}
	if source != "live" {
		t.Errorf("source = %s, want live", source)
	}
	if len(specBytes) == 0 {
		t.Error("expected non-empty spec bytes")
	}
	if FormatDate(gotDMax) != dMax {
		t.Errorf("resolved D_max = %s, want %s", FormatDate(gotDMax), dMax)
	}
	if gotHeader != dMax {
		t.Errorf("openapi.json request carried X-Compatibility-Date: %q, want D_max %q", gotHeader, dMax)
	}
	if gotHeader == appPin {
		t.Fatalf("the openapi.json fetch must never use the app pin (%s) — pinning discovery blinds the catalogue permanently", appPin)
	}
}

// TestOfflineBootUsesEmbeddedSnapshot — network failure -> snapshot
// loaded, stale_snapshot set (Phase 2 exit criterion; 01_ARCHITECTURE.md
// §5.1 "Offline boot").
func TestOfflineBootUsesEmbeddedSnapshot(t *testing.T) {
	// A server that closes the connection immediately simulates "ESI is
	// unreachable" without depending on real network conditions.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("test server does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()

	_, meta, err := LoadEmbeddedSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	wantDMax, err := meta.DMaxDate()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	dMax, specBytes, stale, source, err := fetchSpec(t.Context(), server.Client(), server.URL, now)
	if err != nil {
		t.Fatalf("offline boot must fall back to the embedded snapshot, not fail outright: %v", err)
	}
	if !stale {
		t.Error("expected stale_snapshot = true when falling back to the embedded snapshot")
	}
	if source != "embedded-snapshot" {
		t.Errorf("source = %s, want embedded-snapshot", source)
	}
	if len(specBytes) == 0 {
		t.Error("expected non-empty spec bytes from the embedded snapshot")
	}
	if !dMax.Equal(wantDMax) {
		t.Errorf("D_max = %v, want the embedded snapshot's recorded D_max %v", dMax, wantDMax)
	}
}
