package domain_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hangar-project/hangar/internal/domain"
)

// TestNoFloatOnMoneyPaths is the Principle 9 backstop
// (01_ARCHITECTURE.md §17 invariant 1, 02_DATABASE_SCHEMA.md §3.1): no
// struct field reachable from internal/domain or internal/api/dto whose name
// matches the money vocabulary may be typed float32/float64. It walks
// declared struct types via go/ast rather than the runtime `reflect`
// package, because reflect can only inspect types that have already been
// instantiated somewhere — an AST walk catches every declared field whether
// or not any code happens to construct one, which is the point of a build
// gate. `make check-money` runs this test whenever internal/domain is
// non-empty; it starts enforcing itself the moment Phase 1b adds real money
// fields, with no Makefile edit required.
func TestNoFloatOnMoneyPaths(t *testing.T) {
	root := moduleRoot(t)

	var violations []string
	for _, rel := range []string{"internal/domain", "internal/api/dto"} {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(dir); err != nil {
			continue // internal/api/dto does not exist until a later phase
		}
		violations = append(violations, scanDirForFloatMoneyFields(t, dir)...)
	}

	for _, v := range violations {
		t.Error(v)
	}
}

func scanDirForFloatMoneyFields(t *testing.T, dir string) []string {
	t.Helper()
	var out []string

	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" || isTestFile(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if !isFloatType(field.Type) {
					continue
				}
				for _, name := range field.Names {
					if isMoneyFieldName(name.Name) {
						pos := fset.Position(field.Pos())
						out = append(out, pos.String()+": field "+name.Name+
							" is float but its name matches the money vocabulary (Principle 9) — use domain.Money")
					}
				}
			}
			return true
		})
	}
	return out
}

func isTestFile(name string) bool {
	return len(name) > len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go"
}

func isFloatType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "float32" || ident.Name == "float64"
}

func isMoneyFieldName(name string) bool { return domain.IsMoneyFieldName(name) }

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file location")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root (no go.mod found)")
		}
		dir = parent
	}
}
