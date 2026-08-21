package db

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// ── PHASE 23 (N-6) ───────────────────────────────────────────────────────
//
// These test the PARSE. db/schemadrift_integration_test.go tests the
// comparison against a real PostgreSQL 18, which is the half that can only
// be proved by dropping something.

// TestExpectedColumnsCoversEveryExpectedTable is the structural claim: a
// table with no parsed columns means the CREATE TABLE body was not read,
// and a check that knows about fewer objects passes for the wrong reason —
// the exact failure mode schemacheck.go's header names.
func TestExpectedColumnsCoversEveryExpectedTable(t *testing.T) {
	tables, err := ExpectedTables()
	require.NoError(t, err)
	columns, err := ExpectedColumns()
	require.NoError(t, err)

	byTable := map[TableRef]int{}
	for _, ref := range columns {
		byTable[ref.table()]++
	}

	// The five `PARTITION OF ... DEFAULT` partitions declare no columns of
	// their own — they inherit their parent's — so they are the only
	// tables legitimately absent from the column register.
	var uncovered []string
	for _, table := range tables {
		if byTable[table] == 0 {
			uncovered = append(uncovered, table.String())
		}
	}
	for _, name := range uncovered {
		require.True(t, strings.HasSuffix(name, "_default"),
			"%s has no parsed columns and is not a PARTITION OF ... DEFAULT — its CREATE TABLE body was not read, "+
				"so the schema check silently knows about fewer objects than the migrations create", name)
	}
	require.Len(t, uncovered, 5, "exactly the five declared partitions inherit their columns")
}

// TestExpectedColumnsFindsNoConstraintClauses guards the one thing a naive
// comma split gets wrong: `PRIMARY KEY (a, b)` and `UNIQUE (x)` sit in the
// same list as the columns and are not columns.
func TestExpectedColumnsFindsNoConstraintClauses(t *testing.T) {
	columns, err := ExpectedColumns()
	require.NoError(t, err)
	for _, ref := range columns {
		for _, keyword := range tableConstraintPrefixes {
			require.NotEqual(t, strings.ToLower(keyword), ref.Name,
				"%s was parsed as a column and is a table-level constraint clause", ref)
		}
	}
}

// TestExpectedIndexesHaveNoExpressionKeys is the assumption firstIdentifier
// documents, asserted rather than assumed. An expression key would yield a
// function name where a column name belongs, match nothing Postgres
// reports, and produce a permanent false "missing index" — a check that
// cries wolf is one an operator learns to ignore.
//
// If this fails, the schema has gained its first expression index and
// missingIndexes needs to read pg_get_indexdef for it rather than pg_index's
// column array.
func TestExpectedIndexesHaveNoExpressionKeys(t *testing.T) {
	indexes, err := ExpectedIndexes()
	require.NoError(t, err)
	for _, ref := range indexes {
		require.NotEmpty(t, ref.Columns, "%s parsed to no key columns at all", ref)
		for _, column := range ref.Columns {
			require.NotEmpty(t, column)
		}
	}
}

// TestNoTwoIndexesShareASignature is IndexRef's stated blind spot, held
// closed by measurement rather than by hope. Two indexes differing only in
// their WHERE predicates would collapse to one signature, and dropping one
// of them would pass.
func TestNoTwoIndexesShareASignature(t *testing.T) {
	indexes, err := ExpectedIndexes()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, ref := range indexes {
		require.False(t, seen[ref.Signature()],
			"two declared indexes share the signature %q — one of them could be dropped without this check "+
				"noticing. Give them different key columns, or teach IndexRef to carry the predicate", ref.Signature())
		seen[ref.Signature()] = true
	}
}

// TestPartialIndexesAreRecognised pins the one field that is derived from
// text after the column list rather than from the list itself. A partial
// index recorded as non-partial matches a DIFFERENT index in Postgres, which
// is a false pass rather than a false failure — the dangerous direction.
func TestPartialIndexesAreRecognised(t *testing.T) {
	indexes, err := ExpectedIndexes()
	require.NoError(t, err)

	var partial, total int
	for _, ref := range indexes {
		total++
		if ref.Partial {
			partial++
		}
	}
	require.Positive(t, partial, "the schema declares partial indexes; none was recognised as one")
	require.Less(t, partial, total, "not every index is partial — the WHERE detection is matching something else")

	// A named one, so this asserts a fact rather than a proportion.
	require.Contains(t, signatures(indexes), "app.alert_delivery|btree|state,next_attempt_at|true",
		"app.alert_delivery's `(state, next_attempt_at) WHERE state = 'pending'` must be recorded as partial")
	require.Contains(t, signatures(indexes), "app.alert_delivery|btree|event_id|false")
}

// TestIndexMethodIsRead pins USING <method>. A brin index recorded as btree
// matches nothing and reports a permanent false missing.
func TestIndexMethodIsRead(t *testing.T) {
	indexes, err := ExpectedIndexes()
	require.NoError(t, err)
	require.Contains(t, signatures(indexes), "app.killmail|brin|killmail_time|false",
		"app.killmail's `USING brin (killmail_time)` must record its access method")
}

// TestObjectsOfARetiredTableAreNotExpected — a later migration dropping a
// table takes its columns and indexes with it, and expecting them would make
// the check permanently, unfixably red.
func TestObjectsOfARetiredTableAreNotExpected(t *testing.T) {
	files := fstest.MapFS{
		"migrations/00001_create.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
CREATE TABLE app.temporary (id bigint PRIMARY KEY, note text);
CREATE INDEX ON app.temporary (note);
CREATE TABLE app.permanent (id bigint PRIMARY KEY, kept text);
CREATE INDEX ON app.permanent (kept);
-- +goose Down
DROP TABLE app.temporary;
`)},
		"migrations/00002_retire.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
DROP TABLE app.temporary;
-- +goose Down
SELECT 1;
`)},
	}

	columns, err := expectedColumnsFromFS(files)
	require.NoError(t, err)
	require.Equal(t, []string{"app.permanent.id", "app.permanent.kept"}, renderColumns(columns))

	indexes, err := expectedIndexesFromFS(files)
	require.NoError(t, err)
	require.Len(t, indexes, 1)
	require.Equal(t, "app.permanent", indexes[0].Schema+"."+indexes[0].Table)
}

// TestADroppedColumnIsNotExpected — the ALTER TABLE ... DROP COLUMN path,
// which is what a migration that retires a column actually writes.
func TestADroppedColumnIsNotExpected(t *testing.T) {
	files := fstest.MapFS{
		"migrations/00001_create.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
CREATE TABLE app.thing (id bigint PRIMARY KEY, kept text, doomed text);
-- +goose Down
DROP TABLE app.thing;
`)},
		"migrations/00002_alter.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
ALTER TABLE app.thing DROP COLUMN doomed;
ALTER TABLE app.thing ADD COLUMN added jsonb;
-- +goose Down
ALTER TABLE app.thing DROP COLUMN added;
ALTER TABLE app.thing ADD COLUMN doomed text;
`)},
	}

	columns, err := expectedColumnsFromFS(files)
	require.NoError(t, err)
	require.Equal(t, []string{"app.thing.added", "app.thing.id", "app.thing.kept"}, renderColumns(columns),
		"a column dropped by a later migration's Up must not be expected, and one added by it must be — "+
			"and the Down section, which says the exact opposite, must not be read at all")
}

// TestNestedParenthesesAndLiteralsDoNotBreakTheColumnParse covers what a
// naive split on "," gets wrong inside a real CREATE TABLE: a type with a
// precision, a CHECK with a comma-separated IN list, and a DEFAULT string
// literal containing a comma and a parenthesis.
func TestNestedParenthesesAndLiteralsDoNotBreakTheColumnParse(t *testing.T) {
	files := fstest.MapFS{
		"migrations/00001_create.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
CREATE TABLE app.tricky (
    id        uuid          PRIMARY KEY DEFAULT uuidv7(),
    amount    numeric(28, 2) NOT NULL DEFAULT 0,
    state     text          NOT NULL CHECK (state IN ('a', 'b', 'c')),
    label     text          NOT NULL DEFAULT 'one, two (three)',
    UNIQUE (amount, state),
    CONSTRAINT tricky_label_len CHECK (length(label) < 40)
);
-- +goose Down
DROP TABLE app.tricky;
`)},
	}

	columns, err := expectedColumnsFromFS(files)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"app.tricky.amount", "app.tricky.id", "app.tricky.label", "app.tricky.state"},
		renderColumns(columns))
}

// TestPartitionOfDeclaresNoColumns — `CREATE TABLE x PARTITION OF y DEFAULT`
// has no column list, and reading one would either fail the parse or, worse,
// consume the next statement's parentheses.
func TestPartitionOfDeclaresNoColumns(t *testing.T) {
	files := fstest.MapFS{
		"migrations/00001_create.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
CREATE TABLE app.parent (id bigint, at timestamptz NOT NULL) PARTITION BY RANGE (at);
CREATE TABLE app.parent_default PARTITION OF app.parent DEFAULT;
CREATE TABLE app.after (only_column text);
-- +goose Down
DROP TABLE app.parent;
`)},
	}

	columns, err := expectedColumnsFromFS(files)
	require.NoError(t, err)
	require.Equal(t, []string{"app.after.only_column", "app.parent.at", "app.parent.id"}, renderColumns(columns))
}

// ── helpers ──────────────────────────────────────────────────────────────

func renderColumns(columns []ColumnRef) []string {
	out := make([]string, 0, len(columns))
	for _, ref := range columns {
		out = append(out, ref.String())
	}
	return out
}

func signatures(indexes []IndexRef) []string {
	out := make([]string, 0, len(indexes))
	for _, ref := range indexes {
		out = append(out, ref.Signature())
	}
	return out
}

// TestALaterMigrationRetiresAnIndexByItsGeneratedName is the mechanism that
// took two attempts to get right, so it gets its own test rather than being
// left to the integration baseline.
//
// Migration 00045 replaces four indexes with covering ones, and it retires
// the originals with `DROP INDEX app.wallet_journal_owner_kind_owner_id_
// date_idx` — the name POSTGRES generated, because the original
// `CREATE INDEX ON <table> (...)` never gave one. A parse that does not
// reconstruct that name goes on expecting four indexes the schema replaced,
// and every correct database reports permanent drift.
func TestALaterMigrationRetiresAnIndexByItsGeneratedName(t *testing.T) {
	files := fstest.MapFS{
		"migrations/00001_create.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
CREATE TABLE app.ledger (owner_kind text, owner_id bigint, at timestamptz, id bigint);
CREATE INDEX ON app.ledger (owner_kind, owner_id, at DESC);
-- +goose Down
DROP TABLE app.ledger;
`)},
		"migrations/00002_covering.sql": &fstest.MapFile{Data: []byte(`
-- +goose Up
DROP INDEX IF EXISTS app.ledger_owner_kind_owner_id_at_idx;
CREATE INDEX ledger_keyset_idx ON app.ledger (owner_kind, owner_id, at DESC, id DESC);
-- +goose Down
SELECT 1;
`)},
	}

	indexes, err := expectedIndexesFromFS(files)
	require.NoError(t, err)
	require.Equal(t, []string{"app.ledger|btree|owner_kind,owner_id,at,id|false"}, signatures(indexes),
		"the replaced index must be retired and only the covering one expected")
}

// TestTheFourReplacedIndexesAreRetired is the same claim against the real
// migrations, named so a failure says which four.
func TestTheFourReplacedIndexesAreRetired(t *testing.T) {
	indexes, err := ExpectedIndexes()
	require.NoError(t, err)
	live := signatures(indexes)

	for _, retired := range []string{
		"app.wallet_journal|btree|owner_kind,owner_id,date|false",
		"app.wallet_transaction|btree|owner_kind,owner_id,date|false",
		"app.mail_header|btree|character_id,sent_at|false",
		"app.character_notification|btree|character_id,sent_at|false",
	} {
		require.NotContains(t, live, retired,
			"migration 00045 dropped this index and replaced it with a covering one; expecting it makes every "+
				"correct database report drift forever")
	}
	for _, covering := range []string{
		"app.wallet_journal|btree|owner_kind,owner_id,division,date,journal_id|false",
		"app.wallet_transaction|btree|owner_kind,owner_id,division,date,transaction_id|false",
		"app.mail_header|btree|character_id,sent_at,mail_id|false",
		"app.character_notification|btree|character_id,sent_at,notification_id|false",
	} {
		require.Contains(t, live, covering, "the covering index that replaced it must be expected")
	}
}
