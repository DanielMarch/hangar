package db

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// destructiveDML matches DELETE/TRUNCATE statements. Word-boundaried and
// case-insensitive so it also catches "Delete From" or "TRUNCATE TABLE".
var destructiveDML = regexp.MustCompile(`(?i)\b(delete\s+from|truncate)\b`)

// TestNoDestructiveDMLInMigrations enforces 01_ARCHITECTURE.md §17 invariant
// 9 and 02_DATABASE_SCHEMA.md §3.6: destructive DML (DELETE, TRUNCATE) is
// banned in Goose migrations. Retention is by partition detachment; removal
// of synced rows is by soft delete (`deleted_at`). `DROP TABLE` / `DROP
// SCHEMA` in a migration's own `-- +goose Down` section are DDL, not DML,
// and are exempt — a migration legitimately undoing what its own `up`
// created is not the defect this check guards against.
func TestNoDestructiveDMLInMigrations(t *testing.T) {
	err := fs.WalkDir(Migrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		content, err := fs.ReadFile(Migrations, path)
		if err != nil {
			return err
		}
		if loc := destructiveDML.FindIndex(content); loc != nil {
			t.Errorf("%s: destructive DML found: %q", path, content[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
