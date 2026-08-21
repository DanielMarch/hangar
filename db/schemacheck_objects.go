package db

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

// ── PHASE 23 (N-6): COLUMNS AND INDEXES, NOT ONLY TABLES ─────────────────
//
// schemacheck.go verifies TABLES. A dropped index, column, constraint or
// partition passed it, and the check was named for what it checked rather
// than for what an operator assumes from "schema is current".
//
// The gap is not symmetric with the one 20.11 closed. A missing table breaks
// loudly and soon; a missing INDEX breaks nothing at all until a query that
// was instant becomes a sequential scan over a partitioned killmail table —
// and nothing in the system says the schema is the reason. That is B-5's
// failure shape, a degradation with no statement of its cause, and it is
// what this file exists to rule out.
//
// ── WHAT IS COVERED, AND WHAT STILL IS NOT ───────────────────────────────
//
// Columns and indexes are covered. CONSTRAINTS and RUNTIME PARTITIONS
// deliberately are not, and saying so plainly is better than a name that
// implies more:
//
//   - CHECK and FOREIGN KEY constraints are enforced by the database on
//     every write, so a dropped one is discovered by the first row that
//     should have been rejected. A real gap, and a far narrower one.
//   - The five `PARTITION OF ... DEFAULT` partitions ARE covered, because
//     they are tables. What is not covered is a partition created at
//     runtime by internal/store/partition.go: no migration declares it, so
//     there is no expected set to compare it against.
//
// ── WHY THE EXPECTED SETS ARE DERIVED, NOT LISTED ────────────────────────
//
// The same reason schemacheck.go gives for tables, and it matters more here
// because the lists are longer: a hand-maintained list of 1,900 columns is a
// list that is wrong within one phase. Whatever the migrations CREATE, minus
// what a later migration's Up section DROPs, is what the database must hold.

// ColumnRef is one column the migrations declare.
type ColumnRef struct {
	Schema string
	Table  string
	Name   string
}

func (c ColumnRef) String() string { return c.Schema + "." + c.Table + "." + c.Name }

func (c ColumnRef) table() TableRef { return TableRef{Schema: c.Schema, Name: c.Table} }

// IndexRef is one index the migrations declare, identified by what it
// INDEXES rather than by its name.
//
// That is forced rather than chosen: 110 of the 111 `CREATE INDEX`
// statements in db/migrations are UNNAMED, so Postgres generates the name
// and there is nothing stable to compare against. The signature — table,
// access method, key columns, and whether it is partial — is what the
// migration actually declares and what the planner actually uses.
//
// ── THE ONE THING THIS SIGNATURE CANNOT DISTINGUISH ──────────────────────
// Two indexes on the same table, method and key columns differing ONLY in
// their WHERE predicates collapse to one signature, so dropping one while
// the other survives would pass. There is no such pair in the schema today
// (TestNoTwoIndexesShareASignature), and comparing predicate TEXT would
// compare a migration's source against Postgres's normalised rendering of
// it — a check that reports drift because a predicate was reformatted is a
// check an operator learns to ignore, which is worse than this bounded and
// stated blind spot.
type IndexRef struct {
	Schema  string
	Table   string
	Method  string // "btree" unless the migration says USING <method>
	Columns []string
	Partial bool // the migration attached a WHERE clause
	// Name is the declared name, or — for the 110 unnamed declarations —
	// the name Postgres will generate for it. It is used for ONE thing:
	// resolving `DROP INDEX app.<name>` inside the migration parse, which
	// is how migration 00045 retires the four indexes it replaces with
	// covering ones. It is never used to compare against the database; see
	// the type comment for why comparison is by signature.
	Name string
}

func (i IndexRef) String() string {
	s := fmt.Sprintf("%s.%s USING %s (%s)", i.Schema, i.Table, i.Method, strings.Join(i.Columns, ", "))
	if i.Partial {
		s += " WHERE ..."
	}
	return s
}

func (i IndexRef) table() TableRef { return TableRef{Schema: i.Schema, Name: i.Table} }

// Signature is the comparison key: everything the migration declares except
// the name it does not give.
func (i IndexRef) Signature() string {
	return fmt.Sprintf("%s.%s|%s|%s|%t", i.Schema, i.Table, i.Method, strings.Join(i.Columns, ","), i.Partial)
}

var (
	// `CREATE TABLE x.y PARTITION OF ...` declares no columns of its own,
	// so the trailing group captures which of the two shapes this is.
	createTableHeadRE = regexp.MustCompile(
		`(?i)\bCREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z_0-9]*)\.([a-z_][a-z_0-9]*)\s*(PARTITION\s+OF|\()`)
	// ALTER TABLE is parsed as a HEADER plus a comma-separated clause list,
	// not as one regex over the whole statement.
	//
	// That is not fastidiousness. db/migrations/00033 writes
	//
	//	ALTER TABLE app.corporation_skyhook
	//	    ALTER COLUMN type_id DROP NOT NULL,
	//	    ALTER COLUMN system_id DROP NOT NULL,
	//	    DROP COLUMN fuel_expires,
	//	    ADD COLUMN reagents jsonb NOT NULL DEFAULT '[]',
	//	    ADD COLUMN is_active boolean;
	//
	// and a regex anchored on `ALTER TABLE x.y DROP COLUMN` matches none of
	// it. Written that way the register went on expecting `fuel_expires` on
	// two tables that dropped it three migrations ago, so every CORRECT
	// database reported permanent drift — the worst failure a check like
	// this can have, because an operator learns to ignore it and then
	// ignores it on the day it means something.
	//
	// Caught by TestAFullyMigratedDatabaseHasNoDrift against a real PG18,
	// which is exactly the assertion that exists to catch it.
	alterTableHeadRE = regexp.MustCompile(
		`(?i)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z_0-9]*)\.([a-z_][a-z_0-9]*)\s`)
	addColumnClauseRE = regexp.MustCompile(
		`(?i)^\s*ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z_0-9]*)`)
	dropColumnClauseRE = regexp.MustCompile(
		`(?i)^\s*DROP\s+COLUMN\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z_0-9]*)`)
	createIndexRE = regexp.MustCompile(
		`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z_0-9]*\s+)?ON\s+(?:ONLY\s+)?([a-z_][a-z_0-9]*)\.([a-z_][a-z_0-9]*)\s*(?:USING\s+([a-z_][a-z_0-9]*)\s*)?\(`)
	dropIndexRE = regexp.MustCompile(
		`(?i)\bDROP\s+INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+EXISTS\s+)?([a-z_][a-z_0-9]*)\.([a-z_][a-z_0-9]*)`)
	whereClauseRE = regexp.MustCompile(`(?i)\bWHERE\b`)
	lineCommentRE = regexp.MustCompile(`--[^\n]*`)
)

// tableConstraintPrefixes are the table-level clauses that a naive comma
// split inside a CREATE TABLE body would otherwise read as columns.
var tableConstraintPrefixes = []string{
	"PRIMARY", "UNIQUE", "FOREIGN", "CHECK", "CONSTRAINT", "EXCLUDE", "LIKE",
}

// ExpectedColumns is every column the embedded migrations leave behind, on
// every table they leave behind, in stable order.
func ExpectedColumns() ([]ColumnRef, error) { return expectedColumnsFromFS(Migrations) }

// ExpectedIndexes is every index the embedded migrations leave behind, in
// stable order.
func ExpectedIndexes() ([]IndexRef, error) { return expectedIndexesFromFS(Migrations) }

// expectedColumnsFromFS is ExpectedColumns over an arbitrary FS, for the
// same reason expectedTablesFromFS is: the parsing rules need to be testable
// against synthetic migrations, while production reads only the embedded
// ones.
func expectedColumnsFromFS(files fs.FS) ([]ColumnRef, error) {
	live, err := liveTables(files)
	if err != nil {
		return nil, err
	}
	sections, err := upSections(files)
	if err != nil {
		return nil, err
	}

	present := map[ColumnRef]bool{}
	var order []ColumnRef
	add := func(ref ColumnRef) {
		if !present[ref] {
			present[ref] = true
			order = append(order, ref)
		}
	}

	for _, up := range sections {
		body := lineCommentRE.ReplaceAllString(up, "")

		for _, loc := range createTableHeadRE.FindAllStringSubmatchIndex(body, -1) {
			schema, table := body[loc[2]:loc[3]], body[loc[4]:loc[5]]
			if strings.EqualFold(strings.Fields(body[loc[6]:loc[7]])[0], "PARTITION") {
				continue // inherits its parent's columns; declares none
			}
			definition, ok := balancedParens(body, loc[7]-1)
			if !ok {
				return nil, fmt.Errorf("db: unbalanced parentheses in the definition of %s.%s", schema, table)
			}
			for _, item := range splitTopLevel(definition) {
				if name, isColumn := columnName(item); isColumn {
					add(ColumnRef{Schema: schema, Table: table, Name: name})
				}
			}
		}
		// ALTER TABLE, clause by clause. Adds are collected first and drops
		// applied after, for the same reason the table parse does it: a
		// drop-then-re-add inside one migration must end up present.
		var dropped []ColumnRef
		for _, loc := range alterTableHeadRE.FindAllStringSubmatchIndex(body, -1) {
			schema, table := strings.ToLower(body[loc[2]:loc[3]]), strings.ToLower(body[loc[4]:loc[5]])
			statement := body[loc[1]:]
			if end := strings.Index(statement, ";"); end >= 0 {
				statement = statement[:end]
			}
			for _, clause := range splitTopLevel(statement) {
				if m := addColumnClauseRE.FindStringSubmatch(clause); m != nil {
					add(ColumnRef{Schema: schema, Table: table, Name: strings.ToLower(m[1])})
					continue
				}
				if m := dropColumnClauseRE.FindStringSubmatch(clause); m != nil {
					dropped = append(dropped, ColumnRef{Schema: schema, Table: table, Name: strings.ToLower(m[1])})
				}
			}
		}
		for _, ref := range dropped {
			delete(present, ref)
		}
	}

	out := make([]ColumnRef, 0, len(present))
	for _, ref := range order {
		// A column of a table a later migration retired is not expected.
		if present[ref] && live[ref.table()] {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func expectedIndexesFromFS(files fs.FS) ([]IndexRef, error) {
	live, err := liveTables(files)
	if err != nil {
		return nil, err
	}
	sections, err := upSections(files)
	if err != nil {
		return nil, err
	}

	bySignature := map[string]IndexRef{}
	var order []string
	// Name → signature, so a later migration's `DROP INDEX app.<name>` can
	// retire an index this parse identifies by its columns. Migration 00045
	// replaces four indexes with covering ones exactly this way, and it
	// spells out the SERVER-GENERATED names of the originals, because the
	// `CREATE INDEX ON <table> (...)` form never gave them one.
	signatureByName := map[string]string{}

	for _, up := range sections {
		body := lineCommentRE.ReplaceAllString(up, "")

		for _, loc := range createIndexRE.FindAllStringSubmatchIndex(body, -1) {
			schema, table := body[loc[4]:loc[5]], body[loc[6]:loc[7]]
			declaredName := ""
			if loc[2] >= 0 {
				declaredName = strings.TrimSpace(strings.ToLower(body[loc[2]:loc[3]]))
			}
			method := "btree"
			if loc[8] >= 0 {
				method = strings.ToLower(body[loc[8]:loc[9]])
			}
			// loc[1] is one past the '(' the pattern ends on.
			openParen := loc[1] - 1
			columnList, ok := balancedParens(body, openParen)
			if !ok {
				return nil, fmt.Errorf("db: unbalanced parentheses in a CREATE INDEX on %s.%s", schema, table)
			}
			var columns []string
			for _, item := range splitTopLevel(columnList) {
				if name := firstIdentifier(item); name != "" {
					columns = append(columns, name)
				}
			}
			// Anything between the close paren and the statement
			// terminator: a WHERE predicate, an INCLUDE list, storage
			// parameters. Only WHERE changes what the index covers.
			tail := body[openParen+len(columnList)+2:]
			if end := strings.Index(tail, ";"); end >= 0 {
				tail = tail[:end]
			}
			ref := IndexRef{
				Schema: strings.ToLower(schema), Table: strings.ToLower(table),
				Method: method, Columns: columns, Partial: whereClauseRE.MatchString(tail),
				Name: declaredName,
			}
			if ref.Name == "" {
				ref.Name = generatedIndexName(ref)
			}
			if _, seen := bySignature[ref.Signature()]; !seen {
				bySignature[ref.Signature()] = ref
				order = append(order, ref.Signature())
			}
			signatureByName[ref.Schema+"."+ref.Name] = ref.Signature()
		}

		// A later migration may legitimately retire an index — migration
		// 00045 replaces four with covering ones. Applied after this file's
		// creates, so a drop-then-recreate inside one migration still ends
		// up present, exactly as the table parse does it.
		for _, m := range dropIndexRE.FindAllStringSubmatch(body, -1) {
			key := strings.ToLower(m[1] + "." + m[2])
			if sig, known := signatureByName[key]; known {
				delete(bySignature, sig)
				delete(signatureByName, key)
			}
		}
	}

	out := make([]IndexRef, 0, len(bySignature))
	for _, sig := range order {
		ref, alive := bySignature[sig]
		// An index a later migration dropped by name, or one whose table a
		// later migration retired, went with it.
		if alive && live[ref.table()] {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signature() < out[j].Signature() })
	return out, nil
}

func liveTables(files fs.FS) (map[TableRef]bool, error) {
	tables, err := expectedTablesFromFS(files)
	if err != nil {
		return nil, err
	}
	live := make(map[TableRef]bool, len(tables))
	for _, ref := range tables {
		live[ref] = true
	}
	return live, nil
}

// upSections reads every migration's `-- +goose Up` half, in file order.
func upSections(files fs.FS) ([]string, error) {
	entries, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("db: listing migrations: %w", err)
	}
	sort.Strings(entries)

	out := make([]string, 0, len(entries))
	for _, name := range entries {
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("db: reading %s: %w", name, err)
		}
		up, err := gooseUpSection(string(raw), name)
		if err != nil {
			return nil, err
		}
		out = append(out, up)
	}
	return out, nil
}

// balancedParens returns the text INSIDE the parenthesis group opening at
// `open`, honouring nesting and single-quoted literals.
func balancedParens(s string, open int) (string, bool) {
	if open < 0 || open >= len(s) || s[open] != '(' {
		return "", false
	}
	depth, inQuote := 0, false
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '\'':
			// '' inside a literal is an escaped quote, and toggling twice
			// is the same as not toggling — no special case needed.
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
				if depth == 0 {
					return s[open+1 : i], true
				}
			}
		}
	}
	return "", false
}

// splitTopLevel splits on commas that are not inside parentheses or quotes.
func splitTopLevel(s string) []string {
	var out []string
	depth, inQuote, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		case ',':
			if !inQuote && depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// columnName reads a column definition's name, reporting false for a
// table-level constraint clause.
func columnName(item string) (string, bool) {
	name := firstIdentifier(item)
	if name == "" {
		return "", false
	}
	for _, keyword := range tableConstraintPrefixes {
		if strings.EqualFold(name, keyword) {
			return "", false
		}
	}
	return name, true
}

// firstIdentifier returns the leading identifier token of a clause,
// lowercased — the column name in a definition, and the indexed column in
// an index key list, where `occurred_at DESC` and `name COLLATE "C"` both
// yield the first token.
//
// An EXPRESSION index key — `(lower(name))` — would yield the function name
// rather than a column, which matches nothing Postgres reports and would
// produce a permanent false "missing index". There are none in the schema,
// and TestExpectedIndexesHaveNoExpressionKeys keeps it that way, so this
// stays a parse of what these migrations contain rather than a parser for
// SQL in general.
func firstIdentifier(item string) string {
	item = strings.TrimSpace(item)
	end := 0
	for end < len(item) {
		c := item[end]
		isLetter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		if !isLetter && !isDigit && c != '_' {
			break
		}
		end++
	}
	return strings.ToLower(item[:end])
}

// generatedIndexName reproduces the name PostgreSQL gives an index the
// migration did not name: `<table>_<col1>_<col2>_..._idx`, truncated to the
// 63-byte identifier limit.
//
// It exists for one reason. 110 of the 111 CREATE INDEX statements in
// db/migrations are unnamed, and migration 00045 retires four of them with
// `DROP INDEX app.wallet_journal_owner_kind_owner_id_date_idx` and friends —
// spelling out the server-generated names, as its own comment says it has
// to. Without this, that DROP matches nothing, the register goes on
// expecting four indexes the schema replaced, and every correct database
// reports permanent drift.
//
// Postgres additionally appends `1`, `2`, … to break ties with an existing
// relation of that name. This does not reproduce that, because it would
// require knowing the whole namespace at parse time — and a collision would
// show up as a failing TestAFullyMigratedDatabaseHasNoDrift rather than as
// a silent wrong answer, which is the direction that can be lived with.
func generatedIndexName(ref IndexRef) string {
	parts := append([]string{ref.Table}, ref.Columns...)
	name := strings.Join(parts, "_") + "_idx"
	const maxIdentifier = 63
	if len(name) > maxIdentifier {
		name = name[:maxIdentifier]
	}
	return name
}
