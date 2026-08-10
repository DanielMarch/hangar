package catalogue

import (
	"fmt"
	"sort"
	"strings"
)

// Thresholds returns every CategoryThreshold entry in Catalogue, in
// catalogue order.
func Thresholds() []AlertType {
	var out []AlertType
	for _, t := range Catalogue {
		if t.Category == CategoryThreshold {
			out = append(out, t)
		}
	}
	return out
}

// ThresholdSourceRoutes is the set of upstream_paths the threshold alerts
// depend on — what an operator (or the sync-subscription seeder) must make
// sure is actually being polled for the threshold evaluator to have data.
func ThresholdSourceRoutes() []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range Thresholds() {
		if t.SourceRoute != "" && !seen[t.SourceRoute] {
			seen[t.SourceRoute] = true
			out = append(out, t.SourceRoute)
		}
	}
	sort.Strings(out)
	return out
}

// ValidateThresholds is §4.4's build-time rule, executable:
//
//	"Each threshold alert must declare its source route; a threshold alert
//	 whose source route is not in the sync set is a build-time error."
//
// syncSet is the set of app.esi_route.upstream_path values the sync
// engine can actually poll — internal/sync/worker.SyncSet(), which is the
// union of the three workers' dispatch tables plus their detail fan-out
// paths. Passing it in (rather than importing the worker package here)
// keeps this package free of any dependency on the sync engine, so the
// catalogue stays loadable from anywhere; the test that runs the real
// check supplies the real set.
//
// Three distinct failure modes are reported separately, because they have
// completely different fixes:
//
//   - a threshold alert with no SourceRoute at all — the catalogue entry
//     is incomplete (and the database's own CHECK constraint
//     threshold_declares_source would reject its row anyway);
//   - a non-threshold alert that declares one — a category mistake, caught
//     here rather than being silently ignored by the seed SQL;
//   - a SourceRoute absent from the sync set — the alert can never fire
//     because nothing ever fetches its data. §4.4 calls this a build-time
//     error precisely because it is invisible at runtime: the alert simply
//     never appears, which looks identical to "nothing is wrong".
//
// Every problem found is reported, not just the first — a catalogue edit
// that breaks three thresholds should say so once, not across three runs.
func ValidateThresholds(syncSet map[string]bool) error {
	var problems []string

	for _, t := range Catalogue {
		if t.Category != CategoryThreshold {
			if t.SourceRoute != "" {
				problems = append(problems, fmt.Sprintf(
					"alert type %q is category %q but declares source route %q — only %q alerts may declare one",
					t.Name, t.Category, t.SourceRoute, CategoryThreshold))
			}
			continue
		}
		if t.SourceRoute == "" {
			problems = append(problems, fmt.Sprintf(
				"threshold alert %q declares no source route (§4.4 requires one; app.alert_type's threshold_declares_source CHECK would reject its row)",
				t.Name))
			continue
		}
		if t.SourceMethod == "" {
			problems = append(problems, fmt.Sprintf(
				"threshold alert %q declares source route %q with no HTTP method — app.esi_route's natural key is (method, upstream_path)",
				t.Name, t.SourceRoute))
		}
		if !syncSet[t.SourceRoute] {
			problems = append(problems, fmt.Sprintf(
				"threshold alert %q declares source route %q, which is NOT in the sync set — nothing polls it, so the alert could never fire",
				t.Name, t.SourceRoute))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("catalogue: threshold source-route validation failed:\n  - %s", strings.Join(problems, "\n  - "))
}
