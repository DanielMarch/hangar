//go:build reachability

package reachability

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ── WHY THIS TEST EXISTS, AND WHY IT IS SEPARATE ─────────────────────────
// Phase 20.1's reachability guard runs x/tools/cmd/deadcode, which by design
// "does not report dead functions in generated files". internal/store/gen is
// generated. So the ENTIRE store layer — 442 query methods, the widest
// surface in the codebase — was invisible to the guard built specifically to
// catch unwired subsystems.
//
// That is where defect B42 hid, and B42 is the most consequential instance
// of the B20 pattern found so far: nothing in production ever called
// UpsertSyncSubscription, so no entity was ever subscribed to any ESI route,
// so the planner claimed due work and found none, forever. A character could
// authorise with all 46 scopes and HANGAR would never fetch anything. The
// dashboard even said "/status is scheduled but has not completed a
// successful run" — it was not scheduled.
//
// deadcode's -generated flag would include generated files, but it reports
// per-FUNCTION and the generated layer is better checked per-QUERY against
// the Querier interface, which is the authoritative list. Hence a separate,
// simpler check rather than a flag flip.
func TestEveryGeneratedQueryHasAProductionCaller(t *testing.T) {
	repoRoot := findRepoRoot(t)

	querierPath := filepath.Join(repoRoot, "internal", "store", "gen", "querier.go")
	querier, err := os.ReadFile(querierPath)
	if err != nil {
		t.Fatalf("reading the generated Querier interface: %v", err)
	}
	methodRe := regexp.MustCompile(`(?m)^\t([A-Z]\w+)\(ctx context\.Context`)
	var methods []string
	seen := map[string]bool{}
	for _, m := range methodRe.FindAllStringSubmatch(string(querier), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			methods = append(methods, m[1])
		}
	}
	if len(methods) < 100 {
		t.Fatalf("only found %d Querier methods — the parse is wrong, not the codebase", len(methods))
	}

	// Read every non-generated, non-test .go file once. Grepping per method
	// would be 442 subprocesses.
	production := readGoSources(t, repoRoot, false)

	allowed, reasons := loadAllowlist(t, filepath.Join(repoRoot, "test", "reachability", "generated_allowlist.txt"))

	var undeclared, stale []string
	for _, method := range methods {
		called := containsIdentifier(production, method)
		switch {
		case !called && !allowed[method]:
			undeclared = append(undeclared, method)
		case called && allowed[method]:
			stale = append(stale, method+"  (listed as: "+reasons[method]+")")
		}
	}
	sort.Strings(undeclared)
	sort.Strings(stale)

	if len(undeclared) > 0 {
		t.Errorf(`%d generated store quer(y/ies) have no production caller and are not declared:

%s

A query nothing calls is a capability nothing delivers. This is how B42 —
"no entity is ever subscribed to any ESI route" — survived to Phase 20.

Wire it up, or add it to test/reachability/generated_allowlist.txt under a
heading that says which defect it belongs to and which phase closes it.`,
			len(undeclared), "  "+strings.Join(undeclared, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf(`%d allowlist entr(y/ies) now have a production caller and must be removed:

%s

Delete them from test/reachability/generated_allowlist.txt in the same commit
that wired them, so the file keeps meaning "what is knowingly unused".`,
			len(stale), "  "+strings.Join(stale, "\n  "))
	}

	t.Logf("generated queries: %d total, %d without a production caller, all accounted for",
		len(methods), len(allowed))
}

// readGoSources concatenates every .go file under repoRoot, skipping the
// generated store package and (unless wantTests) _test.go files.
func readGoSources(t *testing.T, repoRoot string, wantTests bool) string {
	t.Helper()
	var b strings.Builder
	genDir := filepath.Join("internal", "store", "gen")

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "web" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		if strings.HasPrefix(rel, genDir) {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") != wantTests {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		b.Write(src)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("walking the source tree: %v", err)
	}
	return b.String()
}

// containsIdentifier reports whether name appears in src as a whole
// identifier, so `GetAsset` does not match `GetAssetTree`.
func containsIdentifier(src, name string) bool {
	for i := 0; ; {
		j := strings.Index(src[i:], name)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(name)
		beforeOK := start == 0 || !isIdentRune(src[start-1])
		afterOK := end >= len(src) || !isIdentRune(src[end])
		if beforeOK && afterOK {
			return true
		}
		i = end
	}
}

func isIdentRune(c byte) bool {
	return c == '_' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}
