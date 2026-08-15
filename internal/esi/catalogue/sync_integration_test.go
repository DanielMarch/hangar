//go:build integration

package catalogue_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"),
		tcpostgres.WithUsername("hangar"),
		tcpostgres.WithPassword("hangar"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })

	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	return store.New(pool)
}

func readFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

// TestRoutesNewerThanPinAreBlocked — blocked_by_pin = true and excluded
// from the scheduling query (Phase 2 exit criterion).
func TestRoutesNewerThanPinAreBlocked(t *testing.T) {
	s := newMigratedStore(t)
	ctx := context.Background()

	spec := readFixtureFile(t, "../../../test/drift/gate6_synthetic_spec.json")
	pin, err := catalogue.ParseDate(catalogue.DefaultCompatibilityPin) // 2026-08-04
	require.NoError(t, err)

	routes, err := catalogue.ParseSpec(spec, pin)
	require.NoError(t, err)

	result, err := catalogue.Sync(ctx, s, routes)
	require.NoError(t, err)
	require.Greater(t, result.Blocked, 0, "expected at least one blocked route (GetSyntheticFutureRoute)")

	all, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	var blockedRow *string
	for _, r := range all {
		if r.OperationID == "GetSyntheticFutureRoute" {
			require.True(t, r.BlockedByPin, "GetSyntheticFutureRoute must be blocked_by_pin")
			op := r.OperationID
			blockedRow = &op
		}
	}
	require.NotNil(t, blockedRow, "GetSyntheticFutureRoute must still appear in ListEsiRoutes (administrator visibility)")

	blocked, err := s.ListBlockedEsiRoutes(ctx)
	require.NoError(t, err)
	found := false
	for _, r := range blocked {
		if r.OperationID == "GetSyntheticFutureRoute" {
			found = true
		}
	}
	require.True(t, found, "GetSyntheticFutureRoute must appear on ListBlockedEsiRoutes (/admin/esi/catalogue/blocked)")

	schedulable, err := s.ListSchedulableEsiRoutes(ctx)
	require.NoError(t, err)
	for _, r := range schedulable {
		require.NotEqual(t, "GetSyntheticFutureRoute", r.OperationID,
			"a blocked route must never appear in the scheduling query")
	}
}

// TestGate6SyntheticSpecIngestsCleanly proves all four Gate 6 conditions
// (04_RELEASE_GATES.md §6.1) ingest correctly against a real database,
// exercising the exact code path Sync uses in production.
func TestGate6SyntheticSpecIngestsCleanly(t *testing.T) {
	s := newMigratedStore(t)
	ctx := context.Background()

	spec := readFixtureFile(t, "../../../test/drift/gate6_synthetic_spec.json")
	pin, err := catalogue.ParseDate(catalogue.DefaultCompatibilityPin)
	require.NoError(t, err)

	routes, err := catalogue.ParseSpec(spec, pin)
	require.NoError(t, err)
	result, err := catalogue.Sync(ctx, s, routes)
	require.NoError(t, err)
	require.Equal(t, len(routes), result.Ingested)

	all, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	byOp := map[string]bool{}
	for _, r := range all {
		byOp[r.OperationID] = true
	}
	for _, op := range []string{
		"GetSyntheticFutureRoute", "GetSyntheticWidgetsWidgetId",
		"GetSyntheticScopeGrammar", "GetSyntheticCacheMode",
	} {
		require.Truef(t, byOp[op], "expected %s to be ingested", op)
	}

	// (c) the novel scope must appear in app.esi_scope, unrejected.
	scope, err := s.GetEsiScope(ctx, "esi::synthetic~widget/read@v3")
	require.NoError(t, err)
	require.Equal(t, "esi::synthetic~widget/read@v3", scope.Scope)

	// (d) the unrecognised cache mode must appear in app.open_vocabulary.
	unacked, err := s.ListUnacknowledgedOpenVocabulary(ctx, "cache_mode")
	require.NoError(t, err)
	found := false
	for _, v := range unacked {
		if v.Value == "quantum-entangled" {
			found = true
		}
	}
	require.True(t, found, "quantum-entangled must be recorded in app.open_vocabulary")
}

// TestBootSeedsPinAndIngestsLive exercises the full boot sequence end to
// end against the real ESI host (network access confirmed available in
// this environment) — pin seeding, live discovery, ingest — and confirms
// the pin is read but never advanced by Boot itself.
func TestBootSeedsPinAndIngestsLive(t *testing.T) {
	s := newMigratedStore(t)
	ctx := context.Background()

	result, err := catalogue.Boot(ctx, http.DefaultClient, s, "", time.Now())
	require.NoError(t, err)
	require.Equal(t, "live", result.Source)
	require.False(t, result.StaleSnapshot)
	require.Greater(t, result.Ingested, 100)
	require.Equal(t, catalogue.DefaultCompatibilityPin, catalogue.FormatDate(result.Pin),
		"first boot must seed the pin to the SRS default")

	pinAfter, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, catalogue.DefaultCompatibilityPin, catalogue.FormatDate(pinAfter),
		"Boot must never advance the pin itself")
}

// TestAdvancePinRecordsHistory — AdvancePin is the only path that changes
// the pin, and every advance is recorded in app.esi_pin_history.
//
// PHASE 18 ([v3.1 — B13]). Updated for AdvancePin's new signature and its
// new server-side D_max bound. Two changes, both of which the old form
// depended on the absence of:
//
//   - `now` is fixed and D_max is seeded, rather than the test advancing
//     to a hardcoded 2026-09-01 and relying on nothing checking it. That
//     date is in the FUTURE relative to the pin, so with the bound in
//     place it is correctly refused — and a test pinned to a wall-clock
//     date would in any case have started failing on its own once real
//     time passed it.
//   - The diff is computed by AdvancePin rather than passed in as nil.
func TestAdvancePinRecordsHistory(t *testing.T) {
	s := newMigratedStore(t)
	ctx := context.Background()

	initial, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, catalogue.DefaultCompatibilityPin, catalogue.FormatDate(initial))

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	dMax, err := catalogue.ParseDate("2026-09-01")
	require.NoError(t, err)
	require.NoError(t, catalogue.SetDMax(ctx, s, dMax))

	newPin, err := catalogue.ParseDate("2026-09-01")
	require.NoError(t, err)
	rec, _, err := catalogue.AdvancePin(ctx, s, newPin, "operator:test-admin", now)
	require.NoError(t, err)
	require.Equal(t, "operator:test-admin", rec.Actor)
	require.Equal(t, "2026-09-01", catalogue.FormatDate(rec.NewPin.Time))
	require.Equal(t, catalogue.DefaultCompatibilityPin, catalogue.FormatDate(rec.OldPin.Time))

	after, err := catalogue.GetPin(ctx, s)
	require.NoError(t, err)
	require.Equal(t, "2026-09-01", catalogue.FormatDate(after))

	history, err := s.ListEsiPinHistory(ctx, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
}

// TestIdentifierTypeChangeFailsLoudly — an identifier that changes type
// between two ingests (bigint -> uuid) fails loudly with a named error
// rather than coercing (04_RELEASE_GATES.md §6.3).
func TestIdentifierTypeChangeFailsLoudly(t *testing.T) {
	s := newMigratedStore(t)
	ctx := context.Background()
	pin, err := catalogue.ParseDate("2999-01-01")
	require.NoError(t, err)

	first := readFixtureFile(t, "../../../testdata/esi/openapi.minimal.json")
	routes1, err := catalogue.ParseSpec(first, pin)
	require.NoError(t, err)
	_, err = catalogue.Sync(ctx, s, routes1)
	require.NoError(t, err)

	before, err := s.GetEsiRouteByOperationID(ctx, "GetAlliancesAllianceId")
	require.NoError(t, err)
	require.JSONEq(t, `{"alliance_id":"bigint"}`, string(before.IdentifierTypes))

	// A second ingest where GetAlliancesAllianceId's alliance_id has
	// drifted from bigint to uuid — inline, and deliberately touching only
	// this one operation, so this test's drift is isolated from
	// openapi.synthetic-drift.json's separate retirement fixture (bundling
	// both into one ingest would abort on the type change before
	// retirement could be observed, per Sync's all-or-nothing guard).
	const driftedSpec = `{
		"openapi": "3.1.0",
		"info": {"title": "drift", "version": "1"},
		"paths": {
			"/alliances/{alliance_id}": {
				"get": {
					"operationId": "GetAlliancesAllianceId",
					"parameters": [
						{"in": "path", "name": "alliance_id", "required": true, "schema": {"type": "string", "format": "uuid"}}
					],
					"responses": {"200": {"description": "OK"}},
					"x-compatibility-date": "2020-01-01",
					"x-server-cache-mode": "ttl-based"
				}
			}
		}
	}`
	routes2, err := catalogue.ParseSpec([]byte(driftedSpec), pin)
	require.NoError(t, err)

	_, err = catalogue.Sync(ctx, s, routes2)
	require.Error(t, err)
	var typeErr *catalogue.IdentifierTypeChangedError
	require.ErrorAs(t, err, &typeErr, "expected an *IdentifierTypeChangedError, got %T: %v", err, err)
	require.Equal(t, "GetAlliancesAllianceId", typeErr.OperationID)
	require.Equal(t, "alliance_id", typeErr.Parameter)
	require.Equal(t, "bigint", typeErr.OldType)
	require.Equal(t, "uuid", typeErr.NewType)

	// The drifted route must not have been silently overwritten.
	after, err := s.GetEsiRouteByOperationID(ctx, "GetAlliancesAllianceId")
	require.NoError(t, err)
	require.JSONEq(t, `{"alliance_id":"bigint"}`, string(after.IdentifierTypes),
		"a rejected identifier-type change must never be committed")
}

// TestRetiredRouteMarkedNotDeleted — a route that vanishes from the spec
// is marked retired_at, never deleted; its subscriptions survive
// (04_RELEASE_GATES.md §6.3).
func TestRetiredRouteMarkedNotDeleted(t *testing.T) {
	s := newMigratedStore(t)
	ctx := context.Background()

	first := readFixtureFile(t, "../../../testdata/esi/openapi.minimal.json")
	pin, err := catalogue.ParseDate("2999-01-01") // far future: nothing blocked
	require.NoError(t, err)

	routes, err := catalogue.ParseSpec(first, pin)
	require.NoError(t, err)
	_, err = catalogue.Sync(ctx, s, routes)
	require.NoError(t, err)

	beforeAll, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	var marketsRouteID *string
	for _, r := range beforeAll {
		if r.OperationID == "GetMarketsPrices" {
			id := r.RouteID.String()
			marketsRouteID = &id
		}
	}
	require.NotNil(t, marketsRouteID, "GetMarketsPrices must exist after the first ingest")

	// Second ingest: openapi.synthetic-drift.json has removed /markets/prices.
	second := readFixtureFile(t, "../../../testdata/esi/openapi.synthetic-drift.json")
	routes2, err := catalogue.ParseSpec(second, pin)
	require.NoError(t, err)
	result, err := catalogue.Sync(ctx, s, routes2)
	require.NoError(t, err)
	require.Equal(t, 1, result.Retired, "GetMarketsPrices must be retired, and only it")

	afterAll, err := s.ListEsiRoutes(ctx)
	require.NoError(t, err)
	for _, r := range afterAll {
		require.NotEqual(t, "GetMarketsPrices", r.OperationID, "a retired route must not appear in ListEsiRoutes (retired_at IS NULL filter)")
	}
}
