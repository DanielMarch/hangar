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

// minAssertions is how many assertions a cited test must make before this
// program will accept it as evidence.
//
// ── WHY EXISTENCE IS NOT ENOUGH (PHASE 20.8) ─────────────────────────────
// 20.7's checker asked one question — does the named test exist — and that
// was the right question then, because forty-two of them did not. Once they
// do, the question has a new wrong answer available: a function named
// TestSyncAssets that asserts nothing satisfies an existence check and is a
// WORSE artefact than the missing test was, because it looks checked.
//
// Three is deliberately low. It is not a quality bar and cannot be: no
// counting rule distinguishes a good test from a bad one. It is a floor
// under the specific failure this file exists to prevent — the empty
// function written to make a gate pass — and every real test in this tree
// clears it by a wide margin.
const minAssertions = 3

var (
	testFuncRe = regexp.MustCompile(`(?m)^func (Test\w+)`)

	// assertionRe counts the ways this tree's tests assert. testify's
	// require/assert dominate; t.Error/t.Fatal cover the handful of
	// hand-rolled checks (internal/api's generated_test.go, the reachability
	// suite) that predate it; and `requireSomething(` catches the shared
	// helpers test/capability's harness factors out — requireDispatched,
	// requireEndpoints, requireDTOCoversSpec. Those ARE assertions, each
	// wrapping several require calls of its own, and not counting them would
	// penalise the tests that factored their checks out properly.
	assertionRe = regexp.MustCompile(`\b(?:require|assert)\.\w+\(|\bt\.(?:Error|Errorf|Fatal|Fatalf)\(|\brequire[A-Z]\w*\(`)

	// skippedDirs are trees with no Go tests worth scanning. web/node_modules
	// in particular is large enough that walking it dominates the run.
	skippedDirs = map[string]bool{
		".git": true, "node_modules": true, "dist": true, "bin": true,
	}
)

// collectGoTestNames returns every Go test function name defined in the
// repository, mapped to how many assertions its body makes.
//
// The body is taken as the text between one `func Test...` and the next
// top-level `func`, which is exact for gofmt'd source: gofmt puts every
// top-level declaration at column zero and nothing else there.
func collectGoTestNames(repoRoot string) (map[string]int, error) {
	names := map[string]int{}
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
		text := string(src)
		for _, loc := range testFuncRe.FindAllStringSubmatchIndex(text, -1) {
			name := text[loc[2]:loc[3]]
			body := text[loc[1]:nextTopLevelFunc(text, loc[1])]
			n := len(assertionRe.FindAllString(body, -1))
			// A name may legitimately be defined twice — a unit half and a
			// build-tagged integration half. Keep the LARGER count so the
			// order of the directory walk cannot change the verdict.
			if existing, seen := names[name]; !seen || n > existing {
				names[name] = n
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// nextTopLevelFunc returns the offset of the next top-level `func` at or
// after from, or len(text) if there is none. Column zero is the whole test:
// gofmt indents everything inside a declaration, so a `func` at the start of
// a line is always the next declaration and never a closure.
func nextTopLevelFunc(text string, from int) int {
	if idx := strings.Index(text[from:], "\nfunc "); idx >= 0 {
		return from + idx
	}
	return len(text)
}

// checkVerificationTests reports every capability row whose named
// verification test does not exist in the tree, or exists and asserts
// almost nothing.
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
			assertions, exists := defined[candidate]
			switch {
			case !exists:
				missing = append(missing, fmt.Sprintf(
					"capability %d names %s, which is not defined anywhere in the tree", row.ID, candidate))
			case assertions < minAssertions:
				missing = append(missing, fmt.Sprintf(
					"capability %d names %s, which exists but makes only %d assertion(s) — a test that asserts "+
						"nothing is worse evidence than a missing one, because it looks checked",
					row.ID, candidate, assertions))
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}
