//go:build reachability

// Package reachability holds the guard that makes defect class B20
// impossible to reintroduce silently.
//
// ── WHAT THIS EXISTS TO CATCH ────────────────────────────────────────────
// A subsystem can be fully built, fully tested and never called, because
// the phase that builds a component and the phase that wires it up are
// usually the same phase — and when the wiring is one line, it is the line
// that gets forgotten. Nothing fails: the package's own tests construct
// what they need, the suite is green, and the feature is inert.
//
// That went undetected for eighteen phases. B20 (catalogue.Boot) was found
// by hand; B22 and B23 were found by asking the question directly of two
// packages; the Phase 20 audit asked it of every package mechanically and
// found thirteen more. A grep cannot find these — the symbol IS in the
// source, sitting in a doc comment describing behaviour that does not
// happen. Only reachability analysis from main answers the real question.
//
// This test shells out to `go build` and to the deadcode tool, so it is
// excluded from the default `go test ./...` run — see the Makefile's
// check-reachability target, which runs
// `go test -tags=reachability ./test/reachability/...`.
package reachability

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// analysisGOOS/GOARCH pin the ANALYSIS target, independent of the host.
//
// deadcode's own documentation warns that "the analysis is valid only for a
// single GOOS/GOARCH configuration, so a function reported as dead may be
// live in a different configuration". HANGAR deploys to linux containers
// (SRS §9.1), so linux/amd64 is the configuration whose answer matters, and
// pinning it means a Windows developer and Linux CI compute the same
// allowlist instead of disagreeing about platform-specific files.
const (
	analysisGOOS   = "linux"
	analysisGOARCH = "amd64"
)

// deadcodeFormat prints one `package/path.FuncName` per unreachable
// function. Deliberately NOT the default diagnostic format, which leads
// with file:line:col — the allowlist must be keyed on something stable, and
// a line number changes every time anyone edits the file above it.
const deadcodeFormat = `{{range .Funcs}}{{printf "%s.%s\n" $.Path .Name}}{{end}}`

const modulePrefix = "github.com/hangar-project/hangar/"

func TestEveryProductionCallerIsAccountedFor(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Build the tool for the HOST, then run it with the analysis target in
	// its environment. Setting GOOS on `go tool deadcode` directly would
	// cross-compile the tool itself and then fail to execute it.
	toolPath := filepath.Join(t.TempDir(), "deadcode")
	if runtime.GOOS == "windows" {
		toolPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", toolPath, "golang.org/x/tools/cmd/deadcode")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the deadcode tool failed: %v\n%s", err, out)
	}

	analyse := exec.Command(toolPath, "-test=false", "-f="+deadcodeFormat, "./cmd/hangar")
	analyse.Dir = repoRoot
	analyse.Env = append(os.Environ(), "GOOS="+analysisGOOS, "GOARCH="+analysisGOARCH)
	out, err := analyse.Output()
	if err != nil {
		t.Fatalf("running deadcode failed: %v\n%s", err, out)
	}

	unreachable := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), modulePrefix))
		if line != "" {
			unreachable[line] = true
		}
	}

	allowed, reasons := loadAllowlist(t, filepath.Join(repoRoot, "test", "reachability", "allowlist.txt"))

	// ── HALF ONE: something new lost its production caller ───────────────
	var undeclared []string
	for sym := range unreachable {
		if !allowed[sym] {
			undeclared = append(undeclared, sym)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf(`%d symbol(s) are unreachable from cmd/hangar and not in the allowlist:

%s

This is defect class B20: code that is built, tested, and never called on any
path a running installation takes. Its own package tests pass, so nothing else
will tell you.

Resolve it by WIRING IT UP, not by adding a line here. Add to
test/reachability/allowlist.txt only when the symbol is a deliberate keep — an
unused helper, a test double, or API published for callers outside this binary
— and say which, in the comment. An entry with no reason is not a decision, it
is the defect with a note attached.`,
			len(undeclared), "  "+strings.Join(undeclared, "\n  "))
	}

	// ── HALF TWO: an allowlist entry has become reachable ────────────────
	// Just as important. An allowlist that only ever grows decays into a
	// list of things nobody has looked at since, which is exactly how B20
	// survived eighteen phases. When a sub-phase wires a subsystem up, this
	// half of the test is what forces the register to be updated in the
	// same commit.
	var stale []string
	for sym := range allowed {
		if !unreachable[sym] {
			stale = append(stale, fmt.Sprintf("%s  (listed as: %s)", sym, reasons[sym]))
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf(`%d allowlist entr(y/ies) are now REACHABLE and must be removed:

%s

This is good news — something got wired up. Delete these lines from
test/reachability/allowlist.txt in the same commit that wired them, so the
file keeps meaning "what is knowingly inert" rather than "what was inert once".`,
			len(stale), "  "+strings.Join(stale, "\n  "))
	}

	t.Logf("reachability: %d unreachable symbols, all accounted for (%s/%s)",
		len(unreachable), analysisGOOS, analysisGOARCH)
}

// loadAllowlist reads the allowlist, returning the symbol set and each
// symbol's stated reason. A `#` begins a comment, either on its own line or
// trailing a symbol.
func loadAllowlist(t *testing.T, path string) (map[string]bool, map[string]string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening allowlist: %v", err)
	}
	defer func() { _ = f.Close() }()

	allowed := map[string]bool{}
	reasons := map[string]string{}
	var section string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			// A section header carries the reason for the symbols beneath
			// it, so an entry never has to repeat it.
			section = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			continue
		}
		sym, trailing, hasTrailing := strings.Cut(line, "#")
		sym = strings.TrimSpace(sym)
		if sym == "" {
			continue
		}
		allowed[sym] = true
		if hasTrailing {
			reasons[sym] = strings.TrimSpace(trailing)
		} else {
			reasons[sym] = section
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading allowlist: %v", err)
	}
	if len(allowed) == 0 {
		// An empty allowlist would make half one vacuously strict and half
		// two vacuously true. If the file is ever legitimately empty, the
		// codebase has no inert subsystems and this line should be deleted
		// deliberately.
		t.Fatal("allowlist is empty — that is almost certainly a parse failure, not a clean codebase")
	}
	return allowed, reasons
}

// TestNoPackageIsAbsentFromTheBinary is the coarser, stronger half of the
// audit: a package absent from `cmd/hangar`'s transitive imports cannot
// execute in production under ANY input. deadcode's function-level result
// subsumes this, but a whole missing package is a categorically different
// severity from an unused helper — internal/sde (B22) and internal/i18n
// (B23) were both this — and it deserves its own named failure.
func TestNoPackageIsAbsentFromTheBinary(t *testing.T) {
	repoRoot := findRepoRoot(t)

	all := goList(t, repoRoot, "./...")
	reachable := goList(t, repoRoot, "-deps", "./cmd/hangar")
	reachableSet := map[string]bool{}
	for _, p := range reachable {
		reachableSet[p] = true
	}

	// Packages legitimately outside the binary: standalone generator mains
	// and the build-tagged test harnesses under test/.
	skip := func(pkg string) bool {
		return strings.HasPrefix(pkg, "tools/") ||
			strings.HasPrefix(pkg, "test/") ||
			strings.HasSuffix(pkg, "/gen")
	}

	var absent []string
	for _, pkg := range all {
		short := strings.TrimPrefix(pkg, modulePrefix)
		if skip(short) || reachableSet[pkg] {
			continue
		}
		absent = append(absent, short)
	}
	sort.Strings(absent)

	// The packages currently known to be absent, each with its defect.
	// Same contract as the allowlist: this shrinks, and an entry that has
	// become reachable is a failure.
	known := map[string]string{
		"internal/sde":            "B22 — the whole SDE import pipeline; closed by Phase 20.5",
		"internal/i18n":           "B23 — locale resolution, incl. capability 58; closed by Phase 20.2",
		"internal/esi/pagination": "B31 — dead duplicate of sync/worker's page-walker; closed by Phase 20.2",
	}

	for _, pkg := range absent {
		if _, ok := known[pkg]; !ok {
			t.Errorf("package %s is not imported by cmd/hangar — it cannot run in production under any input. "+
				"Wire it up, or record it in this test's `known` map with its defect id.", pkg)
		}
	}
	for pkg, why := range known {
		if reachableSet[modulePrefix+pkg] {
			t.Errorf("package %s is now reachable — remove it from this test's `known` map (%s)", pkg, why)
		}
	}
}

func goList(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+analysisGOOS, "GOARCH="+analysisGOARCH)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v failed: %v", args, err)
	}
	var pkgs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, modulePrefix) || line == strings.TrimSuffix(modulePrefix, "/") {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs
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
			t.Fatal("could not find repository root (no go.mod in any parent)")
		}
		dir = parent
	}
}
