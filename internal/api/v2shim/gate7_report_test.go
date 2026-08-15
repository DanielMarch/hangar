//go:build integration

package v2shim_test

// gate7_report_test.go turns Gate 7's byte comparison into the artefact
// 04_RELEASE_GATES.md §8 requires: "byte-diff report over the corpus".
//
// ── WHY THIS IS EMITTED FROM THE TEST AND NOT BY A SEPARATE PROGRAM ──────
// The comparison already exists, is correct, and is what gates the release:
// TestShimByteCompatibleForAllNineControllers requests every route through
// the real handler chain and compares the response BYTES against the
// recording. A separate reporting program would have to rebuild the
// fixtures, the credential and the handler, and would then be a SECOND
// implementation of the comparison — free to drift from the one that
// actually blocks the release, and to report a pass the gate would not.
//
// So the report is written from inside the assertions instead. It is a
// transcript of the verification that ran, and it cannot describe a
// comparison that did not happen. tools/gate7-bytediff invokes the test
// with HANGAR_GATE7_EVIDENCE_DIR set and turns this output into the gate's
// SUMMARY.md.
//
// A row is written BEFORE the assertion that could fail, deliberately: a
// route whose bytes differ must appear in the report as a difference, and a
// report that only ever describes passes is not evidence.

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/api/v2shim"
)

// gate7Row is one route's line in byte-diff.csv.
type gate7Row struct {
	Controller  string
	Pattern     string
	Status      string
	Corpus      string
	HTTPStatus  int
	ExpectedLen int
	ActualLen   int
	ExpectedSHA string
	ActualSHA   string
	Identical   bool
	FirstDiffAt int
	Reason      string
}

type gate7Report struct {
	dir  string
	mu   sync.Mutex
	rows []gate7Row
}

func newGate7Report(dir string) *gate7Report { return &gate7Report{dir: dir} }

// served records a byte comparison against a recording.
func (r *gate7Report) served(route v2shim.LegacyRoute, expected, actual []byte, httpStatus int) {
	if r == nil || r.dir == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, gate7Row{
		Controller: route.Controller, Pattern: route.Pattern, Status: string(route.Status),
		Corpus: route.Corpus, HTTPStatus: httpStatus,
		ExpectedLen: len(expected), ActualLen: len(actual),
		ExpectedSHA: sha(expected), ActualSHA: sha(actual),
		Identical:   string(expected) == string(actual),
		FirstDiffAt: firstDifference(expected, actual),
	})
}

// unserved records a route that is deliberately not byte-compared: a 410
// breaking change, or a 501 for an unshimmable or pending route. They are
// in the report because Gate 7's number is "16 of 34", and a report holding
// only the 16 would not let a reader check the other 18.
func (r *gate7Report) unserved(route v2shim.LegacyRoute, httpStatus int) {
	if r == nil || r.dir == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, gate7Row{
		Controller: route.Controller, Pattern: route.Pattern, Status: string(route.Status),
		Corpus: route.Corpus, HTTPStatus: httpStatus, FirstDiffAt: -1,
		Reason: firstLine(route.Reason),
	})
}

// write emits byte-diff.csv and summary.json into the evidence directory.
func (r *gate7Report) write(t testing.TB) {
	if r == nil || r.dir == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	require.NoError(t, os.MkdirAll(r.dir, 0o750))
	sort.Slice(r.rows, func(i, j int) bool {
		if r.rows[i].Status != r.rows[j].Status {
			return r.rows[i].Status < r.rows[j].Status
		}
		return r.rows[i].Pattern < r.rows[j].Pattern
	})

	// encoding/csv rather than Fprintf with %q. A reason is prose written by
	// a human and several of them contain quotation marks; Go's %q escapes
	// those as \" , which is Go syntax and NOT CSV — CSV doubles them. The
	// first version of this writer produced a file its own reader could not
	// parse, which is a good argument for never hand-rolling the format.
	var b strings.Builder
	w := csv.NewWriter(&b)
	require.NoError(t, w.Write([]string{
		"controller", "pattern", "status", "corpus", "http_status",
		"expected_bytes", "actual_bytes", "expected_sha256", "actual_sha256",
		"byte_identical", "first_difference_at", "reason",
	}))

	var served, identical int
	byStatus := map[string]int{}
	for _, row := range r.rows {
		byStatus[row.Status]++
		if row.Status == string(v2shim.StatusServed) {
			served++
			if row.Identical {
				identical++
			}
		}
		require.NoError(t, w.Write([]string{
			row.Controller, row.Pattern, row.Status, row.Corpus, strconv.Itoa(row.HTTPStatus),
			strconv.Itoa(row.ExpectedLen), strconv.Itoa(row.ActualLen),
			row.ExpectedSHA, row.ActualSHA,
			strconv.FormatBool(row.Identical), strconv.Itoa(row.FirstDiffAt), row.Reason,
		}))
	}
	w.Flush()
	require.NoError(t, w.Error())
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "byte-diff.csv"), []byte(b.String()), 0o600))

	summary := fmt.Sprintf(`{
  "routes_total": %d,
  "served": %d,
  "served_byte_identical": %d,
  "pending": %d,
  "unshimmable": %d,
  "breaking": %d
}
`, len(r.rows), served, identical,
		byStatus[string(v2shim.StatusPending)], byStatus[string(v2shim.StatusUnshimmable)],
		byStatus[string(v2shim.StatusBreaking)])
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "byte-diff-summary.json"), []byte(summary), 0o600))
}

func sha(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// firstDifference returns the byte offset at which two responses diverge,
// or -1 when they are identical. It is the single most useful number in the
// report when a route fails: "differs at byte 412" points at a field, where
// "not byte-identical" points at 40KB of JSON.
func firstDifference(expected, actual []byte) int {
	limit := len(expected)
	if len(actual) < limit {
		limit = len(actual)
	}
	for i := 0; i < limit; i++ {
		if expected[i] != actual[i] {
			return i
		}
	}
	if len(expected) != len(actual) {
		return limit
	}
	return -1
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
