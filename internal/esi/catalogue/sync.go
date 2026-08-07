package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SyncResult summarises one Sync pass.
type SyncResult struct {
	Ingested int
	Blocked  int
	Retired  int
}

// IdentifierTypeChangedError is returned by Sync when a route's identifier
// parameter changes Postgres-mapped type between two ingests — e.g. bigint
// to uuid (04_RELEASE_GATES.md §6.3: "CCP has explicitly warned that code
// assuming numeric IDs needs a different data type for UUID routes, so a
// silent coercion is the exact failure mode Principle 13 exists to
// prevent"). Sync aborts the entire pass without writing this route rather
// than upsert a drifted type — a partial ingest that silently accepted the
// coercion would be worse than refusing outright.
type IdentifierTypeChangedError struct {
	OperationID string
	Parameter   string
	OldType     string
	NewType     string
}

func (e *IdentifierTypeChangedError) Error() string {
	return fmt.Sprintf(
		"catalogue: %s parameter %q changed identifier type from %s to %s between ingests — refusing to coerce, this must be reviewed by hand",
		e.OperationID, e.Parameter, e.OldType, e.NewType)
}

// Sync upserts every parsed Route into app.esi_route (and its scope/role
// children), records every observed scope, required-role and cache-mode
// value into their respective open-vocabulary mechanisms, and retires any
// previously-known, still-live route that did not appear in this ingest
// (02_DATABASE_SCHEMA.md §5.2: "A route that disappears from the spec is
// marked retired_at, never deleted"). Ingest is idempotent and additive —
// running it twice with the same routes changes nothing beyond
// updated_at, guarded the same IS DISTINCT FROM way as every Phase 1
// upsert (db/queries/esi_route.sql).
//
// Before writing anything, every route's freshly-parsed IdentifierTypes is
// compared against what was recorded for the same operation on the
// previous ingest; any parameter whose type differs aborts the whole Sync
// with an *IdentifierTypeChangedError (see above) rather than silently
// overwriting app.esi_route.identifier_types.
func Sync(ctx context.Context, store Store, routes []Route) (SyncResult, error) {
	existing, err := store.ListEsiRoutes(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("catalogue: listing existing routes: %w", err)
	}
	previousIdentifierTypes := make(map[string]map[string]string, len(existing))
	for _, e := range existing {
		var types map[string]string
		if len(e.IdentifierTypes) > 0 {
			if err := json.Unmarshal(e.IdentifierTypes, &types); err != nil {
				return SyncResult{}, fmt.Errorf("catalogue: %s has unparseable stored identifier_types: %w", e.OperationID, err)
			}
		}
		previousIdentifierTypes[e.OperationID] = types
	}
	for _, r := range routes {
		prev := previousIdentifierTypes[r.OperationID]
		for param, newType := range r.IdentifierTypes {
			if oldType, tracked := prev[param]; tracked && oldType != newType {
				return SyncResult{}, &IdentifierTypeChangedError{
					OperationID: r.OperationID, Parameter: param, OldType: oldType, NewType: newType,
				}
			}
		}
	}

	stillPresent := make(map[string]bool, len(routes))

	var result SyncResult
	for _, r := range routes {
		stillPresent[r.OperationID] = true

		row, err := upsertRoute(ctx, store, r)
		if err != nil {
			return result, fmt.Errorf("catalogue: upserting route %s: %w", r.OperationID, err)
		}
		result.Ingested++
		if r.BlockedByPin {
			result.Blocked++
		}

		for _, scope := range r.Scopes {
			// Opaque, never parsed (Principle 14 / SRS v3.1 §4.5) — any
			// string ESI hands back in a security block is recorded,
			// first-seen-wins, however novel its grammar (Gate 6 (c)).
			if err := store.UpsertEsiScope(ctx, scope); err != nil {
				return result, fmt.Errorf("catalogue: recording scope %q: %w", scope, err)
			}
			if err := store.AddEsiRouteScope(ctx, row.RouteID, scope); err != nil {
				return result, fmt.Errorf("catalogue: linking route %s to scope %q: %w", r.OperationID, scope, err)
			}
		}
		for _, role := range r.Roles {
			if err := store.AddEsiRouteRole(ctx, row.RouteID, role); err != nil {
				return result, fmt.Errorf("catalogue: linking route %s to role %q: %w", r.OperationID, role, err)
			}
			if err := store.RecordOpenVocabularyValue(ctx, string(domain.VocabRequiredRole), role); err != nil {
				return result, fmt.Errorf("catalogue: recording required-role %q: %w", role, err)
			}
		}
		if r.CacheMode != nil {
			// Every observed cache_mode is recorded, recognised or not
			// (Gate 6 (d)) — SchedulingMode, not this, decides how an
			// unrecognised value is scheduled.
			if err := store.RecordOpenVocabularyValue(ctx, string(domain.VocabCacheMode), *r.CacheMode); err != nil {
				return result, fmt.Errorf("catalogue: recording cache mode %q: %w", *r.CacheMode, err)
			}
		}
	}

	for _, e := range existing {
		if stillPresent[e.OperationID] {
			continue
		}
		if err := store.RetireEsiRoute(ctx, e.RouteID); err != nil {
			return result, fmt.Errorf("catalogue: retiring route %s: %w", e.OperationID, err)
		}
		result.Retired++
	}

	return result, nil
}

func upsertRoute(ctx context.Context, store Store, r Route) (gen.AppEsiRoute, error) {
	specFragment := r.SpecFragment
	if len(specFragment) == 0 {
		specFragment = json.RawMessage(`{}`)
	}
	identifierTypes, err := json.Marshal(r.IdentifierTypes)
	if err != nil {
		return gen.AppEsiRoute{}, fmt.Errorf("encoding identifier_types: %w", err)
	}

	row, err := store.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID:       r.OperationID,
		Method:            r.Method,
		UpstreamPath:      r.UpstreamPath,
		CacheAge:          durationToInterval(r.CacheAge),
		CacheMode:         r.CacheMode,
		RateLimitGroup:    r.RateLimitGroup,
		RateLimitMax:      r.RateLimitMax,
		RateLimitWindow:   durationToInterval(r.RateLimitWindow),
		PaginationStyle:   r.PaginationStyle,
		CompatibilityDate: pgDate(r.CompatibilityDate),
		BlockedByPin:      r.BlockedByPin,
		SpecFragment:      specFragment,
		IdentifierTypes:   identifierTypes,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// UpsertEsiRoute's ON CONFLICT DO UPDATE ... WHERE ... IS
			// DISTINCT FROM (db/queries/esi_route.sql) suppresses the
			// UPDATE when re-ingesting an operation whose fields are all
			// unchanged — and because no row was touched, RETURNING *
			// legitimately returns zero rows, which pgx surfaces as
			// ErrNoRows on a :one query (the same Postgres semantic Phase
			// 1b documented for every upsert of this shape). That is the
			// guard working, not a failure: the route is unchanged, so
			// fetch its current row instead of treating this as an error.
			return store.GetEsiRouteByOperationID(ctx, r.OperationID)
		}
		return gen.AppEsiRoute{}, err
	}
	return row, nil
}

// durationToInterval converts a *time.Duration into the pgtype.Interval
// UpsertEsiRouteParams expects. sqlc.yaml's interval override
// (pg_catalog.interval -> time.Duration) only has a non-nullable variant —
// app.esi_route.cache_age and .rate_limit_window are both nullable, so
// sqlc fell back to the driver's own pgtype.Interval for these two fields
// rather than the intended time.Duration. Handled here at the store
// boundary (where a raw pgtype value is acceptable, same as
// pgtype.Numeric under Principle 9) rather than by changing every
// generated struct's field type project-wide for what is, today, exactly
// two columns.
func durationToInterval(d *time.Duration) pgtype.Interval {
	if d == nil {
		return pgtype.Interval{}
	}
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}
