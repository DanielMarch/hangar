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

// TestEveryRiverWorkerIsRegisteredInOnePlace is defect B-6's guard, and it
// guards the SHAPE of the defect rather than one instance of it.
//
// B-6 was not a bug inside a worker. It was `river.AddWorker` living in
// exactly one process role's file — cmd/hangar/work.go — while the stock
// docker-compose.yml runs only the other one. The planner enqueued
// `sync_route`, `provision_urgent` and `provision_bulk` jobs on every
// default installation and nothing consumed any of them, for six phases,
// with every test green: each test constructs the worker it needs, so no
// test could see which PROCESS registers it.
//
// A copy is how that happened, so a copy is what this forbids. Every
// AddWorker call must be in cmd/hangar/workers.go, which both `serve` and
// `work` call — and a worker added for one role therefore cannot go
// missing from the other.
//
// If this test fails, do not add the file to the exemption. Move the
// AddWorker call into buildWorkerPool.
func TestEveryRiverWorkerIsRegisteredInOnePlace(t *testing.T) {
	const home = "workers.go"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	offenders := map[string]int{}
	found := 0

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
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "AddWorker" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "river" {
				return true
			}
			found++
			if name != home {
				offenders[name]++
			}
			return true
		})
	}

	if found == 0 {
		t.Fatalf("no river.AddWorker call found in package main at all — that is precisely defect B-6: " +
			"the planner enqueues jobs and no process registers a worker for them")
	}
	for file, n := range offenders {
		t.Errorf("%s registers %d River worker(s). Every worker must be registered in %s, which BOTH `serve` and "+
			"`work` call — registering one here is how the default deployment ended up with a planner and no consumers",
			file, n, home)
	}
}

// TestBothProcessRolesBuildTheWorkerPool is the other half: workers.go
// being the only place they are registered is worth nothing if a role
// never calls it. `serve` not calling it WAS the defect.
func TestBothProcessRolesBuildTheWorkerPool(t *testing.T) {
	for _, file := range []string{"serve.go", "work.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if !strings.Contains(string(body), "buildWorkerPool(") {
			t.Errorf("%s does not call buildWorkerPool. 01_ARCHITECTURE.md §2: \"Single-process default. "+
				"`serve` does everything; `work`/`schedule` exist for administrators who have outgrown one box.\" "+
				"A role that enqueues work and consumes none is defect B-6", file)
		}
		if !strings.Contains(string(body), "riverClient.Start(") {
			t.Errorf("%s builds a worker pool and never starts it — an unstarted River client claims nothing, "+
				"which is indistinguishable from having no workers at all", file)
		}
	}
}
