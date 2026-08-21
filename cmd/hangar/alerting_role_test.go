package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── PHASE 23 (N-9): THE ALERTING SEAM GETS THE GUARD THE WORKER SEAM HAS ──
//
// This is the THIRD time a seam wired in one process only has been a
// defect:
//
//	B-25  the alert PRODUCERS existed and nothing called them, so §4.4's
//	      delivery half was structurally incapable of ever firing;
//	B-6   river.AddWorker lived only in work.go while the stock compose
//	      runs only `serve`, so no job was ever consumed;
//	N-9   wireAlertGeneration, runThresholdEvaluator, runAlertDispatcher
//	      and ensureDefaultAlertChannels lived only in work.go, so a
//	      default installation produced no alert event and delivered no
//	      message.
//
// Each was cheap to fix and cost a phase to find, and none was visible to
// any test, because every test constructs the thing it is testing. What no
// test could see is which PROCESS constructs it. These two do, the same way
// workers_test.go does for the River pool, and they are deliberately
// structural — they read the source rather than run it, because the defect
// is a missing call and a missing call has no runtime behaviour to assert.

// alertingRoleEntryPoints are the four calls that WERE copied into one
// role only. They must now appear in the assembly and nowhere else, so a
// producer added for one role cannot go missing from the other.
var alertingRoleEntryPoints = []string{
	"wireAlertGeneration",
	"ensureDefaultAlertChannels",
	"runAlertDispatcher",
	"runThresholdEvaluator",
}

// TestAlertingPipelineIsAssembledInOnePlace forbids the copy that caused
// N-9. If it fails, do not add your file to an exemption: move the call
// into buildAlertingRole or alertingRole.Start.
func TestAlertingPipelineIsAssembledInOnePlace(t *testing.T) {
	const home = "alerting.go"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	wanted := map[string]bool{}
	for _, name := range alertingRoleEntryPoints {
		wanted[name] = true
	}
	found := map[string]int{}
	offenders := map[string][]string{}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// A bare identifier call — these are all package-level
			// functions in package main, so a SelectorExpr here would be
			// somebody else's method of the same name.
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || !wanted[ident.Name] {
				return true
			}
			found[ident.Name]++
			if name != home {
				offenders[name] = append(offenders[name], ident.Name)
			}
			return true
		})
	}

	for _, name := range alertingRoleEntryPoints {
		if found[name] == 0 {
			t.Errorf("%s is called from nowhere in package main. That is defect B-25's shape: §4.4's pipeline "+
				"exists, is tested, and cannot fire on any installation", name)
		}
	}
	for file, calls := range offenders {
		t.Errorf("%s calls %s directly. Every part of §4.4's pipeline must be assembled in %s, which BOTH `serve` "+
			"and `work` call — wiring it into one role is how a default installation ended up delivering no alerts (N-9)",
			file, strings.Join(calls, ", "), home)
	}
}

// TestBothProcessRolesStartTheAlertingRole is the other half, and the one
// that states the actual defect: an assembly nobody calls is worth exactly
// as much as no assembly. `serve` not calling it WAS N-9.
func TestBothProcessRolesStartTheAlertingRole(t *testing.T) {
	for _, file := range []string{"serve.go", "work.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		source := string(body)
		if !strings.Contains(source, "buildAlertingRole(") {
			t.Errorf("%s does not call buildAlertingRole. 01_ARCHITECTURE.md §2: \"Single-process default. "+
				"`serve` does everything; `work`/`schedule` exist for administrators who have outgrown one box.\" "+
				"A role that serves an installation and delivers none of its alerts is defect N-9", file)
		}
		if !strings.Contains(source, ".Start(") {
			t.Errorf("%s builds the alerting role and never starts it — an unstarted pump claims nothing, "+
				"which is indistinguishable from having no pump at all", file)
		}
	}
}

// TestBothProcessRolesRegisterGateThreeMetrics guards the corollary. Both
// roles run the pump now, so both must export what it does: `serve` passed
// a literal nil for alert_delivery_total and alert_dead_letter_depth until
// this phase, which is what a metric looks like when the subsystem it
// measures does not run in the process exporting it.
func TestBothProcessRolesRegisterGateThreeMetrics(t *testing.T) {
	for _, file := range []string{"serve.go", "work.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		source := string(body)
		if !strings.Contains(source, "Deliveries") || !strings.Contains(source, "DeadLetters") {
			t.Errorf("%s does not pass the alerting role's Gate 3 metrics to buildMetricsRegistry. "+
				"A process that settles deliveries and exports no counter for them makes Gate 3 unmeasurable "+
				"from the only process a stock docker-compose.yml runs", file)
		}
	}
}
