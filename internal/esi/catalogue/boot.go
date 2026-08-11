package catalogue

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// BootResult summarises one catalogue boot/ingest pass — the payload the
// admin Route Catalogue view (Phase 18) surfaces.
type BootResult struct {
	DMax          time.Time
	Pin           time.Time
	StaleSnapshot bool   // true when the embedded snapshot was used instead of a live fetch
	Source        string // "live" | "embedded-snapshot"
	// DMaxRecorded reports whether DMax made it into app.setting for
	// AdvancePin's server-side bound check ([v3.1 — B13]). False means the
	// bound falls back to the rollover date until the next successful
	// ingest — sound, just looser.
	DMaxRecorded bool
	SyncResult
}

// Boot runs the full Phase 2 boot sequence (01_ARCHITECTURE.md §5.1):
//  1. GET /meta/compatibility-dates -> D_max (no X-Compatibility-Date header)
//  2. GET /meta/openapi.json with X-Compatibility-Date: D_max — NEVER the app pin
//  3. ingest every operation into app.esi_route
//  4. mark routes newer than the app pin blocked_by_pin
//
// If steps 1 or 2 fail for any reason (network down, ESI unreachable, a
// non-200 response), Boot falls back to the embedded snapshot and marks
// the result StaleSnapshot — a stale snapshot must never look like a live
// ingest (01_ARCHITECTURE.md §5.1's "Offline boot"). The pin is read (and
// seeded on first boot) but is NEVER advanced here — advancing it is
// exclusively AdvancePin's job, called only from an explicit administrator
// action in a later phase.
func Boot(ctx context.Context, client *http.Client, store Store, now time.Time) (BootResult, error) {
	pin, err := GetPin(ctx, store)
	if err != nil {
		return BootResult{}, err
	}

	dMax, specBytes, stale, source, err := fetchSpec(ctx, client, EsiBaseURL, now)
	if err != nil {
		return BootResult{}, fmt.Errorf("catalogue: boot: neither a live fetch nor the embedded snapshot succeeded: %w", err)
	}

	routes, err := ParseSpec(specBytes, pin)
	if err != nil {
		return BootResult{}, fmt.Errorf("catalogue: boot: %w", err)
	}

	syncResult, err := Sync(ctx, store, routes)
	if err != nil {
		return BootResult{}, fmt.Errorf("catalogue: boot: %w", err)
	}

	// [v3.1 — B13] Persist the D_max this ingest observed. Boot has always
	// computed it and returned it in BootResult, but nothing stored it, so
	// AdvancePin had no ceiling to validate a candidate pin against. This
	// is the only writer of the setting; GetDMax is the only reader, and it
	// falls back to the rollover date when no ingest has run yet.
	//
	// A failure here does NOT fail the boot: the catalogue is ingested and
	// usable, and GetDMax's fallback is sound without this row. Losing the
	// tighter bound is worth less than losing the ingest.
	dMaxRecorded := true
	if err := SetDMax(ctx, store, dMax); err != nil {
		dMaxRecorded = false
	}

	return BootResult{
		DMaxRecorded:  dMaxRecorded,
		DMax:          dMax,
		Pin:           pin,
		StaleSnapshot: stale,
		Source:        source,
		SyncResult:    syncResult,
	}, nil
}

// fetchSpec performs steps 1-2 of the boot sequence against baseURL,
// falling back to the embedded snapshot on any failure. Split out of Boot
// so ingest-only tests (which never need a live network call) can call
// ParseSpec/Sync directly without going through HTTP at all, and so
// TestSpecFetchedAtDMaxNotAppPin / TestOfflineBootUsesEmbeddedSnapshot can
// point baseURL at an httptest server instead of the real ESI host.
func fetchSpec(ctx context.Context, client *http.Client, baseURL string, now time.Time) (dMax time.Time, specBytes []byte, stale bool, source string, err error) {
	dates, dateErr := fetchCompatibilityDates(ctx, client, baseURL)
	if dateErr == nil {
		max, maxErr := MaxDate(dates)
		if maxErr == nil {
			clamped := ClampToToday(max, now)
			spec, specErr := fetchOpenAPISpec(ctx, client, baseURL, FormatDate(clamped))
			if specErr == nil {
				return clamped, spec, false, "live", nil
			}
			err = specErr
		} else {
			err = maxErr
		}
	} else {
		err = dateErr
	}

	// Offline boot: fall back to the embedded snapshot rather than fail
	// the whole process. The snapshot must never masquerade as a live
	// ingest, hence `stale = true` unconditionally on this path.
	spec, meta, snapErr := LoadEmbeddedSnapshot()
	if snapErr != nil {
		return time.Time{}, nil, false, "", fmt.Errorf("live fetch failed (%v) and embedded snapshot failed too: %w", err, snapErr)
	}
	metaDMax, dateErr2 := meta.DMaxDate()
	if dateErr2 != nil {
		return time.Time{}, nil, false, "", fmt.Errorf("embedded snapshot metadata: %w", dateErr2)
	}
	return metaDMax, spec, true, "embedded-snapshot", nil
}
