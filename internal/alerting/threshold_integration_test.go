//go:build integration

package alerting_test

// Phase 20.4 (B25) — §4.4's THRESHOLD category, which had no evaluator
// until this phase and therefore generated zero alerts on every
// installation ever built.
//
// The four thresholds are tested against REAL ROWS in the real tables the
// sync engine writes, not against a fake, because the thing most likely to
// be wrong about a threshold is its query — the join, the NULL handling,
// the boundary — and a fake would agree with whatever the Go code
// believed.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const thresholdCorporationID = int64(98000042)

// seedThresholdRoutes makes the four threshold alert types seedable.
// app.alert_type's threshold_declares_source CHECK requires a NOT NULL
// source_route_id, so db/seed/alert_types.sql inserts them through a JOIN
// against app.esi_route — present on a real installation once Phase 2's
// ingest has run.
func seedThresholdRoutes(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s := store.New(pool)
	for i, path := range catalogue.ThresholdSourceRoutes() {
		_, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
			OperationID: "ThresholdRoute" + uuid.NewString()[:8], Method: "GET", UpstreamPath: path,
			CompatibilityDate: pgtype.Date{Time: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), Valid: true},
			SpecFragment:      json.RawMessage(`{}`), IdentifierTypes: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
		_ = i
	}
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))

	_, err := pool.Exec(ctx,
		`INSERT INTO app.corporation (corporation_id, name, ticker) VALUES ($1, 'Threshold Corp', 'THR')
		 ON CONFLICT (corporation_id) DO NOTHING`, thresholdCorporationID)
	require.NoError(t, err)
}

func newThresholdEvaluator(t *testing.T, pool *pgxpool.Pool) *alerting.Evaluator {
	t.Helper()
	return &alerting.Evaluator{
		Pool: pool,
		// Coalescing off: this suite is about WHICH subjects cross and how
		// often they re-fire, and a five-minute window would put every
		// assertion behind the same key.
		Emitter: &alerting.Emitter{Pool: pool, Window: -1},
	}
}

// TestEveryCatalogueThresholdIsEvaluated is the guard §4.4's build-time
// rule implies but does not state: a threshold declared in the catalogue
// and not evaluated by anything generates zero alerts, which is the same
// silence as one whose source route is unpolled.
//
// It works by evaluating with margins wide enough to catch a subject
// seeded for EACH of the four, and requiring all four to have produced
// one. A fifth catalogue threshold added without an evaluator fails here
// rather than shipping quiet.
func TestEveryCatalogueThresholdIsEvaluated(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	seedThresholdRoutes(t, pool)
	s := store.New(pool)

	for _, threshold := range catalogue.Thresholds() {
		seedChannelAndRule(t, s, threshold.Name, "corporation", "98000042")
	}

	// One subject per threshold, each comfortably over its line.
	_, err := pool.Exec(ctx, `
		INSERT INTO app.corporation_structure (corporation_id, structure_id, type_id, system_id, fuel_expires)
		VALUES ($1, 1000000000001, 35832, 30000142, now() + interval '6 hours')`, thresholdCorporationID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO app.corporation_starbase (corporation_id, starbase_id, type_id, system_id)
		VALUES ($1, 2000000000001, 16213, 30000142)`, thresholdCorporationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO app.starbase_detail (corporation_id, starbase_id, system_id, fuels)
		VALUES ($1, 2000000000001, 30000142, '[{"type_id": 4051, "quantity": 120}]'::jsonb)`, thresholdCorporationID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO app.character (character_id, name, owner_hash) VALUES (3000001, 'Lapsed Pilot', 'h')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO app.corporation_member_tracking (corporation_id, character_id, logoff_date)
		VALUES ($1, 3000001, now() - interval '200 days')`, thresholdCorporationID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO app.contract (owner_kind, owner_id, contract_id, issuer_id, issuer_corporation_id,
		                          type, status, availability, date_issued, date_expired)
		VALUES ('corporation', $1, 4000001, 3000001, $1, 'item_exchange', 'outstanding', 'corporation',
		        now() - interval '1 day', now() + interval '12 hours')`, thresholdCorporationID)
	require.NoError(t, err)

	result, err := newThresholdEvaluator(t, pool).Evaluate(ctx)
	require.NoError(t, err)

	for _, threshold := range catalogue.Thresholds() {
		require.Equal(t, 1, result.ByType[threshold.Name],
			"catalogue threshold %q produced no alert — either it has no evaluator, or its query does not "+
				"match the rows the sync engine writes. Both silently generate nothing.", threshold.Name)
	}
	require.Equal(t, len(catalogue.Thresholds()), result.Subjects)
	require.Equal(t, len(catalogue.Thresholds()), result.Emitted)
}

// TestThresholdReArmsOnlyWhenTheSituationChanges is the property that makes
// a ten-minute evaluator tolerable: the same structure still low on fuel
// must not produce an alert every ten minutes forever, and a REFUELLED and
// re-drained structure must produce a new one.
//
// It is the reason ThresholdFingerprint takes a `bucket` at all, and the
// reason that bucket is never the time of the evaluation.
func TestThresholdReArmsOnlyWhenTheSituationChanges(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	seedThresholdRoutes(t, pool)
	s := store.New(pool)
	seedChannelAndRule(t, s, "corporation.structure.fuel_low", "corporation", "98000042")

	_, err := pool.Exec(ctx, `
		INSERT INTO app.corporation_structure (corporation_id, structure_id, type_id, system_id, fuel_expires)
		VALUES ($1, 1000000000009, 35832, 30000142, now() + interval '6 hours')`, thresholdCorporationID)
	require.NoError(t, err)

	evaluator := newThresholdEvaluator(t, pool)

	first, err := evaluator.Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, first.Emitted)

	// Nothing has changed. The structure is still low; the operator has
	// already been told.
	for pass := 0; pass < 3; pass++ {
		again, err := evaluator.Evaluate(ctx)
		require.NoError(t, err)
		require.Zero(t, again.Emitted, "pass %d re-fired an unchanged threshold", pass)
		require.Equal(t, 1, again.Deduplicated)
	}

	// Refuelled — and then run down again to a DIFFERENT expiry. That is a
	// genuinely new occurrence and must alert.
	_, err = pool.Exec(ctx,
		`UPDATE app.corporation_structure SET fuel_expires = now() + interval '3 hours'
		  WHERE structure_id = 1000000000009`)
	require.NoError(t, err)

	after, err := evaluator.Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, after.Emitted,
		"a new fuel expiry is a new occurrence — the re-arm token moved, so the alert must fire again")
}

// TestThresholdDoesNotCrossCorporations is EmitRequest.TargetFilter's
// reason for existing. Routing resolves by alert TYPE only, so without the
// filter an installation serving two corporations would send each
// corporation's fuel alerts to the other's channel — with nothing in the
// payload to reveal it but a structure id.
func TestThresholdDoesNotCrossCorporations(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	seedThresholdRoutes(t, pool)
	s := store.New(pool)

	const otherCorporationID = int64(98000099)
	_, err := pool.Exec(ctx,
		`INSERT INTO app.corporation (corporation_id, name, ticker) VALUES ($1, 'Other Corp', 'OTH')`,
		otherCorporationID)
	require.NoError(t, err)

	// Only the OTHER corporation subscribes.
	seedChannelAndRule(t, s, "corporation.structure.fuel_low", "corporation", "98000099")

	_, err = pool.Exec(ctx, `
		INSERT INTO app.corporation_structure (corporation_id, structure_id, type_id, system_id, fuel_expires)
		VALUES ($1, 1000000000021, 35832, 30000142, now() + interval '6 hours')`, thresholdCorporationID)
	require.NoError(t, err)

	result, err := newThresholdEvaluator(t, pool).Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Subjects, "the structure did cross its threshold")
	require.Zero(t, result.Emitted,
		"a corporation-scoped rule must not receive another corporation's structures")

	var events int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.alert_event`).Scan(&events))
	require.Zero(t, events)
}

// TestStarbaseWithNoFuelDataIsNotAStarbaseWithNoFuel is SRS §6's
// empty-versus-unavailable rule applied to a threshold.
//
// app.starbase_detail.fuels DEFAULTS to '[]', so every starbase whose
// detail fan-out has not run yet sums to zero. Reading that as an empty
// fuel bay would alert on every tower an installation has ever seen, at
// the moment it first sees it.
func TestStarbaseWithNoFuelDataIsNotAStarbaseWithNoFuel(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	seedThresholdRoutes(t, pool)
	s := store.New(pool)
	seedChannelAndRule(t, s, "corporation.starbase.fuel_low", "corporation", "98000042")

	for i, fuels := range []string{`[]`, `[{"type_id": 4051, "quantity": 60}]`} {
		starbaseID := int64(2000000000100 + i)
		_, err := pool.Exec(ctx, `
			INSERT INTO app.corporation_starbase (corporation_id, starbase_id, type_id, system_id)
			VALUES ($1, $2, 16213, 30000142)`, thresholdCorporationID, starbaseID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO app.starbase_detail (corporation_id, starbase_id, system_id, fuels)
			VALUES ($1, $2, 30000142, $3::jsonb)`, thresholdCorporationID, starbaseID, fuels)
		require.NoError(t, err)
	}

	result, err := newThresholdEvaluator(t, pool).Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Subjects,
		"only the starbase with a REAL, low fuel reading may alert — an unfetched fuel bay is not an empty one")
}

// TestUnroutedThresholdIsNotEvaluated pins the cost guard. A threshold
// nobody subscribes to — corporation.member.inactive is default_enabled=
// false and will be unrouted on most installations forever — must cost one
// routing query per pass, not one transaction per subject.
func TestUnroutedThresholdIsNotEvaluated(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	seedThresholdRoutes(t, pool)

	_, err := pool.Exec(ctx, `
		INSERT INTO app.corporation_structure (corporation_id, structure_id, type_id, system_id, fuel_expires)
		VALUES ($1, 1000000000033, 35832, 30000142, now() + interval '6 hours')`, thresholdCorporationID)
	require.NoError(t, err)

	result, err := newThresholdEvaluator(t, pool).Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, result.Unrouted, "no routing rules exist, so all four thresholds are skipped")
	require.Zero(t, result.Subjects, "an unrouted threshold must not read its source table at all")
}
