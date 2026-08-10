package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplySeeds runs every *.sql file directly under db/seed (embedded above
// as Seed), in sorted filename order, against pool. Each file is applied
// whole as one Exec call (pgx's simple-query protocol accepts a
// multi-statement string), inside its own transaction so a failure in one
// file doesn't affect files already applied.
//
// Discovered gap, Phase 10: db/seed/permissions.sql and db/seed/roles.sql
// existed since Phase 1a with nothing anywhere in cmd/hangar ever applying
// them — harmless while nothing referenced app.permission/app.role rows,
// but load-bearing from Phase 10 on: app.role_grant.permission has a
// foreign key to app.permission, so a fresh installation's first RBAC
// grant would fail against an empty table. This function, plus its call
// from runMigrateUp (cmd/hangar/migrate.go), closes that gap rather than
// leaving seeding as a manual, undocumented step.
//
// Every seed file already in this tree is idempotent by construction
// (`ON CONFLICT ... DO UPDATE`/`DO NOTHING` — see each file's own INSERT),
// so re-running ApplySeeds on every `hangar migrate up` is safe and is the
// same idempotent-on-every-run philosophy Goose's own migrations follow.
func ApplySeeds(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(Seed, "seed")
	if err != nil {
		return fmt.Errorf("db: reading embedded seed directory: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue // .gitkeep and anything else non-SQL
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		contents, err := fs.ReadFile(Seed, "seed/"+name)
		if err != nil {
			return fmt.Errorf("db: reading seed file %s: %w", name, err)
		}
		if len(contents) == 0 {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("db: beginning transaction for seed file %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(contents)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: applying seed file %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("db: committing seed file %s: %w", name, err)
		}
	}
	return nil
}
