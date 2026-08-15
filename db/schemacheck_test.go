package db

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// TestExpectedTablesDerivesTheAppSchema is the plain sanity check: the parse
// finds a schema-shaped number of tables, in both schemas the migrations
// touch, and app.esi_replica — the table whose absence started all this — is
// among them.
func TestExpectedTablesDerivesTheAppSchema(t *testing.T) {
	tables, err := ExpectedTables()
	require.NoError(t, err)

	bySchema := map[string]int{}
	found := map[string]bool{}
	for _, ref := range tables {
		bySchema[ref.Schema]++
		found[ref.String()] = true
	}

	require.Equal(t, 138, bySchema["app"],
		"the app schema's table count changed; if that is intended, update this number AND "+
			"expectedPlatformTables/expectedDomainTables in the integration tests, which must agree")
	require.NotZero(t, bySchema["sde"], "migrate up claims to manage `app/sde`; sde must be covered too")
	require.True(t, found["app.esi_replica"],
		"app.esi_replica is the table whose out-of-band DROP this check exists to catch")

	// The five declarative partitions are ordinary tables in
	// information_schema and must be expected, or a correct database would
	// look like it had spare tables and a drifted one would not be noticed.
	for _, partition := range []string{
		"app.wallet_journal_default", "app.wallet_transaction_default",
		"app.market_history_default", "app.killmail_default",
		"app.character_notification_default",
	} {
		require.True(t, found[partition], "%s is a real table and must be expected", partition)
	}
}

// TestExpectedTablesIsSortedAndUnique — the result is compared and printed,
// so a stable order is part of the contract.
func TestExpectedTablesIsSortedAndUnique(t *testing.T) {
	tables, err := ExpectedTables()
	require.NoError(t, err)

	seen := map[TableRef]bool{}
	for i, ref := range tables {
		require.False(t, seen[ref], "%s appears twice", ref)
		seen[ref] = true
		if i > 0 {
			previous := tables[i-1]
			require.True(t,
				previous.Schema < ref.Schema || (previous.Schema == ref.Schema && previous.Name < ref.Name),
				"tables must be sorted: %s came before %s", previous, ref)
		}
	}
}

// TestMigrationsHaveNoParseHazards guards the assumption schemacheck.go's
// regexes rest on. A `CREATE TABLE` inside a goose StatementBegin block, a
// function body or a string literal would be counted as a real table and
// this check would then demand a table that does not exist — turning the
// integrity check itself into the thing that reports false drift.
//
// Asserted rather than assumed, because the failure is silent in the
// dangerous direction: every future migration has to keep this true.
func TestMigrationsHaveNoParseHazards(t *testing.T) {
	names, err := listMigrationFiles(t)
	require.NoError(t, err)
	require.NotEmpty(t, names)

	for _, name := range names {
		raw := readMigration(t, name)
		require.Contains(t, raw, "-- +goose Up", "%s has no Up marker", name)

		up, err := gooseUpSection(raw, name)
		require.NoError(t, err)
		require.NotContains(t, up, "+goose StatementBegin",
			"%s wraps statements in a StatementBegin block; schemacheck.go's parse does not "+
				"understand function bodies and would have to be taught before this lands", name)
		require.NotRegexp(t, regexp.MustCompile(`(?i)CREATE\s+(?:GLOBAL\s+|LOCAL\s+)?TEMP`), up,
			"%s creates a temporary table; it would be expected permanently", name)
	}
}

// TestExpectedTablesReadsOnlyTheUpSection is the property the whole parse
// hinges on and the one that is easiest to break by accident: every
// migration's Down section drops what its Up created, so reading whole files
// would cancel the schema out to nothing.
//
// Exercised against a synthetic FS rather than the real migrations, so it
// fails for the stated reason instead of incidentally.
func TestExpectedTablesReadsOnlyTheUpSection(t *testing.T) {
	upOnly, err := tablesFromSource(t, `
-- +goose Up
CREATE TABLE app.kept (id int);
-- +goose Down
DROP TABLE app.kept;
`)
	require.NoError(t, err)
	require.Equal(t, []TableRef{{Schema: "app", Name: "kept"}}, upOnly,
		"the Down section's DROP must not cancel the Up section's CREATE")
}

// TestExpectedTablesHonoursALaterDrop — a migration that retires a table
// must remove it from the expectation, or the check would demand a table the
// schema is correct not to have.
func TestExpectedTablesHonoursALaterDrop(t *testing.T) {
	tables, err := tablesFromSource(t,
		"-- +goose Up\nCREATE TABLE app.retired (id int);\n-- +goose Down\nDROP TABLE app.retired;\n",
		"-- +goose Up\nDROP TABLE app.retired;\n-- +goose Down\nCREATE TABLE app.retired (id int);\n")
	require.NoError(t, err)
	require.Empty(t, tables, "a table dropped by a later migration must not be expected")
}

// TestExpectedTablesRecognisesTheRealCreateForms — the spellings the
// migrations actually use.
func TestExpectedTablesRecognisesTheRealCreateForms(t *testing.T) {
	tables, err := tablesFromSource(t, `
-- +goose Up
CREATE TABLE app.plain (id int);
CREATE UNLOGGED TABLE app.unlogged (id int);
CREATE TABLE IF NOT EXISTS app.guarded (id int);
CREATE TABLE app.part_default PARTITION OF app.plain DEFAULT;
CREATE INDEX ON app.plain (id);
-- +goose Down
DROP TABLE app.plain;
`)
	require.NoError(t, err)
	require.Equal(t, []TableRef{
		{Schema: "app", Name: "guarded"},
		{Schema: "app", Name: "part_default"},
		{Schema: "app", Name: "plain"},
		{Schema: "app", Name: "unlogged"},
	}, tables)
}

// TestGooseUpSectionRejectsAMigrationWithNoMarker — a file with no Up marker
// must be an error, never a silent skip. A skip would make this check pass by
// knowing about fewer tables, which is the exact failure mode it exists to
// prevent.
func TestGooseUpSectionRejectsAMigrationWithNoMarker(t *testing.T) {
	_, err := gooseUpSection("CREATE TABLE app.orphan (id int);", "00099_broken.sql")
	require.Error(t, err)
	require.Contains(t, err.Error(), "00099_broken.sql")
}

// TestFormatMissingDoesNotRecommendMigrateUp — the remediation has to be one
// that works. goose has the migration recorded as applied, so `migrate up`
// is a no-op, and pointing an operator at the command that already told them
// the schema was current is worse than saying nothing.
func TestFormatMissingDoesNotRecommendMigrateUp(t *testing.T) {
	message := FormatMissing([]TableRef{{Schema: "app", Name: "esi_replica"}})
	require.Contains(t, message, "app.esi_replica")
	require.Contains(t, message, "will NOT restore them")
	require.Contains(t, message, "db/migrations/")
}

// ── helpers ──────────────────────────────────────────────────────────────

func listMigrationFiles(t *testing.T) ([]string, error) {
	t.Helper()
	entries, err := Migrations.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, "migrations/"+entry.Name())
		}
	}
	return names, nil
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	raw, err := Migrations.ReadFile(name)
	require.NoError(t, err)
	return string(raw)
}

// tablesFromSource runs the REAL extraction over synthetic migration files,
// through the same expectedTablesFromFS the embedded path uses — so these
// tests pin the shipped parsing rules rather than a copy of them.
func tablesFromSource(t *testing.T, sources ...string) ([]TableRef, error) {
	t.Helper()
	files := fstest.MapFS{}
	for i, source := range sources {
		files[migrationName(i)] = &fstest.MapFile{Data: []byte(source)}
	}
	return expectedTablesFromFS(files)
}

func migrationName(i int) string {
	return "migrations/" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "_synthetic.sql"
}
