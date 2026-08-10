package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestZeroOffsetInGeneratedSQL — Phase 15 exit criterion: "no OFFSET
// anywhere in sqlc output". sqlc.yaml's `no-offset` rule already enforces
// this at generate time (`go tool sqlc vet`, part of `make lint`), but that
// only runs when db/queries has content and a rule is configured — this
// test is a second, always-on proof that scans the generated Go directly,
// so a future rule regression (or a hand-edited generated file, which
// should never happen but `DO NOT EDIT` headers don't enforce themselves)
// still fails `go test` even if `sqlc vet` is skipped.
func TestZeroOffsetInGeneratedSQL(t *testing.T) {
	root := findRepoRoot(t)
	genDir := filepath.Join(root, "internal", "store", "gen")
	entries, err := os.ReadDir(genDir)
	if err != nil {
		t.Fatalf("reading %s: %v", genDir, err)
	}
	offsetRe := regexp.MustCompile(`(?i)\bOFFSET\b`)
	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(genDir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		// Only the embedded SQL string literals matter — a Go // comment
		// mentioning the word "OFFSET" (several files document the
		// no-offset rule itself) is not a violation.
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if offsetRe.MatchString(line) {
				t.Errorf("%s: line contains OFFSET outside a comment — Principle: OFFSET is banned outright (SRS §17 invariant 10): %q", e.Name(), trimmed)
				found++
			}
		}
	}
	if found == 0 {
		t.Logf("scanned generated SQL files under %s, zero OFFSET occurrences", genDir)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod not found)")
		}
		dir = parent
	}
}
