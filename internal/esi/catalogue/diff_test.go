package catalogue_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := catalogue.ParseDate(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

func route(t *testing.T, opID, compat string) gen.AppEsiRoute {
	t.Helper()
	return gen.AppEsiRoute{
		OperationID:       opID,
		Method:            "GET",
		UpstreamPath:      "/" + opID + "/",
		CompatibilityDate: pgtype.Date{Time: date(t, compat), Valid: true},
	}
}

func opIDs(cs []catalogue.RouteChange) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.OperationID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDiffRoutesBothDirections is the unit-level half of
// TestPinAdvanceRecordsComputedDiff: the diff must report newly BLOCKED
// routes as well as newly unblocked ones. A one-directional diff would
// render a pin rollback — a legitimate administrator action — as "nothing
// changes", which is the most dangerous possible answer.
func TestDiffRoutesBothDirections(t *testing.T) {
	// blocked_by_pin is `compatibility_date > pin`, so with the pin at the
	// 4th: before is visible, on is visible, after is blocked.
	routes := []gen.AppEsiRoute{
		route(t, "before", "2026-08-01"),
		route(t, "on_old", "2026-08-04"),
		route(t, "between", "2026-08-07"),
		route(t, "on_new", "2026-08-11"),
		route(t, "after", "2026-08-20"),
	}

	t.Run("forward advance unblocks", func(t *testing.T) {
		d := catalogue.DiffRoutes(routes, date(t, "2026-08-04"), date(t, "2026-08-11"))
		if want := []string{"between", "on_new"}; !equal(opIDs(d.NewlyUnblocked), want) {
			t.Errorf("newly_unblocked = %v, want %v", opIDs(d.NewlyUnblocked), want)
		}
		if len(d.NewlyBlocked) != 0 {
			t.Errorf("newly_blocked = %v, want none", opIDs(d.NewlyBlocked))
		}
		if d.Unchanged != 3 {
			t.Errorf("unchanged = %d, want 3", d.Unchanged)
		}
		if d.OldPin != "2026-08-04" || d.NewPin != "2026-08-11" {
			t.Errorf("pins = %s -> %s", d.OldPin, d.NewPin)
		}
	})

	t.Run("rollback blocks", func(t *testing.T) {
		d := catalogue.DiffRoutes(routes, date(t, "2026-08-11"), date(t, "2026-08-04"))
		if want := []string{"between", "on_new"}; !equal(opIDs(d.NewlyBlocked), want) {
			t.Errorf("newly_blocked = %v, want %v — a rollback re-blocks routes and the diff must say so",
				opIDs(d.NewlyBlocked), want)
		}
		if len(d.NewlyUnblocked) != 0 {
			t.Errorf("newly_unblocked = %v, want none", opIDs(d.NewlyUnblocked))
		}
	})

	t.Run("a quiet week changes nothing, and says so", func(t *testing.T) {
		// No route's compatibility date falls in (04, 06]. This is a
		// legitimate, informative answer — not an empty state.
		d := catalogue.DiffRoutes(routes, date(t, "2026-08-04"), date(t, "2026-08-06"))
		if !d.Empty() {
			t.Fatalf("expected an empty diff, got %+v", d)
		}
		if d.Unchanged != len(routes) {
			t.Errorf("unchanged = %d, want %d — the count is what lets a client say "+
				"'0 of %d routes change' instead of rendering a blank panel",
				d.Unchanged, len(routes), len(routes))
		}
		// Both directions must serialise as [], never null: a client cannot
		// distinguish "no routes changed" from "the payload failed to load"
		// if an empty diff arrives as null (the same empty-vs-unavailable
		// distinction SRS §6 draws for collections).
		encoded, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		for _, key := range []string{"newly_blocked", "newly_unblocked"} {
			if _, ok := decoded[key].([]any); !ok {
				t.Errorf("%s = %#v, want [] — an empty diff must not serialise as null", key, decoded[key])
			}
		}
	})

	t.Run("no movement at all", func(t *testing.T) {
		d := catalogue.DiffRoutes(routes, date(t, "2026-08-04"), date(t, "2026-08-04"))
		if !d.Empty() || d.Unchanged != len(routes) {
			t.Errorf("pinning to the same date changed something: %+v", d)
		}
	})

	t.Run("ordered by the date that moved them", func(t *testing.T) {
		d := catalogue.DiffRoutes([]gen.AppEsiRoute{
			route(t, "z_early", "2026-08-05"),
			route(t, "a_late", "2026-08-09"),
			route(t, "a_early", "2026-08-05"),
		}, date(t, "2026-08-04"), date(t, "2026-08-11"))
		if want := []string{"a_early", "z_early", "a_late"}; !equal(opIDs(d.NewlyUnblocked), want) {
			t.Errorf("order = %v, want %v (date, then path)", opIDs(d.NewlyUnblocked), want)
		}
	})

	t.Run("a route with no compatibility date is counted, never guessed at", func(t *testing.T) {
		d := catalogue.DiffRoutes([]gen.AppEsiRoute{
			{OperationID: "no_date", Method: "GET", UpstreamPath: "/x/"},
		}, date(t, "2026-08-04"), date(t, "2026-08-11"))
		if !d.Empty() || d.Unchanged != 1 {
			t.Errorf("expected the undated route to count as unchanged, got %+v", d)
		}
	})

	t.Run("an empty catalogue diffs to an empty diff", func(t *testing.T) {
		d := catalogue.DiffRoutes(nil, date(t, "2026-08-04"), date(t, "2026-08-11"))
		if !d.Empty() || d.Unchanged != 0 {
			t.Errorf("got %+v", d)
		}
	})
}
