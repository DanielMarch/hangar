package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Baseline is docs/BASELINE.md's measured legacy footprint.
//
// ── WHY PARSED AND NOT RESTATED (defect B6) ──────────────────────────────
// SRS Appendix B states these counts and BASELINE.md measures them, and the
// SRS is explicit that "Gate 4 compares against BASELINE.md, not against
// this document". Restating either number in Go would make this program a
// third place they live, which is how the two got to disagree in the first
// place.
type Baseline struct {
	ESIRoutes     int
	UIControllers int
	AlertTypes    int
	Locales       int
	V2Controllers int
	// Capabilities is not in BASELINE.md's summary table — it is Gate 4.1's
	// own requirement ("58 capability rows") and comes from the SRS.
	Capabilities int
}

// summaryRow matches a row of BASELINE.md's Summary table:
//
//	| Distinct ESI routes | 106 | 106 | ✅ |
var summaryRow = regexp.MustCompile(`^\|\s*([^|]+?)\s*\|\s*(\d+)\s*\|\s*(\d+)\s*\|\s*(.*?)\s*\|\s*$`)

// ParseBaseline reads the Summary table at the end of BASELINE.md.
//
// It reads the MEASURED column, not the expected one, and fails if the two
// disagree — which is §4.1's rule stated as code: "If a measured count
// disagrees with the SRS, that disagreement is a specification defect to be
// raised and resolved before the gate can pass. Adopting either number
// silently is a Principle 15 violation."
func ParseBaseline(path string) (Baseline, error) {
	file, err := os.Open(path)
	if err != nil {
		return Baseline{}, fmt.Errorf("opening BASELINE.md: %w", err)
	}
	defer func() { _ = file.Close() }()

	baseline := Baseline{Capabilities: 58}
	inSummary := false
	found := 0

	scan := bufio.NewScanner(file)
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "## Summary") {
			inSummary = true
			continue
		}
		if !inSummary {
			continue
		}
		match := summaryRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		dimension := match[1]
		expected, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		measured, err := strconv.Atoi(match[3])
		if err != nil {
			continue
		}
		if expected != measured {
			return Baseline{}, fmt.Errorf(
				"BASELINE.md: %q expected %d but measured %d — a specification defect that blocks Gate 4 (§4.1)",
				dimension, expected, measured)
		}

		switch {
		case strings.Contains(dimension, "Distinct ESI routes"):
			baseline.ESIRoutes, found = measured, found+1
		case strings.Contains(dimension, "UI controller"):
			baseline.UIControllers, found = measured, found+1
		case strings.Contains(dimension, "alert types"):
			baseline.AlertTypes, found = measured, found+1
		case strings.Contains(dimension, "UI locales"):
			baseline.Locales, found = measured, found+1
		case strings.Contains(dimension, "/api/v2` controllers"), strings.Contains(dimension, "api/v2"):
			baseline.V2Controllers, found = measured, found+1
		}
	}
	if err := scan.Err(); err != nil {
		return Baseline{}, fmt.Errorf("reading BASELINE.md: %w", err)
	}
	if found < 5 {
		return Baseline{}, fmt.Errorf(
			"BASELINE.md's Summary table yielded only %d of 5 dimensions — the parse is wrong, not the document", found)
	}
	return baseline, nil
}
