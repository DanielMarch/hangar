// Command gate7-bytediff runs Gate 7 — Third-Party API Migration
// (docs/04_RELEASE_GATES.md §7) — and commits the byte-diff report §8 names
// as its blocking artefact.
//
// ── WHY A WRAPPER RATHER THAN A SECOND COMPARISON ────────────────────────
// Gate 7's comparison is TestShimByteCompatibleForAllNineControllers: it
// requests every one of legacy's 34 read routes through the real handler
// chain and compares response BYTES against the recorded corpus. That test
// is what blocks the release, and it is correct.
//
// What it did not do was leave anything behind. §8 requires "a byte-diff
// report over the corpus", and a passing test is not a report — a reviewer
// cannot read it, and "16 of 34 served" cannot be checked from it.
//
// So this command invokes that test with HANGAR_GATE7_EVIDENCE_DIR set, and
// the test writes one CSV row per route as it verifies it. Reimplementing
// the comparison here would have meant a second copy of the fixtures, the
// credential and the handler chain — free to drift from the one that gates
// the release, and free to report a pass it did not earn.
//
//	go run ./tools/gate7-bytediff -version=v1.0.0-rc1
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/hangar-project/hangar/test/load"
	"github.com/hangar-project/hangar/tools/gaterun"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gate7-bytediff: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		version = flag.String("version", "", "release version the evidence belongs to")
		outDir  = flag.String("out", "", "evidence directory (default docs/gate-evidence/<version>/gate7)")
		timeout = flag.Duration("timeout", 20*time.Minute, "timeout for the underlying go test run")
	)
	flag.Parse()

	if *version == "" {
		return errors.New("-version is required")
	}
	dir, err := gaterun.EvidenceDir(*outDir, *version, "gate7")
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := time.Now()

	// The corpus verification needs a database (the shim reads projections
	// through the real store), so it is the testcontainers-backed
	// integration suite. Its exit code is the gate's own verdict; the CSV
	// is the evidence for it.
	fmt.Println("gate7: running the corpus verification (testcontainers-backed, this takes a few minutes)")
	cmd := exec.CommandContext(ctx, "go", "test", "-tags=integration",
		"-run", "TestShimByteCompatibleForAllNineControllers|TestShimEmitsDeprecationAndSunset|TestShimStripsSyncEnvelope|TestReshapedRoutesReturn410WithMigrationPointer|TestWriteRoutesAreNotShimmed",
		"-count=1", fmt.Sprintf("-timeout=%s", *timeout), "./internal/api/v2shim/...")
	cmd.Env = append(os.Environ(), "HANGAR_GATE7_EVIDENCE_DIR="+absDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	testErr := cmd.Run()

	logPath := filepath.Join(dir, "verification.log")
	if testErr != nil {
		_ = os.WriteFile(logPath, []byte("go test exited non-zero: "+testErr.Error()+"\n"), 0o600)
	}

	rows, err := readReport(filepath.Join(dir, "byte-diff.csv"))
	if err != nil {
		return fmt.Errorf("the verification produced no byte-diff report: %w", err)
	}

	counts := tally(rows)
	conditions := []load.ConditionResult{
		{
			ID:          "7.1",
			Description: "every served route is byte-identical to its recording (field order, whitespace, numeric formatting and null-vs-absent all in scope)",
			Passed:      counts["served"] > 0 && counts["identical"] == counts["served"],
			Measurement: fmt.Sprintf("%d of %d served routes byte-identical", counts["identical"], counts["served"]),
		},
		{
			ID:          "7.7",
			Description: "RoleController and RoleLookupController return a documented 410 with a migration pointer, not a partial shim",
			Passed:      counts["breaking"] > 0 && counts["breaking_410"] == counts["breaking"],
			Measurement: fmt.Sprintf("%d breaking routes, %d answered 410", counts["breaking"], counts["breaking_410"]),
		},
		{
			ID:          "7.8",
			Description: "unshimmable and pending routes answer 501 — a clear \"not shimmed\", never a 404",
			Passed:      counts["unserved_501"] == counts["unshimmable"]+counts["pending"],
			Measurement: fmt.Sprintf("%d unshimmable + %d pending, %d answered 501",
				counts["unshimmable"], counts["pending"], counts["unserved_501"]),
		},
		{
			ID:          "7-suite",
			Description: "the corpus verification suite passed (7.2 Deprecation, 7.3 Sunset, 7.4 envelope stripped, 7.7 410s, 7.8 write routes)",
			Passed:      testErr == nil,
			Measurement: suiteResult(testErr),
		},
	}

	if err := load.WriteSummary(dir, load.Summary{
		Gate: "7", Name: "Third-Party API Migration", Version: *version,
		StartedAt: started, FinishedAt: time.Now(),
		Headline: fmt.Sprintf(
			"%d legacy read routes: %d served and byte-identical, %d pending, %d unshimmable, %d breaking.",
			len(rows), counts["identical"], counts["pending"], counts["unshimmable"], counts["breaking"]),
		Conditions: conditions,
		Environment: map[string]string{
			"Corpus":       "testdata/legacy-api-v2 — responses recorded from a running legacy SeAT instance",
			"Comparison":   "response BYTES through the real handler chain, not parsed objects",
			"Verification": "TestShimByteCompatibleForAllNineControllers and the four §7.3 condition tests",
		},
		Artefacts: map[string]string{
			"byte-diff.csv":          "one row per legacy read route: expected and actual byte length, sha256 of each, byte-identical verdict, and the offset of the first difference. The blocking artefact.",
			"byte-diff-summary.json": "the route counts by status.",
		},
		Notes: gate7Notes(counts),
	}); err != nil {
		return err
	}

	for _, c := range conditions {
		verdict := "FAIL"
		if c.Passed {
			verdict = "pass"
		}
		fmt.Printf("  %-10s %-5s %s\n", c.ID, verdict, c.Measurement)
	}
	for _, c := range conditions {
		if !c.Passed {
			return fmt.Errorf("GATE 7 FAILED — see %s", filepath.Join(dir, "SUMMARY.md"))
		}
	}
	fmt.Printf("gate7: PASS — %s\n", filepath.Join(dir, "SUMMARY.md"))
	return nil
}

func readReport(path string) ([]map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is this program's own output directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing byte-diff.csv: %w", err)
	}
	if len(records) < 2 {
		return nil, errors.New("byte-diff.csv holds no rows — the verification did not compare anything")
	}
	header := records[0]
	var out []map[string]string
	for _, record := range records[1:] {
		row := map[string]string{}
		for i, key := range header {
			if i < len(record) {
				row[key] = record[i]
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func tally(rows []map[string]string) map[string]int {
	counts := map[string]int{}
	for _, row := range rows {
		status := row["status"]
		counts[status]++
		httpStatus, _ := strconv.Atoi(row["http_status"])
		switch status {
		case "served":
			if row["byte_identical"] == "true" {
				counts["identical"]++
			}
		case "breaking":
			if httpStatus == 410 {
				counts["breaking_410"]++
			}
		case "unshimmable", "pending":
			if httpStatus == 501 {
				counts["unserved_501"]++
			}
		}
	}
	return counts
}

func suiteResult(err error) string {
	if err == nil {
		return "go test exited 0"
	}
	return "go test FAILED: " + err.Error()
}

func gate7Notes(counts map[string]int) string {
	served := counts["served"]
	total := served + counts["pending"] + counts["unshimmable"] + counts["breaking"]
	return fmt.Sprintf(
		"Gate 7 is %d of %d served, and that is a PRODUCT DECISION recorded as such rather than a defect: "+
			"%d routes are permanently unservable and %d are breaking by construction. Each carries its "+
			"reason in the `reason` column of byte-diff.csv, re-derived against legacy's source — a "+
			"surrogate auto-increment key on the wire, MySQL double rounding, an identity space HANGAR "+
			"does not share. A shim that invented values for those would be byte-compatible with the "+
			"recording and WRONG on every real installation.\n\n"+
			"\"Byte-identical\" here is the strict standard §7.2 defines: field order, JSON whitespace, "+
			"numeric formatting and null-vs-absent are all in scope. Comparing parsed objects would not "+
			"satisfy this gate, and the report records a sha256 of both sides so the claim can be checked "+
			"rather than taken.\n\n"+
			"One caveat the report cannot state for itself: five served routes rest on single-row "+
			"recordings, so their own multi-row ORDERING is pinned by inference from the ordering rule "+
			"measured elsewhere, not by their own recording. See docs/PRE_V1_OPEN_ITEMS.md N-3.",
		served, total, counts["unshimmable"], counts["breaking"])
}
