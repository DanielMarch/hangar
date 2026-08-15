package db

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemacheck.go answers a question `hangar migrate up` was asserting rather
// than checking: are the objects the migrations created still THERE?
//
// ── WHY THIS EXISTS ──────────────────────────────────────────────────────
// goose records which migration FILES have run. It does not, and cannot,
// know whether the tables those files created still exist — so
// `migrate up` printed "app/sde schema current" on the strength of a row in
// `goose_db_version`, which is a statement about history, not about the
// database in front of it. The two come apart the moment anything drops an
// object out of band, and goose will never put it back: the migration is
// recorded as applied, so re-running is a no-op.
//
// That is not hypothetical. Phase 20.9 found the live development database
// missing exactly one table, app.esi_replica, because a documented
// housekeeping step ("delete app.esi_replica after integration runs") had
// been carried out as a DROP TABLE rather than a row delete. `migrate up`
// reported the schema current every time it ran afterwards.
//
// ── WHY THE EXPECTED SET IS DERIVED, NOT LISTED ──────────────────────────
// The obvious implementation is a hand-maintained list of table names. This
// repository has been bitten repeatedly by exactly that shape — a number or
// a list stated somewhere and never re-derived (defect B6, and again B55 and
// B57 in the /api/v2 shim's blocker reasons). So the expected set is parsed
// out of the embedded migrations themselves: whatever they CREATE, minus
// whatever a later migration's Up section DROPs, is what the database must
// hold. Adding a migration updates the check automatically, and a check that
// cannot drift is worth more than one that is easy to read.
//
// Cross-checked against the independent hand-maintained list in
// db/migrations_domain_integration_test.go: both say 138 app tables. Two
// numbers derived by different means agreeing is the evidence that this
// parse is right; TestExpectedTablesMatchesTheHandMaintainedLists keeps them
// agreeing.

// TableRef is one schema-qualified table the migrations create.
type TableRef struct {
	Schema string
	Name   string
}

func (t TableRef) String() string { return t.Schema + "." + t.Name }

// Only our own migrations are parsed, and they are consistently formatted:
// no `CREATE TABLE` appears inside a function body, a string literal or a
// goose StatementBegin block (asserted by TestMigrationsHaveNoParseHazards).
var (
	createTableRE = regexp.MustCompile(
		`(?i)\bCREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z_0-9]*)\.([a-z_][a-z_0-9]*)`)
	dropTableRE = regexp.MustCompile(
		`(?i)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z_0-9]*)\.([a-z_][a-z_0-9]*)`)
)

// ExpectedTables is every table the embedded migrations leave behind, in
// stable order.
//
// Only the `-- +goose Up` half of each file is read. That is load-bearing
// rather than tidy: every migration's Down section drops what its Up
// created, so parsing whole files would cancel the entire schema out to
// nothing.
func ExpectedTables() ([]TableRef, error) { return expectedTablesFromFS(Migrations) }

// expectedTablesFromFS is ExpectedTables over an arbitrary FS, so the parsing
// rules can be tested against synthetic migrations. ExpectedTables itself
// takes no parameter on purpose: production must read the EMBEDDED
// migrations, and a caller that could pass a different FS could pass one that
// makes the check pass by knowing about fewer tables.
func expectedTablesFromFS(files fs.FS) ([]TableRef, error) {
	entries, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("db: listing migrations: %w", err)
	}
	sort.Strings(entries)

	created := map[TableRef]bool{}
	var order []TableRef
	for _, name := range entries {
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("db: reading %s: %w", name, err)
		}
		up, err := gooseUpSection(string(raw), name)
		if err != nil {
			return nil, err
		}

		for _, m := range createTableRE.FindAllStringSubmatch(up, -1) {
			ref := TableRef{Schema: m[1], Name: m[2]}
			if !created[ref] {
				created[ref] = true
				order = append(order, ref)
			}
		}
		// A later migration may legitimately retire a table. Applied after
		// this file's creates so a drop-then-recreate within one migration
		// still ends up present.
		for _, m := range dropTableRE.FindAllStringSubmatch(up, -1) {
			delete(created, TableRef{Schema: m[1], Name: m[2]})
		}
	}

	out := make([]TableRef, 0, len(created))
	for _, ref := range order {
		if created[ref] {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// gooseUpSection returns the text between `-- +goose Up` and `-- +goose Down`.
// A file with no Up marker is a malformed migration and is reported rather
// than silently contributing nothing — a silent skip here would make the
// check pass by knowing about fewer tables, which is the failure mode this
// whole file exists to avoid.
func gooseUpSection(content, name string) (string, error) {
	_, after, found := strings.Cut(content, "-- +goose Up")
	if !found {
		return "", fmt.Errorf("db: %s has no `-- +goose Up` marker", name)
	}
	before, _, _ := strings.Cut(after, "-- +goose Down")
	return before, nil
}

// MissingTables reports which tables the migrations create that the database
// does not currently have, in stable order. An empty result means the schema
// matches what the migrations describe.
func MissingTables(ctx context.Context, pool *pgxpool.Pool) ([]TableRef, error) {
	expected, err := ExpectedTables()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, `
		SELECT table_schema, table_name
		  FROM information_schema.tables
		 WHERE table_schema = ANY($1::text[])`, expectedSchemas(expected))
	if err != nil {
		return nil, fmt.Errorf("db: listing existing tables: %w", err)
	}
	defer rows.Close()

	present := map[TableRef]bool{}
	for rows.Next() {
		var ref TableRef
		if err := rows.Scan(&ref.Schema, &ref.Name); err != nil {
			return nil, fmt.Errorf("db: scanning existing tables: %w", err)
		}
		present[ref] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: reading existing tables: %w", err)
	}

	var missing []TableRef
	for _, ref := range expected {
		if !present[ref] {
			missing = append(missing, ref)
		}
	}
	return missing, nil
}

func expectedSchemas(expected []TableRef) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range expected {
		if !seen[ref.Schema] {
			seen[ref.Schema] = true
			out = append(out, ref.Schema)
		}
	}
	sort.Strings(out)
	return out
}

// FormatMissing renders a missing-table list for an operator, with the
// remediation that actually works. `migrate up` is deliberately NOT the
// advice: goose has recorded the migration as applied, so re-running it does
// nothing, and telling an operator to run the command that already lies to
// them is worse than saying nothing.
func FormatMissing(missing []TableRef) string {
	names := make([]string, 0, len(missing))
	for _, ref := range missing {
		names = append(names, ref.String())
	}
	return fmt.Sprintf(
		"%d table(s) the migrations create are absent: %s. "+
			"`hangar migrate up` will NOT restore them — goose records those migrations as applied, "+
			"so re-running is a no-op. Restore from a backup, or re-create the objects from the "+
			"migration that declares them (db/migrations/) and verify with this check.",
		len(missing), strings.Join(names, ", "))
}
