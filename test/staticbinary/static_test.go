//go:build staticbinary

// Package staticbinary holds the Phase 0 exit criterion
// TestStaticBinaryHasNoDynamicLinks. It cross-compiles the release matrix
// (SRS §9.2: linux/amd64, linux/arm64, windows/amd64) with CGO_ENABLED=0 and
// inspects the resulting binaries directly — a single accidental cgo
// dependency silently produces a dynamically linked binary that boots fine
// on a developer's glibc host and then fails to start on the distroless
// runtime image, so this has to be caught here, not at release.
//
// This test shells out to `go build`, so it is excluded from the default
// `go test ./...` run (see Makefile's check-static-binary target, which runs
// `go test -tags=staticbinary ./test/staticbinary/...`).
package staticbinary

import (
	"debug/elf"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var targets = []struct {
	goos, goarch string
}{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"windows", "amd64"},
}

func TestStaticBinaryHasNoDynamicLinks(t *testing.T) {
	repoRoot := findRepoRoot(t)
	dir := t.TempDir()

	for _, target := range targets {
		target := target
		t.Run(target.goos+"_"+target.goarch, func(t *testing.T) {
			ext := ""
			if target.goos == "windows" {
				ext = ".exe"
			}
			out := filepath.Join(dir, "hangar-"+target.goos+"-"+target.goarch+ext)

			cmd := exec.Command("go", "build", "-trimpath", "-o", out, "./cmd/hangar")
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(),
				"CGO_ENABLED=0",
				"GOOS="+target.goos,
				"GOARCH="+target.goarch,
			)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build (%s/%s) failed: %v\n%s", target.goos, target.goarch, err, b)
			}

			switch target.goos {
			case "linux":
				assertStaticELF(t, out)
			case "windows":
				assertStaticPE(t, out)
			}
		})
	}
}

// assertStaticELF fails if the ELF binary has a PT_INTERP segment (an
// ELF interpreter, e.g. ld-linux.so) or any DT_NEEDED dynamic library
// dependency — both are proof of dynamic linking.
func assertStaticELF(t *testing.T, path string) {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("opening ELF binary: %v", err)
	}
	defer func() { _ = f.Close() }()

	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			t.Errorf("%s has a PT_INTERP segment — it is dynamically linked", path)
		}
	}

	needed, err := f.DynString(elf.DT_NEEDED)
	// A statically linked binary typically has no dynamic section at all,
	// which elf.DynString reports as an error; that is the success case.
	if err == nil && len(needed) > 0 {
		t.Errorf("%s has dynamic dependencies: %v", path, needed)
	}
}

// assertStaticPE fails if the PE binary imports a C runtime DLL. A CGO-free
// Windows Go binary imports only kernel32.dll (raw syscalls), never msvcrt
// or a vcruntime DLL.
func assertStaticPE(t *testing.T, path string) {
	t.Helper()
	f, err := pe.Open(path)
	if err != nil {
		t.Fatalf("opening PE binary: %v", err)
	}
	defer func() { _ = f.Close() }()

	libs, err := f.ImportedLibraries()
	if err != nil {
		t.Fatalf("reading imported libraries: %v", err)
	}
	for _, lib := range libs {
		if lib != "kernel32.dll" && lib != "KERNEL32.dll" {
			t.Errorf("%s imports %s — expected only kernel32.dll for a CGO_ENABLED=0 build", path, lib)
		}
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
			t.Fatal("could not find repository root (no go.mod found)")
		}
		dir = parent
	}
}
