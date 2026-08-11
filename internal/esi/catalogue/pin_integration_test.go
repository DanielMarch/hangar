//go:build integration

package catalogue_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// seedRoutes writes a small, hand-built catalogue whose compatibility
// dates straddle the pin in both directions. Built by hand rather than by
// ingesting the embedded snapshot so the expected diff is exact and does
// not move when CCP publishes a new spec.
func seedRoutes(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	for _, r := range []struct{ opID, compat string }{
		{"get_old_a", "2026-08-01"},
		{"get_old_b", "2026-08-04"},
		{"get_new_a", "2026-08-07"},
		{"get_new_b", "2026-08-11"},
		{"get_far_future", "2026-12-01"},
	} {
		d, err := catalogue.ParseDate(r.compat)
		require.NoError(t, err)
		_, err = s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
			OperationID:       r.opID,
			Method:            "GET",
			UpstreamPath:      "/" + r.opID + "/",
			CompatibilityDate: pgtype.Date{Time: d, Valid: true},
			BlockedByPin:      d.After(mustDate(t, catalogue.DefaultCompatibilityPin)),
			SpecFragment:      json.RawMessage(`{}`),
			IdentifierTypes:   json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := catalogue.ParseDate(s)
	require.NoError(t, err)
	return d
}

// now returns a fixed "now" late enough that every date used in these
// tests is in the past for the rollover-today fallback, so the tests
// exercise the RECORDED D_max bound rather than accidentally passing
// because of the fallback.
func fixedNow() time.Time {
	return time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
}

// TestPinPreviewIsNonMutating is Phase 18's exit criterion for the preview
// half of SRS defect B13: calling preview leaves the stored pin and
// app.esi_pin_history untouched. This is the whole reason preview is a
// separate endpoint rather than a flag on the advance — Principle 12
// requires the administrator to see the diff BEFORE the pin moves.
func TestPinPreviewIsNonMutating(t *testing.T) {
	ctx := context.Background()
	s := newMigratedStore(t)
	seedRoutes(t, s)
	require.NoError(t, catalogue.SetDMax(ctx, s, mustDate(t, "2026-08-11")))

	pinBefore, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	historyBefore, err := s.ListEsiPinHistory(ctx, 100)
	require.NoError(t, err)

	preview, err := catalogue.PreviewPin(ctx, s, mustDate(t, "2026-08-11"), fixedNow())
	require.NoError(t, err)

	// The preview is real: it reports the actual diff, not a placeholder.
	require.Equal(t, "2026-08-04", preview.CurrentPin)
	require.Equal(t, "2026-08-11", preview.CandidatePin)
	require.True(t, preview.WithinBounds)
	require.Len(t, preview.Diff.NewlyUnblocked, 2, "get_new_a and get_new_b become callable")
	require.Empty(t, preview.Diff.NewlyBlocked)

	// And it changed nothing.
	pinAfter, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, pinBefore, pinAfter, "preview moved the stored pin")

	historyAfter, err := s.ListEsiPinHistory(ctx, 100)
	require.NoError(t, err)
	require.Len(t, historyAfter, len(historyBefore), "preview wrote to app.esi_pin_history")

	// Previewing repeatedly is still non-mutating — a client polling the
	// preview as the operator types a date must not accumulate history.
	for i := 0; i < 3; i++ {
		_, err := catalogue.PreviewPin(ctx, s, mustDate(t, "2026-12-01"), fixedNow())
		require.NoError(t, err)
	}
	historyAfterMany, err := s.ListEsiPinHistory(ctx, 100)
	require.NoError(t, err)
	require.Len(t, historyAfterMany, len(historyBefore))
	pinStill, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, pinBefore, pinStill)
}

// TestPinPreviewReportsOutOfRangeWithoutRefusing — an out-of-range
// candidate is previewed, not refused: the caller gets the real diff plus
// the actual ceiling, which is strictly more useful than an error. The
// refusal that matters is the advance's.
func TestPinPreviewReportsOutOfRangeWithoutRefusing(t *testing.T) {
	ctx := context.Background()
	s := newMigratedStore(t)
	seedRoutes(t, s)
	require.NoError(t, catalogue.SetDMax(ctx, s, mustDate(t, "2026-08-11")))

	preview, err := catalogue.PreviewPin(ctx, s, mustDate(t, "2026-12-01"), fixedNow())
	require.NoError(t, err)
	require.False(t, preview.WithinBounds)
	require.Equal(t, "2026-08-11", preview.DMax)
	require.Equal(t, "recorded", preview.DMaxSource)
	require.Len(t, preview.Diff.NewlyUnblocked, 3, "the diff is still computed and real")
}

// TestPinAdvanceRecordsComputedDiff is Phase 18's exit criterion for the
// other half of B13: the persisted route_diff is the real diff, never
// `{}`, and newly blocked and newly unblocked routes both appear.
//
// Every diff recorded before this phase was empty — advancePinHandler
// passed nil and AdvancePin substituted `{}`.
func TestPinAdvanceRecordsComputedDiff(t *testing.T) {
	ctx := context.Background()
	s := newMigratedStore(t)
	seedRoutes(t, s)
	require.NoError(t, catalogue.SetDMax(ctx, s, mustDate(t, "2026-12-01")))

	// Forward: two routes become callable.
	rec, diff, err := catalogue.AdvancePin(ctx, s, mustDate(t, "2026-08-11"), "test", fixedNow())
	require.NoError(t, err)
	require.NotEqual(t, json.RawMessage(`{}`), rec.RouteDiff, "the recorded diff is still the empty placeholder")

	var stored catalogue.RouteDiff
	require.NoError(t, json.Unmarshal(rec.RouteDiff, &stored))
	require.Equal(t, diff, stored, "the returned diff and the persisted one must be the same document")
	require.Equal(t, "2026-08-04", stored.OldPin)
	require.Equal(t, "2026-08-11", stored.NewPin)
	require.ElementsMatch(t,
		[]string{"get_new_a", "get_new_b"},
		[]string{stored.NewlyUnblocked[0].OperationID, stored.NewlyUnblocked[1].OperationID})
	require.Empty(t, stored.NewlyBlocked)
	// The rendered change carries enough to display without a second lookup.
	require.Equal(t, "GET", stored.NewlyUnblocked[0].Method)
	require.Equal(t, "/get_new_a/", stored.NewlyUnblocked[0].UpstreamPath)
	require.Equal(t, "2026-08-07", stored.NewlyUnblocked[0].CompatibilityDate)

	// Backward: the SAME two routes are re-blocked. This is the direction
	// the roadmap calls out and the one a one-way diff would report as
	// "nothing changed".
	rec2, _, err := catalogue.AdvancePin(ctx, s, mustDate(t, "2026-08-04"), "test", fixedNow())
	require.NoError(t, err)
	var rolled catalogue.RouteDiff
	require.NoError(t, json.Unmarshal(rec2.RouteDiff, &rolled))
	require.Empty(t, rolled.NewlyUnblocked)
	require.ElementsMatch(t,
		[]string{"get_new_a", "get_new_b"},
		[]string{rolled.NewlyBlocked[0].OperationID, rolled.NewlyBlocked[1].OperationID})

	// A no-op week records an empty-but-well-formed diff, not `{}`.
	rec3, _, err := catalogue.AdvancePin(ctx, s, mustDate(t, "2026-08-05"), "test", fixedNow())
	require.NoError(t, err)
	var quiet catalogue.RouteDiff
	require.NoError(t, json.Unmarshal(rec3.RouteDiff, &quiet))
	require.True(t, quiet.Empty())
	require.Equal(t, 5, quiet.Unchanged, "the count is what makes 'nothing changed' legible")
	require.NotNil(t, quiet.NewlyBlocked)
	require.NotNil(t, quiet.NewlyUnblocked)
}

// TestPinAdvanceRefusesDateNewerThanDMax is the SERVER half of the exit
// criterion. The client half alone was never the criterion: a UI-only
// bound check is bypassed by any direct API call.
func TestPinAdvanceRefusesDateNewerThanDMax(t *testing.T) {
	ctx := context.Background()
	s := newMigratedStore(t)
	seedRoutes(t, s)
	require.NoError(t, catalogue.SetDMax(ctx, s, mustDate(t, "2026-08-11")))

	pinBefore, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)

	_, _, err = catalogue.AdvancePin(ctx, s, mustDate(t, "2026-12-01"), "test", fixedNow())
	require.Error(t, err)
	var oor *catalogue.OutOfRangeError
	require.ErrorAs(t, err, &oor, "the refusal must be typed so the API can answer 422, not 500")
	require.Equal(t, mustDate(t, "2026-08-11"), oor.DMax)
	require.Equal(t, "recorded", oor.DMaxSource)

	// Refused means refused: neither the pin nor the history moved.
	pinAfter, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, pinBefore, pinAfter)
	history, err := s.ListEsiPinHistory(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, history, "a refused advance must not be recorded as one")

	// Exactly D_max is allowed — the bound is inclusive.
	_, _, err = catalogue.AdvancePin(ctx, s, mustDate(t, "2026-08-11"), "test", fixedNow())
	require.NoError(t, err)

	// Rolling BACK is always allowed; there is deliberately no lower bound.
	_, _, err = catalogue.AdvancePin(ctx, s, mustDate(t, "2026-08-01"), "test", fixedNow())
	require.NoError(t, err)
}

// TestPinBoundFallsBackToRolloverTodayWithoutAnIngest — with no recorded
// D_max the bound is ESI's own current compatibility date. Looser than a
// real D_max, but still sound (D_max <= today always) and still a real
// server-side refusal, which is what the endpoint had none of before.
func TestPinBoundFallsBackToRolloverTodayWithoutAnIngest(t *testing.T) {
	ctx := context.Background()
	s := newMigratedStore(t)
	seedRoutes(t, s)

	now := fixedNow()
	dMax, source, err := catalogue.GetDMax(ctx, s, now)
	require.NoError(t, err)
	require.Equal(t, "rollover-today", source)
	require.Equal(t, catalogue.CurrentDate(now), dMax)

	_, _, err = catalogue.AdvancePin(ctx, s, mustDate(t, "2099-01-01"), "test", now)
	var oor *catalogue.OutOfRangeError
	require.ErrorAs(t, err, &oor)
	require.Equal(t, "rollover-today", oor.DMaxSource)
}

// TestRecordedDMaxIsClampedToToday — a D_max recorded by an earlier ingest
// can never license pinning past ESI's current compatibility date, since
// ESI rejects a future date outright (01_ARCHITECTURE.md §5.1).
func TestRecordedDMaxIsClampedToToday(t *testing.T) {
	ctx := context.Background()
	s := newMigratedStore(t)

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) // past the 11:00 UTC rollover
	require.NoError(t, catalogue.SetDMax(ctx, s, mustDate(t, "2027-06-01")))

	dMax, source, err := catalogue.GetDMax(ctx, s, now)
	require.NoError(t, err)
	require.Equal(t, "recorded", source)
	require.Equal(t, catalogue.CurrentDate(now), dMax, "a stale recorded D_max is clamped to today")
}
