// Command gate4-traceability emits Gate 4's evidence artefacts.
//
// ── WHY THIS IS A PROGRAM AND NOT A DOCUMENT ─────────────────────────────
// 04_RELEASE_GATES.md §4.2 requires
// docs/gate-evidence/<version>/gate4/traceability.csv, "one row per
// capability", and §4.3 requires all 58 rows at status = verified and every
// one of the 106 measured ESI routes mapped or recorded as deliberately
// unmapped with a reason.
//
// A hand-written traceability matrix is a document that agrees with itself:
// the author reads the spec, writes the rows, and the rows match the spec
// because that is where they came from. Nothing in that loop can discover
// that a capability's route has no dispatch entry, or that its table has no
// writer — which is exactly what defects B42, B30 and B47 turned out to be.
//
// So every column that could be WRONG is derived from something executable:
//
//	capability rows        docs/00_SRS_v3.1.md Appendix A (parsed; Gate 4.8
//	                       band arithmetic checked while parsing)
//	measured counts        docs/BASELINE.md's Summary table (parsed)
//	route → subscription   internal/sync/worker.SubscribableRoutes()
//	route → why not        internal/sync/worker.DeliberatelyUnmapped()
//	legacy controllers     internal/api/v2shim.Classification()
//	STATUS                 computed from the four above — never asserted
//
// The one declared input is which routes deliver which capability, in
// capability_routes.go. That is specification content (SRS §11), not
// evidence, and it is kept honest by a totality check: every route the sync
// engine can reach, and every route recorded as a not-built capability, must
// be claimed by at least one capability or the program fails.
//
// ── UNREACHABLE ROWS ARE THE POINT ───────────────────────────────────────
// Rows WILL come out at status = unreachable. Per §0.4 that is a gate
// failure correctly recorded, not something to be marked verified with a
// note, and this program exits non-zero when it happens so a CI run cannot
// treat the artefact's existence as the artefact's success.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hangar-project/hangar/internal/api/v2shim"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/worker"
)

func main() {
	repoRoot := flag.String("root", ".", "repository root")
	version := flag.String("version", "", "release version; the <version> in docs/gate-evidence/<version>")
	flag.Parse()

	if *version == "" {
		fmt.Fprintln(os.Stderr, "gate4-traceability: -version is required (docs/gate-evidence/<version>/gate4)")
		os.Exit(2)
	}

	if err := run(*repoRoot, *version); err != nil {
		fmt.Fprintf(os.Stderr, "gate4-traceability: %v\n", err)
		os.Exit(1)
	}
}

func run(repoRoot, version string) error {
	capabilities, err := ParseCapabilities(filepath.Join(repoRoot, "docs", "00_SRS_v3.1.md"))
	if err != nil {
		return err
	}
	baseline, err := ParseBaseline(filepath.Join(repoRoot, "docs", "BASELINE.md"))
	if err != nil {
		return err
	}

	subscribable := worker.SubscribableRoutes()
	unmapped := worker.DeliberatelyUnmapped()

	if err := checkRouteMapTotality(subscribable, unmapped); err != nil {
		return err
	}

	outDir := filepath.Join(repoRoot, "docs", "gate-evidence", version, "gate4")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", outDir, err)
	}

	rows, counts := buildCapabilityRows(capabilities, subscribable, unmapped)
	if err := writeCapabilityCSV(filepath.Join(outDir, "traceability.csv"), rows); err != nil {
		return err
	}
	routeRows := buildRouteRows(subscribable, unmapped)
	if err := writeRouteCSV(filepath.Join(outDir, "esi-route-coverage.csv"), routeRows); err != nil {
		return err
	}
	if err := writeSummary(filepath.Join(outDir, "SUMMARY.md"), version, capabilities, baseline, counts, routeRows); err != nil {
		return err
	}

	fmt.Printf("gate4: %d capability rows (%d verified, %d unreachable, %d deferred)\n",
		len(rows), counts[StatusVerified], counts[StatusUnreachable], counts[StatusDeferred])
	fmt.Printf("gate4: %d catalogued GET routes (%d subscribable, %d deliberately unmapped)\n",
		len(routeRows), len(subscribable), len(unmapped))

	// ── §4.3's pass conditions, evaluated rather than assumed ────────────
	var failures []string
	if len(capabilities) != baseline.Capabilities {
		failures = append(failures, fmt.Sprintf(
			"Gate 4.1: Appendix A enumerates %d capabilities, the gate requires %d",
			len(capabilities), baseline.Capabilities))
	}
	if counts[StatusVerified] != len(rows) {
		failures = append(failures, fmt.Sprintf(
			"Gate 4.1: %d of %d capability rows are not verified (%d unreachable, %d deferred)",
			len(rows)-counts[StatusVerified], len(rows), counts[StatusUnreachable], counts[StatusDeferred]))
	}
	for _, row := range routeRows {
		if row.Status == RouteUnclassified {
			failures = append(failures, "Gate 4.2: route neither mapped nor recorded: "+row.Route)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "\nGATE 4 IS NOT MET. The artefacts above record it, which is the point:")
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "  - "+f)
		}
		return fmt.Errorf("%d gate condition(s) unmet", len(failures))
	}
	return nil
}

// Status is a capability row's verification state.
type Status string

const (
	// StatusVerified — every ESI route this capability needs is one the sync
	// engine can actually reach (it has a dispatch entry, so a subscription
	// for it can exist and a worker will handle it).
	StatusVerified Status = "verified"

	// StatusUnreachable — at least one route this capability needs is
	// recorded as not-built: the schema, the store queries and usually an
	// /api/v1 endpoint exist, and no sync handler writes the table, so the
	// endpoint serves an empty result on every installation. A GATE FAILURE,
	// recorded per §0.4 rather than annotated away.
	StatusUnreachable Status = "unreachable"

	// StatusDeferred — every route is in SRS §12's declared post-v1.0
	// backlog. Gate 4.7 counts declared scope reductions as intentional, but
	// a CAPABILITY in Appendix A resting entirely on deferred routes would
	// still be a contradiction between §12 and Appendix A, so it is reported
	// separately rather than folded into verified.
	StatusDeferred Status = "deferred"
)

// CapabilityRow is one line of traceability.csv, in §4.2's column order.
type CapabilityRow struct {
	ID                int
	Name              string
	UpstreamESIRoutes []string
	LegacyControllers []string
	AlertTypes        []string
	HangarEndpoints   []string
	DeliveringPhase   string
	VerificationTest  string
	Status            Status
	// Note carries WHY a row is not verified. Never used to excuse a status.
	Note string
}

func buildCapabilityRows(capabilities []Capability, subscribable map[string]sync.EntityKind, unmapped map[string]worker.UnmappedReason) ([]CapabilityRow, map[Status]int) {
	counts := map[Status]int{}
	rows := make([]CapabilityRow, 0, len(capabilities))

	for _, capability := range capabilities {
		spec := capabilitySpec(capability.ID)
		row := CapabilityRow{
			ID:                capability.ID,
			Name:              capability.Name,
			UpstreamESIRoutes: spec.Routes,
			LegacyControllers: spec.Controllers,
			AlertTypes:        spec.AlertTypes,
			HangarEndpoints:   spec.Endpoints,
			DeliveringPhase:   spec.Phase,
			VerificationTest:  spec.Test,
		}

		var blocked, deferred []string
		for _, route := range spec.Routes {
			if _, ok := subscribable[route]; ok {
				continue
			}
			switch unmapped[route] {
			case worker.ReasonNotBuilt:
				blocked = append(blocked, route)
			case worker.ReasonPostV1:
				deferred = append(deferred, route)
			}
		}

		switch {
		case len(blocked) > 0:
			row.Status = StatusUnreachable
			row.Note = "no sync handler writes: " + strings.Join(blocked, " ")
		case len(deferred) > 0 && len(deferred) == len(spec.Routes):
			row.Status = StatusDeferred
			row.Note = "every route is SRS §12 post-v1.0: " + strings.Join(deferred, " ")
		default:
			row.Status = StatusVerified
		}
		counts[row.Status]++
		rows = append(rows, row)
	}
	return rows, counts
}

func writeCapabilityCSV(path string, rows []CapabilityRow) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	out := csv.NewWriter(file)
	defer out.Flush()

	// §4.2's column list, verbatim, plus `note` — which carries the reason a
	// row is not verified. §0.4 requires the failure recorded, and a status
	// with no reason is not a record.
	if err := out.Write([]string{
		"capability_id", "capability_name", "upstream_esi_routes", "legacy_controllers",
		"alert_types", "hangar_endpoints", "delivering_phase", "verification_test", "status", "note",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := out.Write([]string{
			strconv.Itoa(row.ID), row.Name,
			strings.Join(row.UpstreamESIRoutes, " "),
			strings.Join(row.LegacyControllers, " "),
			strings.Join(row.AlertTypes, " "),
			strings.Join(row.HangarEndpoints, " "),
			row.DeliveringPhase, row.VerificationTest, string(row.Status), row.Note,
		}); err != nil {
			return err
		}
	}
	return out.Error()
}

// RouteStatus is Gate 4.2's per-route verdict.
type RouteStatus string

const (
	RouteMapped       RouteStatus = "mapped"
	RouteUnmapped     RouteStatus = "deliberately-unmapped"
	RouteUnclassified RouteStatus = "UNCLASSIFIED"
)

// RouteRow is one line of esi-route-coverage.csv — Gate 4.2's countable
// form: every catalogued GET route, whether a subscription can exist for it,
// and if not, the recorded reason.
type RouteRow struct {
	Route      string
	Status     RouteStatus
	EntityKind string
	Reason     string
}

func buildRouteRows(subscribable map[string]sync.EntityKind, unmapped map[string]worker.UnmappedReason) []RouteRow {
	rows := make([]RouteRow, 0, len(subscribable)+len(unmapped))
	for route, kind := range subscribable {
		rows = append(rows, RouteRow{Route: route, Status: RouteMapped, EntityKind: string(kind)})
	}
	for route, reason := range unmapped {
		rows = append(rows, RouteRow{Route: route, Status: RouteUnmapped, Reason: string(reason)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Route < rows[j].Route })
	return rows
}

func writeRouteCSV(path string, rows []RouteRow) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	out := csv.NewWriter(file)
	defer out.Flush()
	if err := out.Write([]string{"upstream_path", "status", "entity_kind", "unmapped_reason"}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := out.Write([]string{row.Route, string(row.Status), row.EntityKind, row.Reason}); err != nil {
			return err
		}
	}
	return out.Error()
}

func writeSummary(path, version string, capabilities []Capability, baseline Baseline, counts map[Status]int, routeRows []RouteRow) error {
	byReason := map[string]int{}
	mapped := 0
	for _, row := range routeRows {
		if row.Status == RouteMapped {
			mapped++
			continue
		}
		byReason[row.Reason]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Gate 4 evidence — %s\n\n", version)
	b.WriteString("Generated by `go run ./tools/gate4-traceability`. Do not edit by hand: every\n")
	b.WriteString("status below is computed from the dispatch tables, the route classification and\n")
	b.WriteString("the parsed specification, so editing this file changes the record without\n")
	b.WriteString("changing the fact.\n\n")

	fmt.Fprintf(&b, "## 4.1 — capability rows\n\n")
	fmt.Fprintf(&b, "| Status | Rows |\n| :-- | --: |\n")
	fmt.Fprintf(&b, "| verified | %d |\n| unreachable | %d |\n| deferred | %d |\n| **total** | **%d** |\n\n",
		counts[StatusVerified], counts[StatusUnreachable], counts[StatusDeferred], len(capabilities))
	fmt.Fprintf(&b, "Gate 4.1 requires %d rows, all verified. %s\n\n",
		baseline.Capabilities, passFail(counts[StatusVerified] == len(capabilities) && len(capabilities) == baseline.Capabilities))

	fmt.Fprintf(&b, "## 4.2 — ESI route coverage\n\n")
	fmt.Fprintf(&b, "| Disposition | Routes |\n| :-- | --: |\n")
	fmt.Fprintf(&b, "| mapped to a live subscription | %d |\n", mapped)
	reasons := make([]string, 0, len(byReason))
	for reason := range byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(&b, "| deliberately unmapped — %s | %d |\n", reason, byReason[reason])
	}
	fmt.Fprintf(&b, "| **total catalogued GET routes** | **%d** |\n\n", len(routeRows))

	b.WriteString("Gate 4.2 requires every measured route mapped or recorded with a reason. Every\n")
	b.WriteString("route above carries one, so the partition is total — but `not-built` is a\n")
	b.WriteString("RECORDED FAILURE and not an exemption: those routes back an Appendix A\n")
	b.WriteString("capability whose table, store queries and API endpoint exist while no sync\n")
	b.WriteString("handler writes it, so the endpoint serves an empty result forever.\n\n")

	fmt.Fprintf(&b, "## Measured baseline (docs/BASELINE.md)\n\n")
	fmt.Fprintf(&b, "| Dimension | Measured |\n| :-- | --: |\n")
	fmt.Fprintf(&b, "| distinct ESI routes | %d |\n", baseline.ESIRoutes)
	fmt.Fprintf(&b, "| UI controllers | %d |\n", baseline.UIControllers)
	fmt.Fprintf(&b, "| alert types | %d |\n", baseline.AlertTypes)
	fmt.Fprintf(&b, "| UI locales | %d |\n", baseline.Locales)
	fmt.Fprintf(&b, "| /api/v2 controllers | %d |\n\n", baseline.V2Controllers)

	fmt.Fprintf(&b, "## Gate 7 — /api/v2 shim coverage\n\n")
	shim := v2shim.ByStatus()
	fmt.Fprintf(&b, "| Status | Routes |\n| :-- | --: |\n")
	total := 0
	for _, status := range []v2shim.RouteStatus{
		v2shim.StatusServed, v2shim.StatusPending, v2shim.StatusUnshimmable, v2shim.StatusBreaking,
	} {
		fmt.Fprintf(&b, "| %s | %d |\n", status, len(shim[status]))
		total += len(shim[status])
	}
	fmt.Fprintf(&b, "| **total** | **%d** |\n", total)

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func passFail(ok bool) string {
	if ok {
		return "**MET.**"
	}
	return "**NOT MET** — recorded, not excused."
}
