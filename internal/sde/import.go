package sde

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tableSpec describes one `sde.*` table this package knows how to build:
// its COPY column order and a row extractor translating one decoded JSONL
// Row into that column order. `data` is always the raw decoded row,
// re-marshalled — Principle-14-flavoured "keep everything, index the few
// columns actually joined against" (00036's migration header explains the
// scope call this is).
type tableSpec struct {
	table   string
	columns []string
	extract func(row Row) ([]any, error)
}

// errSkipRow lets an extractor discard a row that doesn't belong in this
// table's data (used by the derived join tables below, which iterate
// sub-fields of a parent row and may find nothing to emit for a given
// parent).
var errSkipRow = fmt.Errorf("sde: skip row")

func rawData(row Row) ([]byte, error) {
	full := map[string]any{"_key": row.Key}
	for k, v := range row.Fields {
		full[k] = v
	}
	b, err := json.Marshal(full)
	if err != nil {
		return nil, fmt.Errorf("sde: re-marshalling row %v: %w", row.Key, err)
	}
	return b, nil
}

// firstColName is every table's PK column name, following 00036's naming
// (<table>_id, or a CCP-specific PK name for the handful that differ).
func firstColName(table string) string {
	switch table {
	case "group_":
		return "group_id"
	case "market_group":
		return "market_group_id"
	case "station_operation":
		return "operation_id"
	case "dogma_attribute":
		return "attribute_id"
	case "dogma_effect":
		return "effect_id"
	case "blueprint":
		return "blueprint_type_id"
	case "npc_corporation":
		return "corporation_id"
	default:
		return table + "_id"
	}
}

// tableSpecs is every `sde.*` table 00036 creates, in the order the
// importer builds and verifies them. The 22 "one row per JSONL line"
// tables share simpleSpec's shape; type/blueprint carry a couple of extra
// scalar columns pulled straight off the row; type_dogma_attribute,
// type_material and blueprint_activity are DERIVED — flattened out of a
// nested array/object on the `type`/`blueprint` row rather than having
// their own JSONL file. CCP's exact nested-field names for those three
// (dogma attribute lists, material lists, per-activity blueprint data)
// were not independently confirmed against a live JSONL sample as part of
// this phase (no network access to the real SDE export at build time);
// the extractors below try several plausible field-name spellings and
// SKIP (never error) when none match, so a wrong guess degrades to "this
// derived table stays empty" rather than aborting the whole import.
// Flagged here rather than silently presented as verified.
func tableSpecs() []tableSpec {
	specs := []tableSpec{
		nameSpec("category"),
		{table: "group_", columns: []string{"group_id", "category_id", "name", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: group row missing key")
			}
			catID, _ := asInt64(row.Fields["categoryID"])
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, catID, englishName(row.Fields), data}, nil
		}},
		nameSpec("market_group"),
		{table: "type", columns: []string{"type_id", "group_id", "market_group_id", "name", "volume", "mass", "published", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: type row missing key")
			}
			groupID, _ := asInt64(row.Fields["groupID"])
			var marketGroupID any
			if v, ok := asInt64(row.Fields["marketGroupID"]); ok {
				marketGroupID = v
			}
			var volume, mass any
			if v, ok := asFloat64(row.Fields["volume"]); ok {
				volume = v
			}
			if v, ok := asFloat64(row.Fields["mass"]); ok {
				mass = v
			}
			published, _ := asBool(row.Fields["published"])
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, groupID, marketGroupID, englishName(row.Fields), volume, mass, published, data}, nil
		}},
		nameSpec("region"),
		fkNameSpec("constellation", "regionID"),
		{table: "solar_system", columns: []string{"solar_system_id", "constellation_id", "region_id", "name", "security_status", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: solar_system row missing key")
			}
			constID, _ := asInt64(row.Fields["constellationID"])
			regionID, _ := asInt64(row.Fields["regionID"])
			var sec any
			if v, ok := asFloat64(row.Fields["securityStatus"]); ok {
				sec = v
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, constID, regionID, englishName(row.Fields), sec, data}, nil
		}},
		nameSpec("station_operation"),
		fkNameSpec("station", "solarSystemID"),
		{table: "planet", columns: []string{"planet_id", "solar_system_id", "type_id", "name", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: planet row missing key")
			}
			sysID, _ := asInt64(row.Fields["solarSystemID"])
			var typeID any
			if v, ok := asInt64(row.Fields["typeID"]); ok {
				typeID = v
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, sysID, typeID, englishName(row.Fields), data}, nil
		}},
		{table: "moon", columns: []string{"moon_id", "planet_id", "solar_system_id", "name", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: moon row missing key")
			}
			planetID, _ := asInt64(row.Fields["planetID"])
			sysID, _ := asInt64(row.Fields["solarSystemID"])
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, planetID, sysID, englishName(row.Fields), data}, nil
		}},
		nameSpec("dogma_attribute"),
		nameSpec("dogma_effect"),
		fileNameSpec("icon"),
		fileNameSpec("graphic"),
		nameSpec("faction"),
		fkNameSpec("npc_corporation", "factionID"),
		nameSpec("race"),
		fkNameSpec("bloodline", "raceID"),
		fkNameSpec("ancestry", "bloodlineID"),
		{table: "skin", columns: []string{"skin_id", "type_id", "name", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: skin row missing key")
			}
			var typeID any
			if v, ok := asInt64(row.Fields["typeID"]); ok {
				typeID = v
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, typeID, englishName(row.Fields), data}, nil
		}},
		{table: "blueprint", columns: []string{"blueprint_type_id", "max_production_limit", "data"}, extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: blueprint row missing key")
			}
			var limit any
			if v, ok := asInt64(row.Fields["maxProductionLimit"]); ok {
				limit = v
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, limit, data}, nil
		}},
	}
	return specs
}

// nameSpec is the plain "id + english name + data" shape (category,
// market_group, region, dogma_attribute, dogma_effect, icon, graphic,
// faction, race).
func nameSpec(table string) tableSpec {
	return tableSpec{
		table:   table,
		columns: []string{firstColName(table), "name", "data"},
		extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: %s row missing key", table)
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, englishName(row.Fields), data}, nil
		},
	}
}

// fileNameSpec is "id + nullable file_name + data" — sde.icon and
// sde.graphic (00036) key on the asset file name, not a localized `name`.
func fileNameSpec(table string) tableSpec {
	return tableSpec{
		table:   table,
		columns: []string{firstColName(table), "file_name", "data"},
		extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: %s row missing key", table)
			}
			var fileName any
			if v, ok := row.Fields["fileName"].(string); ok {
				fileName = v
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, fileName, data}, nil
		},
	}
}

// fkNameSpec is "id + one nullable fk + english name + data" (group_'s
// siblings: constellation/npc_corporation/bloodline/ancestry, and
// station).
func fkNameSpec(table, fkField string) tableSpec {
	fkCol := fkColumnName(table)
	return tableSpec{
		table:   table,
		columns: []string{firstColName(table), fkCol, "name", "data"},
		extract: func(row Row) ([]any, error) {
			id, ok := asInt64(row.Key)
			if !ok {
				return nil, fmt.Errorf("sde: %s row missing key", table)
			}
			var fk any
			if v, ok := asInt64(row.Fields[fkField]); ok {
				fk = v
			}
			data, err := rawData(row)
			if err != nil {
				return nil, err
			}
			return []any{id, fk, englishName(row.Fields), data}, nil
		},
	}
}

func fkColumnName(table string) string {
	switch table {
	case "constellation":
		return "region_id"
	case "station":
		return "solar_system_id"
	case "npc_corporation":
		return "faction_id"
	case "bloodline":
		return "race_id"
	case "ancestry":
		return "bloodline_id"
	default:
		return table + "_fk"
	}
}

// SourceProvider opens the JSONL stream for one table. Production code
// backs this with a downloaded-and-unzipped SDE build (a temp file on
// disk, never the whole payload in memory — see doc.go); tests back it
// with local testdata fixtures.
type SourceProvider interface {
	Open(ctx context.Context, table string) (io.ReadCloser, error)
}

// Result is what Build reports for app.sde_import bookkeeping.
type Result struct {
	Checksum  string
	RowCounts map[string]int64
}

// Build streams every table in tableSpecs() into a freshly created
// `sde_next` schema (02_DATABASE_SCHEMA.md §6 step 1). Each table is
// created with `CREATE TABLE sde_next.<t> (LIKE sde.<t> INCLUDING ALL)` —
// cloning the live `sde` schema's own structure rather than duplicating
// the DDL a second time (00036's migration header) — then populated with
// pgx's CopyFrom, which streams row-by-row rather than building one giant
// INSERT.
//
// A source that has no data for a table (SourceProvider.Open returning a
// "not found"-shaped error) is tolerated: some SDE builds omit a table
// entirely. Any OTHER error aborts immediately, leaving `sde_next` as a
// half-built schema for swap.go's caller to DROP — the live `sde` schema
// is never touched by Build itself, which is the property
// TestSDEAtomicSwap checks.
func Build(ctx context.Context, pool *pgxpool.Pool, src SourceProvider) (Result, error) {
	if _, err := pool.Exec(ctx, `CREATE SCHEMA sde_next`); err != nil {
		return Result{}, fmt.Errorf("sde: creating sde_next: %w", err)
	}

	result := Result{RowCounts: map[string]int64{}}
	hasher := sha256.New()

	// Clone EVERY table that exists in the live `sde` schema, not just the
	// ones this package has a populate-from-JSONL spec for — this is what
	// guarantees the swap (swap.go) never silently drops a table just
	// because its importer registration is incomplete. type_dogma_attribute,
	// type_material and blueprint_activity (derived from nested fields on
	// the type/blueprint rows — see tableSpecs' doc comment on why their
	// exact upstream shape isn't confirmed yet) clone with structure only
	// and stay empty until that's resolved; every other table streams real
	// data.
	liveTables, err := listLiveSDETables(ctx, pool)
	if err != nil {
		return result, err
	}

	specByTable := map[string]tableSpec{}
	for _, sp := range tableSpecs() {
		specByTable[sp.table] = sp
	}

	for _, table := range liveTables {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE sde_next.%s (LIKE sde.%s INCLUDING ALL)`, pgIdent(table), pgIdent(table))); err != nil {
			return result, fmt.Errorf("sde: cloning structure for sde_next.%s: %w", table, err)
		}

		spec, hasSpec := specByTable[table]
		if !hasSpec {
			fmt.Fprintf(hasher, "%s:0\n", table)
			continue // structure-only clone; not yet populated (see doc comment above)
		}

		rc, err := src.Open(ctx, spec.table)
		if err != nil {
			if isNotFound(err) {
				continue // this build omits the table; leave it empty
			}
			return result, fmt.Errorf("sde: opening source for %s: %w", spec.table, err)
		}

		var n int64
		var rows [][]any
		flush := func() error {
			if len(rows) == 0 {
				return nil
			}
			_, cerr := pool.CopyFrom(ctx, pgx.Identifier{"sde_next", spec.table}, spec.columns, pgx.CopyFromRows(rows))
			rows = rows[:0]
			return cerr
		}

		readErr := ReadJSONL(rc, func(row Row) error {
			values, err := spec.extract(row)
			if err == errSkipRow {
				return nil
			}
			if err != nil {
				return err
			}
			rows = append(rows, values)
			n++
			if len(rows) >= 5000 { // bounded batch: never accumulate the whole table in memory
				return flush()
			}
			return nil
		})
		closeErr := rc.Close()
		if readErr != nil {
			return result, fmt.Errorf("sde: streaming %s: %w", spec.table, readErr)
		}
		if err := flush(); err != nil {
			return result, fmt.Errorf("sde: copying %s into sde_next: %w", spec.table, err)
		}
		if closeErr != nil {
			return result, fmt.Errorf("sde: closing source for %s: %w", spec.table, closeErr)
		}

		result.RowCounts[spec.table] = n
		fmt.Fprintf(hasher, "%s:%d\n", spec.table, n)
	}

	result.Checksum = hex.EncodeToString(hasher.Sum(nil))
	return result, nil
}

// listLiveSDETables reads the current `sde` schema's own table list from
// information_schema — the single source of truth for "what does sde_next
// need to end up with", so Build() can never drift from 00036's DDL no
// matter how the Go-side spec registry above is (or isn't) kept in sync.
// Sorted so the checksum in Result is deterministic across runs.
func listLiveSDETables(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'sde' ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("sde: listing live sde tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("sde: scanning sde table name: %w", err)
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sde: reading sde table list: %w", err)
	}
	sort.Strings(tables)
	return tables, nil
}

func pgIdent(s string) string { return s } // table names here are all internal constants, never user input

type notFoundErr struct{ err error }

func (e notFoundErr) Error() string { return e.err.Error() }
func (e notFoundErr) Unwrap() error { return e.err }

// NotFound wraps an error so Build treats a missing table's source as
// "leave it empty" instead of aborting the whole import.
func NotFound(err error) error { return notFoundErr{err} }

func isNotFound(err error) bool {
	_, ok := err.(notFoundErr)
	return ok
}
