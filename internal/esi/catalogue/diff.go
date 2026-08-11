package catalogue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/hangar-project/hangar/internal/store/gen"
)

// RouteChange identifies one route whose blocked-by-pin state differs
// between two compatibility dates. It carries enough to be rendered
// without a second lookup — an administrator reading a pin diff wants the
// method and path, not a UUID.
type RouteChange struct {
	OperationID       string `json:"operation_id"`
	Method            string `json:"method"`
	UpstreamPath      string `json:"upstream_path"`
	CompatibilityDate string `json:"compatibility_date"`
}

// RouteDiff is the full, BOTH-DIRECTIONS route-set diff between two
// compatibility dates — the payload stored in app.esi_pin_history
// .route_diff and returned by the preview endpoint.
//
// Both directions matter and the roadmap's Phase 18 edge case says so
// explicitly. Moving the pin forward unblocks routes; moving it BACK — a
// rollback after a bad advance, which is a legitimate administrator action
// and the reason AdvancePin has no lower bound — blocks routes that were
// working a moment ago. A one-directional diff would show that rollback as
// "nothing changes", which is the most dangerous possible answer.
//
// NewlyBlocked and NewlyUnblocked are never nil: an empty diff must
// serialise as `[]`, so the client can tell "no routes changed" (a
// legitimate, informative answer for a quiet week) from a payload that
// failed to load. `null` there would collapse those two states the same
// way SRS §6 forbids for collections.
type RouteDiff struct {
	OldPin         string        `json:"old_pin"`
	NewPin         string        `json:"new_pin"`
	NewlyUnblocked []RouteChange `json:"newly_unblocked"`
	NewlyBlocked   []RouteChange `json:"newly_blocked"`
	// Unchanged counts the routes whose blocked state is identical at both
	// dates, so "no routes changed" can be rendered as "0 of 412 routes
	// change" rather than as a bare empty panel.
	Unchanged int `json:"unchanged"`
}

// Empty reports whether the diff moves no route in either direction. This
// is a legitimate answer, not a failure — see RouteDiff's doc comment.
func (d RouteDiff) Empty() bool {
	return len(d.NewlyUnblocked) == 0 && len(d.NewlyBlocked) == 0
}

// blockedAt is the single definition of "is this route gated by the pin at
// date d". It matches ingest.go's own `compatDate.After(appPin)` for
// app.esi_route.blocked_by_pin, verbatim and deliberately: if the two ever
// disagreed, a preview would promise a change the next ingest wouldn't
// make.
func blockedAt(compatibilityDate, pin time.Time) bool {
	return compatibilityDate.After(pin)
}

// DiffRoutes computes the route-set diff between two compatibility dates
// over an already-fetched route set. Pure — no context, no store — so the
// table test can exercise every direction without a database, the same way
// ParseSpec is testable without a network.
//
// Retired routes are the caller's business to exclude (ListEsiRoutes
// already does); a route that no longer exists upstream cannot be
// meaningfully "unblocked" by moving the pin.
func DiffRoutes(routes []gen.AppEsiRoute, oldPin, newPin time.Time) RouteDiff {
	diff := RouteDiff{
		OldPin:         FormatDate(oldPin),
		NewPin:         FormatDate(newPin),
		NewlyUnblocked: []RouteChange{},
		NewlyBlocked:   []RouteChange{},
	}
	for _, r := range routes {
		if !r.CompatibilityDate.Valid {
			// x-compatibility-date is mandatory at ingest (buildRoute errors
			// without one), so a NULL here means a row written by something
			// other than an ingest. Count it as unchanged rather than guess
			// a date and report a change that isn't real.
			diff.Unchanged++
			continue
		}
		compat := r.CompatibilityDate.Time.UTC()
		was := blockedAt(compat, oldPin)
		now := blockedAt(compat, newPin)
		switch {
		case was && !now:
			diff.NewlyUnblocked = append(diff.NewlyUnblocked, routeChange(r, compat))
		case !was && now:
			diff.NewlyBlocked = append(diff.NewlyBlocked, routeChange(r, compat))
		default:
			diff.Unchanged++
		}
	}
	// Stable, human-meaningful ordering: by the date that moved them, then
	// by path. An administrator scanning a 40-route diff reads it as "these
	// went live on the 4th, these on the 11th", not in ingest order.
	sortChanges(diff.NewlyUnblocked)
	sortChanges(diff.NewlyBlocked)
	return diff
}

func routeChange(r gen.AppEsiRoute, compat time.Time) RouteChange {
	return RouteChange{
		OperationID:       r.OperationID,
		Method:            r.Method,
		UpstreamPath:      r.UpstreamPath,
		CompatibilityDate: FormatDate(compat),
	}
}

func sortChanges(cs []RouteChange) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].CompatibilityDate != cs[j].CompatibilityDate {
			return cs[i].CompatibilityDate < cs[j].CompatibilityDate
		}
		if cs[i].UpstreamPath != cs[j].UpstreamPath {
			return cs[i].UpstreamPath < cs[j].UpstreamPath
		}
		return cs[i].Method < cs[j].Method
	})
}

// ComputeRouteDiff reads the live route set and diffs it across the two
// dates. Read-only: it takes no locks, writes nothing, and is the shared
// body of both PreviewPin and AdvancePin so the diff an administrator
// confirmed is computed by the same code that records it.
func ComputeRouteDiff(ctx context.Context, q Store, oldPin, newPin time.Time) (RouteDiff, error) {
	routes, err := q.ListEsiRoutes(ctx)
	if err != nil {
		return RouteDiff{}, fmt.Errorf("catalogue: listing routes for the pin diff: %w", err)
	}
	return DiffRoutes(routes, oldPin, newPin), nil
}

// MarshalRouteDiff encodes a diff for app.esi_pin_history.route_diff.
func MarshalRouteDiff(d RouteDiff) (json.RawMessage, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("catalogue: encoding route diff: %w", err)
	}
	return b, nil
}
