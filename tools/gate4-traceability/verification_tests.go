package main

// ── DEFECT B51 (PHASE 20.7): THE EVIDENCE COLUMN NOBODY CHECKED ──────────
//
// traceability.csv's `verification_test` column is what makes a capability
// row's `verified` status mean anything: it names the test that proves the
// capability works. Every value in it was hand-written, and nothing ever
// confirmed the named test EXISTS.
//
// Measured in Phase 20.7 against the repository: of the 45 capability rows
// naming a test, THREE named a test that exists and FORTY-TWO named one that
// does not — TestSyncAssets, TestSyncMarketPrices, TestSyncCorporationProjects,
// TestSyncServerStatus and thirty-eight others are not defined anywhere in
// the tree. So the artefact that certifies Gate 4 was, for 93% of its rows,
// asserting evidence that had never been written.
//
// That is the same defect class this phase spent its time on, one layer up.
// B48 was an endpoint with no writer; B50 was a DTO matching no real
// response; this is a gate artefact citing no real test. In every case the
// claim looked fine precisely because nothing checked it.
//
// The fix is the one this phase applied everywhere else: stop asserting,
// start measuring. checkVerificationTests scans the repository for Go test
// functions and reports every named-but-absent test as a Gate 4 failure, so
// the CSV cannot silently cite a test again. It deliberately does NOT
// invent the missing tests — writing 42 tests is a phase's own work, and a
// recorded failure is the correct artefact for it in the meantime
// (04_RELEASE_GATES.md §0.4).

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	testFuncRe = regexp.MustCompile(`(?m)^func (Test\w+)`)

	// skippedDirs are trees with no Go tests worth scanning. web/node_modules
	// in particular is large enough that walking it dominates the run.
	skippedDirs = map[string]bool{
		".git": true, "node_modules": true, "dist": true, "bin": true,
	}
)

// collectGoTestNames returns every Go test function name defined in the
// repository.
func collectGoTestNames(repoRoot string) (map[string]bool, error) {
	names := map[string]bool{}
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
		for _, m := range testFuncRe.FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// checkVerificationTests reports every capability row whose named
// verification test does not exist in the tree.
//
// A row whose Test field is parenthetical — "(none — no sync handler
// exists)" and its kin — is NOT reported here. Those are explicit
// statements that there is no test, which is honest; the row's own
// unreachable/deferred status is what carries that failure, and reporting it
// twice would double-count one problem. What this catches is the other case:
// a row that names a specific test, confidently, and is wrong.
func checkVerificationTests(repoRoot string, rows []CapabilityRow) ([]string, error) {
	defined, err := collectGoTestNames(repoRoot)
	if err != nil {
		return nil, err
	}

	var missing []string
	for _, row := range rows {
		name := strings.TrimSpace(row.VerificationTest)
		if name == "" || strings.HasPrefix(name, "(") {
			continue
		}
		// A field may name more than one test, space- or comma-separated.
		for _, candidate := range strings.FieldsFunc(name, func(r rune) bool {
			return r == ' ' || r == ',' || r == '/'
		}) {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" || !strings.HasPrefix(candidate, "Test") {
				continue
			}
			if !defined[candidate] {
				missing = append(missing, fmt.Sprintf("capability %d names %s, which is not defined anywhere in the tree", row.ID, candidate))
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}
