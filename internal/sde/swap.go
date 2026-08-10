package sde

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SmokeQuery is one verification query run against sde_next before the
// swap — 02_DATABASE_SCHEMA.md §6 step 2's "smoke query per table". Query
// must return exactly one row with exactly one bigint-compatible column;
// WantMin is the minimum acceptable value (typically 1, "this table is
// not suspiciously empty").
type SmokeQuery struct {
	Table   string
	SQL     string
	WantMin int64
}

// DefaultSmokeQueries covers every table Build() populates from a spec —
// derived tables that stay structure-only (see import.go's tableSpecs doc
// comment) are deliberately excluded, since "at least one row" would be a
// false-failure for a table this phase doesn't populate yet.
func DefaultSmokeQueries() []SmokeQuery {
	tables := []string{
		"category", "group_", "market_group", "type", "region", "constellation",
		"solar_system", "station_operation", "station", "planet", "moon",
		"dogma_attribute", "dogma_effect", "icon", "graphic", "faction",
		"npc_corporation", "race", "bloodline", "ancestry", "skin", "blueprint",
	}
	out := make([]SmokeQuery, len(tables))
	for i, t := range tables {
		out[i] = SmokeQuery{Table: t, SQL: fmt.Sprintf("SELECT count(*) FROM sde_next.%s", t), WantMin: 1}
	}
	return out
}

// Verify runs row-count-against-manifest and smoke-query checks
// (02_DATABASE_SCHEMA.md §6 step 2) against sde_next. It returns a
// descriptive error on the first failure rather than aborting silently —
// the caller (Import, below) is responsible for treating any error here
// as "do not swap, drop sde_next".
func Verify(ctx context.Context, pool *pgxpool.Pool, result Result, queries []SmokeQuery) error {
	for _, q := range queries {
		var got int64
		if err := pool.QueryRow(ctx, q.SQL).Scan(&got); err != nil {
			return fmt.Errorf("sde: smoke query for %s failed: %w", q.Table, err)
		}
		if got < q.WantMin {
			return fmt.Errorf("sde: smoke query for %s returned %d rows, want at least %d", q.Table, got, q.WantMin)
		}
		if want, ok := result.RowCounts[q.Table]; ok && got != want {
			return fmt.Errorf("sde: %s: sde_next has %d rows but the import streamed %d — row count mismatch against the manifest", q.Table, got, want)
		}
	}
	return nil
}

// Swap performs 02_DATABASE_SCHEMA.md §6 steps 3-5: the rename in one
// short transaction, then (separately, deliberately NOT in that
// transaction — the rename takes an ACCESS EXCLUSIVE lock and must be held
// for microseconds, not for as long as a DROP of a whole schema takes) a
// drop of the now-renamed-away old schema after gracePeriod.
//
// If sde_next fails verification, Swap is never called: the caller drops
// sde_next directly and the live `sde` schema is provably untouched,
// which is exactly what TestSDEAtomicSwap asserts.
func Swap(ctx context.Context, pool *pgxpool.Pool, gracePeriod time.Duration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sde: beginning swap transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	if _, err := tx.Exec(ctx, `ALTER SCHEMA sde RENAME TO sde_old`); err != nil {
		return fmt.Errorf("sde: renaming sde to sde_old: %w", err)
	}
	if _, err := tx.Exec(ctx, `ALTER SCHEMA sde_next RENAME TO sde`); err != nil {
		return fmt.Errorf("sde: renaming sde_next to sde: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sde: committing swap: %w", err)
	}

	if gracePeriod > 0 {
		time.Sleep(gracePeriod)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA sde_old CASCADE`); err != nil {
		// The swap itself already succeeded and committed — a failed drop
		// of the orphaned old schema is an operational cleanup problem,
		// not a correctness one, and must not be reported as if the swap
		// failed.
		return fmt.Errorf("sde: swap succeeded but dropping sde_old failed (manual cleanup needed): %w", err)
	}
	return nil
}

// AbortBuild drops sde_next after a failed Build/Verify — the live `sde`
// schema was never touched by either step, so this is the entire recovery
// action needed.
func AbortBuild(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS sde_next CASCADE`); err != nil {
		return fmt.Errorf("sde: dropping failed sde_next build: %w", err)
	}
	return nil
}

// Import runs the full 02_DATABASE_SCHEMA.md §6 pipeline: build, verify,
// swap, record the outcome in app.sde_import via internal/store's already
// generated StartSdeImport/CompleteSdeImport (db/queries/reference.sql).
// On any failure before Swap is reached, sde_next is dropped and `sde` is
// left exactly as it was — the property TestSDEAtomicSwap exercises by
// injecting a failure partway through Build.
func Import(ctx context.Context, pool *pgxpool.Pool, s *store.Store, src SourceProvider, queries []SmokeQuery, gracePeriod time.Duration) error {
	imp, err := s.StartSdeImport(ctx)
	if err != nil {
		return fmt.Errorf("sde: recording import start: %w", err)
	}
	complete := func(status string, checksum string, rowCounts []byte, importErr error) error {
		var checksumPtr *string
		if checksum != "" {
			checksumPtr = &checksum
		}
		var errPtr *string
		if importErr != nil {
			msg := importErr.Error()
			errPtr = &msg
		}
		if rowCounts == nil {
			rowCounts = []byte(`{}`)
		}
		return s.CompleteSdeImport(ctx, gen.CompleteSdeImportParams{
			ImportID: imp.ImportID, Status: status, Checksum: checksumPtr, RowCounts: rowCounts, Error: errPtr,
		})
	}

	result, buildErr := Build(ctx, pool, src)
	if buildErr != nil {
		_ = AbortBuild(ctx, pool)
		_ = complete("failed", "", nil, buildErr)
		return buildErr
	}

	if verifyErr := Verify(ctx, pool, result, queries); verifyErr != nil {
		_ = AbortBuild(ctx, pool)
		_ = complete("failed", "", nil, verifyErr)
		return verifyErr
	}

	rowCounts, err := json.Marshal(result.RowCounts)
	if err != nil {
		_ = AbortBuild(ctx, pool)
		return fmt.Errorf("sde: marshalling row counts: %w", err)
	}

	if err := complete("verified", result.Checksum, rowCounts, nil); err != nil {
		_ = AbortBuild(ctx, pool)
		return fmt.Errorf("sde: recording verified state: %w", err)
	}

	if swapErr := Swap(ctx, pool, gracePeriod); swapErr != nil {
		_ = complete("failed", result.Checksum, rowCounts, swapErr)
		return swapErr
	}

	return complete("swapped", result.Checksum, rowCounts, nil)
}
