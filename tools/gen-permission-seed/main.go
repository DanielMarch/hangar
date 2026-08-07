// Command gen-permission-seed renders internal/domain.Permissions into
// db/seed/permissions.sql. It is the "seeded from Go" half of Principle 14's
// app.permission exception: the Go slice is the source of truth, this tool
// is the one-way generator, and TestPermissionSeedMatchesGoSet
// (internal/domain/vocabulary_test.go) fails CI if someone hand-edits the
// SQL file out of step with it.
//
// Run via `go generate ./internal/domain/...` or directly:
//
//	go run ./tools/gen-permission-seed
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hangar-project/hangar/internal/domain"
)

func main() {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("gen-permission-seed: could not determine source location")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // tools/gen-permission-seed -> tools -> repo root
	out := filepath.Join(root, "db", "seed", "permissions.sql")

	var buf bytes.Buffer
	buf.WriteString("-- GENERATED FILE — do not edit by hand.\n")
	buf.WriteString("-- Source of truth: internal/domain/vocabulary.go (domain.Permissions).\n")
	buf.WriteString("-- Regenerate: go generate ./internal/domain/...\n")
	buf.WriteString("-- TestPermissionSeedMatchesGoSet fails CI if this file and the Go slice diverge.\n\n")
	buf.WriteString("INSERT INTO app.permission (permission, description, category) VALUES\n")

	for i, p := range domain.Permissions {
		sep := ","
		if i == len(domain.Permissions)-1 {
			sep = ""
		}
		fmt.Fprintf(&buf, "    (%s, %s, %s)%s\n", sqlLit(p.Name), sqlLit(p.Description), sqlLit(p.Category), sep)
	}

	buf.WriteString("ON CONFLICT (permission) DO UPDATE\n")
	buf.WriteString("   SET description = EXCLUDED.description,\n")
	buf.WriteString("       category    = EXCLUDED.category;\n")

	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		log.Fatalf("gen-permission-seed: writing %s: %v", out, err)
	}
}

func sqlLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
