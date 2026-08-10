// Package handlers implements Phase 7's character-core sync handlers: one
// pair of (Parse, Sync) functions per ESI sub-resource, each parsing a raw
// ESI JSON response into a DTO matching the spec exactly (Principle 13)
// and upserting it via internal/store's IS DISTINCT FROM-guarded queries
// (02_DATABASE_SCHEMA.md §3.5) so a second identical sync touches no
// updated_at.
//
// Every domain follows the same shape: DTOs are unmarshalled directly
// (json.Unmarshal into a well-typed struct IS the "no field loss" parse —
// there is no separate normalisation step for a character-sheet response,
// unlike internal/sync/normalize's route-agnostic envelope handling). A
// full-state list (skills, clones, implants, contacts, ...) is synced
// inside one transaction: upsert every row ESI returned, then delete any
// row for this character not in that set — internal/store.WithTx makes
// that atomic.
package handlers

import (
	"fmt"
	"hash/fnv"
	"time"
)

// SyncResult reports what one domain sync did, for app.sync_run.rows_affected
// (via internal/sync/normalize, which counts the source JSON array — this
// is the DB-side echo of that count, included separately so a caller can
// assert the two agree in tests).
type SyncResult struct {
	RowsAffected int32
}

// nilIfZero turns a decoded zero time.Time (an absent JSON date-time field
// that ESI still includes as a struct field rather than omitting, or that
// Go's zero value fills when a field truly is optional and absent) into a
// nil pointer for a nullable timestamptz column. ESI itself omits absent
// date-time fields entirely (encoding/json leaves the Go field at its
// zero value), so this is the one place that omission needs to become a
// real NULL rather than the Postgres epoch.
func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// syntheticRecordID derives a stable, deterministic int64 key for an ESI
// list element that carries no id of its own (e.g. corporation role
// history — see corporation_structure.go's SyncCorporationRoleHistory).
// FNV-1a over the fields' string forms: same inputs always produce the
// same key, so a re-synced page collides with (and is suppressed by) the
// row it already inserted, rather than duplicating.
func syntheticRecordID(parts ...any) int64 {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = fmt.Fprint(h, p)
		_, _ = h.Write([]byte{0})
	}
	// Mask off the sign bit: app.corporation_role_history.record_id is
	// bigint (signed), and a negative synthetic id is harmless but
	// needlessly surprising in ad-hoc SQL/log output.
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}
