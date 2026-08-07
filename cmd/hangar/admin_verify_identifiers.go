package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// newAdminVerifyIdentifierTypesCmd is Principle 13's enforcement point
// (01_ARCHITECTURE.md §17 invariant 2, 02_DATABASE_SCHEMA.md §3.2): every
// identifier column's Postgres type must match its OpenAPI-declared type,
// and coercion between bigint and uuid is prohibited in both directions.
//
// Full spec-driven verification (comparing against the *exact* declared
// width — int32 vs int64 — of every field) requires the ingested route
// catalogue's app.esi_route.identifier_types column, which does not exist
// until Phase 2. Until then this command runs the check Phase 1b *can*
// make offline and unconditionally: every "%_id" column in the app schema
// against internal/domain.KnownUUIDIdentifiers, the same registry
// `hangar admin verify-identifier-types` grows into consulting the live
// spec through. A column in that registry must be `uuid`; every other
// identifier column must be neither `uuid` nor `text` — the two shapes
// Principle 13 explicitly bans a coerced or "flexible" identifier from
// taking. --spec is accepted (and `make check-identifiers` already passes
// it whenever SPEC_SNAPSHOT exists) so the flag's shape is stable across
// the Phase 2 upgrade; it is not yet read, and this command says so rather
// than silently ignoring it.
func newAdminVerifyIdentifierTypesCmd() *cobra.Command {
	var specPath string
	cmd := &cobra.Command{
		Use:   "verify-identifier-types",
		Short: "Verify every app.* identifier column matches its declared type (Principle 13)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if specPath != "" {
				// `make check-identifiers` already passes --spec whenever
				// SPEC_SNAPSHOT exists on disk; Phase 2 (route catalogue
				// ingest) is what teaches this command to actually read it
				// and cross-check app.esi_route.identifier_types. Until
				// then the flag is accepted — not rejected — so that
				// Makefile target keeps working unmodified once Phase 2
				// lands; only the offline registry check runs today.
				fmt.Printf("admin verify-identifier-types: --spec %q not yet consulted (Phase 2); running the offline registry check only\n", specPath)
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			pool, err := pgxpool.New(cmd.Context(), cfg.DB.URL.Reveal())
			if err != nil {
				return fmt.Errorf("admin verify-identifier-types: connecting to database: %w", err)
			}
			defer pool.Close()

			mismatches, err := verifyIdentifierTypes(cmd.Context(), pool)
			if err != nil {
				return err
			}
			if len(mismatches) > 0 {
				for _, m := range mismatches {
					fmt.Println(m)
				}
				return fmt.Errorf("admin verify-identifier-types: %d identifier column(s) violate Principle 13", len(mismatches))
			}
			fmt.Println("admin verify-identifier-types: all identifier columns match their declared type")
			return nil
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "reserved for Phase 2: path to a captured OpenAPI spec snapshot")
	return cmd
}

// nonIdentifierColumns names "%_id"-suffixed columns that are not ESI
// entity identifiers at all, so Principle 13's bigint/uuid typing rule does
// not apply to them — each was deliberately designed as text:
//   - app.esi_route.operation_id: the OpenAPI operationId string
//     ("get_characters_character_id"), not an ESI-typed field.
//   - app.outbox_event.aggregate_id: a generic cross-aggregate reference
//     (02_DATABASE_SCHEMA.md §4.6) that must hold whatever primary key
//     shape its aggregate uses, bigint or uuid alike — text is the only
//     type that can hold both without coercion.
var nonIdentifierColumns = map[domain.IdentifierKey]bool{
	{Table: "esi_route", Column: "operation_id"}:    true,
	{Table: "outbox_event", Column: "aggregate_id"}: true,
}

// identifierColumn is one row of the information_schema.columns walk.
type identifierColumn struct {
	Table    string
	Column   string
	DataType string
	Default  *string
}

// selfGeneratedDefault matches the two forms 02_DATABASE_SCHEMA.md §3.2
// sanctions for HANGAR-minted internal surrogate keys: PostgreSQL 18's
// built-in uuidv7(), or the pgcrypto-free gen_random_uuid() fallback if
// uuidv7() turns out unavailable. A column carrying either default is, by
// construction, a HANGAR-internal identity rather than a CCP-supplied ESI
// identifier, so it is exempt from the KnownUUIDIdentifiers registry check
// below — there is no spec field for this schema to disagree with.
func selfGeneratedDefault(def *string) bool {
	if def == nil {
		return false
	}
	return strings.Contains(*def, "uuidv7(") || strings.Contains(*def, "gen_random_uuid(")
}

// verifyIdentifierTypes walks every "%_id" column in the app schema and
// checks it against internal/domain.KnownUUIDIdentifiers, returning a
// human-readable mismatch line per violation. An empty, nil-error result
// means every identifier column is consistent.
//
// Two categories of "%_id" uuid column are exempt from registry
// membership, because neither one corresponds to an ESI spec field at all:
//   - self-generated (selfGeneratedDefault) — a HANGAR-minted surrogate key.
//   - a foreign key referencing another table's column — its type is
//     inherited from (and already checked at) the column it references, and
//     Postgres itself refuses to create a FK between mismatched types.
//
// Every remaining "%_id" column — no self-generated default, not a FK — is
// a bare value supplied directly by a sync process or caller with nothing
// else enforcing its type, which is exactly the shape an ESI-sourced
// identifier takes. Those are checked against the registry.
func verifyIdentifierTypes(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name, data_type, column_default
		  FROM information_schema.columns
		 WHERE table_schema = 'app' AND column_name LIKE '%\_id' ESCAPE '\'
		 ORDER BY table_name, column_name`)
	if err != nil {
		return nil, fmt.Errorf("admin verify-identifier-types: querying information_schema: %w", err)
	}
	defer rows.Close()

	var columns []identifierColumn
	for rows.Next() {
		var c identifierColumn
		if err := rows.Scan(&c.Table, &c.Column, &c.DataType, &c.Default); err != nil {
			return nil, fmt.Errorf("admin verify-identifier-types: scanning information_schema row: %w", err)
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fkColumns, err := foreignKeyColumns(ctx, pool)
	if err != nil {
		return nil, err
	}

	var mismatches []string
	for _, c := range columns {
		key := domain.IdentifierKey{Table: c.Table, Column: c.Column}
		if nonIdentifierColumns[key] {
			// A "%_id"-suffixed column that isn't an ESI entity identifier
			// at all — an opaque OpenAPI operationId string, a generic
			// event-aggregate discriminator, etc. Principle 13 has nothing
			// to say about these; they were deliberately designed as text.
			continue
		}

		exempt := selfGeneratedDefault(c.Default) || fkColumns[tableColumn{c.Table, c.Column}] || domain.InternalUUIDIdentifiers[key]

		expected := domain.IdentifierTypeFor(c.Table, c.Column)
		switch {
		case expected == domain.IdentifierUUID:
			if c.DataType != "uuid" {
				mismatches = append(mismatches, fmt.Sprintf(
					"app.%s.%s: registry declares uuid (Principle 13 known-UUID identifier) but column is %s",
					c.Table, c.Column, c.DataType))
			}
		case exempt:
			// A self-generated or foreign-key column may legitimately be
			// uuid, bigint or integer depending on what it points at —
			// nothing more to check.
		default: // domain.IdentifierBigInt — the offline default for every other "%_id" column.
			if c.DataType == "uuid" {
				mismatches = append(mismatches, fmt.Sprintf(
					"app.%s.%s: column is uuid but is not in the KnownUUIDIdentifiers registry and is neither "+
						"self-generated nor a foreign key — either register it in internal/domain.KnownUUIDIdentifiers "+
						"or this is an accidental uuid coercion",
					c.Table, c.Column))
			}
			if strings.HasPrefix(c.DataType, "character") || c.DataType == "text" {
				mismatches = append(mismatches, fmt.Sprintf(
					"app.%s.%s: identifier column stored as %s — Principle 13 prohibits text-typed identifiers "+
						"(a uuid stored as text is explicitly banned; every other identifier is bigint or integer)",
					c.Table, c.Column, c.DataType))
			}
		}
	}

	sort.Strings(mismatches)
	return mismatches, nil
}

type tableColumn struct{ table, column string }

// foreignKeyColumns returns every (table, column) pair in the app schema
// that participates in a FOREIGN KEY constraint.
func foreignKeyColumns(ctx context.Context, pool *pgxpool.Pool) (map[tableColumn]bool, error) {
	rows, err := pool.Query(ctx, `
		SELECT tc.table_name, kcu.column_name
		  FROM information_schema.table_constraints tc
		  JOIN information_schema.key_column_usage kcu
		    ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		 WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'app'`)
	if err != nil {
		return nil, fmt.Errorf("admin verify-identifier-types: querying foreign keys: %w", err)
	}
	defer rows.Close()

	out := make(map[tableColumn]bool)
	for rows.Next() {
		var tc tableColumn
		if err := rows.Scan(&tc.table, &tc.column); err != nil {
			return nil, fmt.Errorf("admin verify-identifier-types: scanning foreign key row: %w", err)
		}
		out[tc] = true
	}
	return out, rows.Err()
}
