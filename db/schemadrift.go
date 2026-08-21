package db

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemadrift.go is the database half of PHASE 23's N-6: it compares what
// the migrations declare (schemacheck.go for tables, schemacheck_objects.go
// for columns and indexes) against what the database in front of it holds.
//
// ── WHY MISSING COLUMNS AND INDEXES ARE ONLY REPORTED FOR TABLES THAT
//    EXIST ───────────────────────────────────────────────────────────────
//
// A single dropped table would otherwise report itself three times: once as
// a table, then once per column, then once per index. An operator reading
// "app.killmail is absent, and also its 24 columns and 4 indexes are
// absent" learns nothing from the last twenty-eight lines, and the one line
// that can be acted on is buried in them. The table report is the actionable
// one, and the others are consequences of it.

// Drift is everything the migrations declare that the database does not
// hold. The zero value means the schema matches.
type Drift struct {
	Tables  []TableRef
	Columns []ColumnRef
	Indexes []IndexRef
}

// Empty reports whether the schema matches what the migrations describe.
func (d Drift) Empty() bool { return len(d.Tables)+len(d.Columns)+len(d.Indexes) == 0 }

// Count is the total number of absent objects.
func (d Drift) Count() int { return len(d.Tables) + len(d.Columns) + len(d.Indexes) }

// MissingObjects reports every table, column and index the migrations create
// that the database does not currently have.
//
// It is the check `hangar migrate up` and every serving process run.
// MissingTables remains the table-only primitive — it is what this builds
// on, and what decides which tables' columns and indexes are worth asking
// about at all.
func MissingObjects(ctx context.Context, pool *pgxpool.Pool) (Drift, error) {
	var drift Drift

	missingTables, err := MissingTables(ctx, pool)
	if err != nil {
		return drift, err
	}
	drift.Tables = missingTables

	absent := make(map[TableRef]bool, len(missingTables))
	for _, ref := range missingTables {
		absent[ref] = true
	}

	drift.Columns, err = missingColumns(ctx, pool, absent)
	if err != nil {
		return drift, err
	}
	drift.Indexes, err = missingIndexes(ctx, pool, absent)
	if err != nil {
		return drift, err
	}
	return drift, nil
}

func missingColumns(ctx context.Context, pool *pgxpool.Pool, absentTables map[TableRef]bool) ([]ColumnRef, error) {
	expected, err := ExpectedColumns()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, `
		SELECT table_schema, table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = ANY($1::text[])`, columnSchemas(expected))
	if err != nil {
		return nil, fmt.Errorf("db: listing existing columns: %w", err)
	}
	defer rows.Close()

	present := map[ColumnRef]bool{}
	for rows.Next() {
		var ref ColumnRef
		if err := rows.Scan(&ref.Schema, &ref.Table, &ref.Name); err != nil {
			return nil, fmt.Errorf("db: scanning existing columns: %w", err)
		}
		present[ref] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: reading existing columns: %w", err)
	}

	var missing []ColumnRef
	for _, ref := range expected {
		if absentTables[ref.table()] {
			continue // its table is already reported; see the file header
		}
		if !present[ref] {
			missing = append(missing, ref)
		}
	}
	return missing, nil
}

// missingIndexes compares by SIGNATURE, not by name — see IndexRef.
//
// The key columns come from pg_index rather than from pg_indexes.indexdef,
// so the comparison is against Postgres's own structural answer rather than
// against its rendering of one. indkey is 1-based over the index's own
// attributes and carries INCLUDE columns after the key ones, which is why
// the slice stops at indnkeyatts: an INCLUDE column is not part of what the
// migration declared the index to be ON.
//
// An expression key has attnum 0 and matches no pg_attribute row, so it
// simply does not appear in the array. There are none in this schema
// (TestExpectedIndexesHaveNoExpressionKeys), and if one is ever added this
// query will report its index as missing rather than silently accepting a
// wrong match — the safe direction for a check like this.
func missingIndexes(ctx context.Context, pool *pgxpool.Pool, absentTables map[TableRef]bool) ([]IndexRef, error) {
	expected, err := ExpectedIndexes()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, `
		SELECT n.nspname,
		       c.relname,
		       am.amname,
		       i.indpred IS NOT NULL,
		       coalesce((
		           SELECT array_agg(a.attname ORDER BY k.ord)
		             FROM unnest(string_to_array(i.indkey::text, ' ')::int2[])
		                  WITH ORDINALITY AS k(attnum, ord)
		             JOIN pg_attribute a
		               ON a.attrelid = c.oid AND a.attnum = k.attnum
		            WHERE k.ord <= i.indnkeyatts
		       ), ARRAY[]::name[])
		  FROM pg_index i
		  JOIN pg_class ic       ON ic.oid = i.indexrelid
		  JOIN pg_class c        ON c.oid = i.indrelid
		  JOIN pg_namespace n    ON n.oid = c.relnamespace
		  JOIN pg_am am          ON am.oid = ic.relam
		 WHERE n.nspname = ANY($1::text[])`, indexSchemas(expected))
	if err != nil {
		return nil, fmt.Errorf("db: listing existing indexes: %w", err)
	}
	defer rows.Close()

	present := map[string]bool{}
	for rows.Next() {
		var ref IndexRef
		if err := rows.Scan(&ref.Schema, &ref.Table, &ref.Method, &ref.Partial, &ref.Columns); err != nil {
			return nil, fmt.Errorf("db: scanning existing indexes: %w", err)
		}
		present[ref.Signature()] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: reading existing indexes: %w", err)
	}

	var missing []IndexRef
	for _, ref := range expected {
		if absentTables[ref.table()] {
			continue
		}
		if !present[ref.Signature()] {
			missing = append(missing, ref)
		}
	}
	return missing, nil
}

func columnSchemas(expected []ColumnRef) []string {
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

func indexSchemas(expected []IndexRef) []string {
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

// FormatDrift renders a Drift for an operator, with the remediation that
// actually works.
//
// `migrate up` is deliberately NOT the advice, for the reason FormatMissing
// gives: goose has recorded those migrations as applied, so re-running them
// does nothing, and telling an operator to run the command that already lies
// to them is worse than saying nothing.
//
// Each kind is listed separately because each has a different consequence.
// A missing TABLE breaks the first query that touches it. A missing COLUMN
// breaks writes and silently changes reads. A missing INDEX breaks nothing
// at all until a query that was instant becomes a sequential scan — the one
// an operator is least likely to attribute to schema drift, and the reason
// this check was extended.
func FormatDrift(d Drift) string {
	var parts []string
	if len(d.Tables) > 0 {
		names := make([]string, 0, len(d.Tables))
		for _, ref := range d.Tables {
			names = append(names, ref.String())
		}
		parts = append(parts, fmt.Sprintf("%d table(s): %s", len(names), strings.Join(names, ", ")))
	}
	if len(d.Columns) > 0 {
		names := make([]string, 0, len(d.Columns))
		for _, ref := range d.Columns {
			names = append(names, ref.String())
		}
		parts = append(parts, fmt.Sprintf("%d column(s): %s", len(names), strings.Join(names, ", ")))
	}
	if len(d.Indexes) > 0 {
		names := make([]string, 0, len(d.Indexes))
		for _, ref := range d.Indexes {
			names = append(names, ref.String())
		}
		parts = append(parts, fmt.Sprintf("%d index(es): %s", len(names), strings.Join(names, ", ")))
	}

	return fmt.Sprintf(
		"%d object(s) the migrations create are absent — %s. "+
			"`hangar migrate up` will NOT restore them: goose records those migrations as applied, "+
			"so re-running is a no-op. Restore from a backup, or re-create the objects from the "+
			"migration that declares them (db/migrations/) and verify with this check.",
		d.Count(), strings.Join(parts, "; "))
}
