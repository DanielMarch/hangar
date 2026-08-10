//go:build integration

package handlers_test

import (
	"context"
	"testing"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/stretchr/testify/require"
)

func seedStarbase(t *testing.T, s *store.Store, corporationID, starbaseID int64, systemID int32) {
	t.Helper()
	_, err := s.UpsertCorporationStarbase(context.Background(), gen.UpsertCorporationStarbaseParams{
		CorporationID: corporationID, StarbaseID: starbaseID, TypeID: 16213, SystemID: systemID,
	})
	require.NoError(t, err)
}

// TestStarbaseDetailPopulatesFuelBay (roadmap exit criterion):
// app.starbase_detail.fuels populates from a recorded fixture — the
// source Phase 14's fuel-low alert (corporation.starbase.fuel_low) reads.
func TestStarbaseDetailPopulatesFuelBay(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const corporationID, starbaseID int64 = 98000003, 1030000000001
	const systemID int32 = 30000142
	seedCorporation(t, s, corporationID)
	seedStarbase(t, s, corporationID, starbaseID, systemID)

	dto, err := handlers.ParseCorporationStarbaseDetail(mustReadCorpFixture(t, "starbase_detail.json"))
	require.NoError(t, err)
	require.NotEmpty(t, dto.Fuels, "precondition: the fixture must actually carry fuel line items")

	res, err := handlers.SyncCorporationStarbaseDetail(ctx, s, corporationID, starbaseID, systemID, dto)
	require.NoError(t, err)
	require.Equal(t, int32(1), res.RowsAffected)

	detail, err := s.GetStarbaseDetail(ctx, corporationID, starbaseID)
	require.NoError(t, err)
	require.NotEmpty(t, detail.Fuels, "app.starbase_detail.fuels must be populated from the recorded fixture")
	require.Contains(t, string(detail.Fuels), `"type_id"`)
	require.Contains(t, string(detail.Fuels), `"quantity"`)
}

func seedSkyhook(t *testing.T, s *store.Store, corporationID, skyhookID, planetID int64) {
	t.Helper()
	_, err := s.UpsertCorporationSkyhookStub(context.Background(), corporationID, skyhookID, &planetID)
	require.NoError(t, err)
}

func seedSovereigntyHub(t *testing.T, s *store.Store, corporationID, hubID int64, systemID int32) {
	t.Helper()
	_, err := s.UpsertCorporationSovereigntyHub(context.Background(), corporationID, hubID, systemID)
	require.NoError(t, err)
}

// TestSkyhookAndSovereigntyHubRoundTrip (roadmap exit criterion): both
// detail endpoints round-trip — Phase 8.1's reagent-not-fuel fix
// (00033_phase8_1_skyhook_reagent_fixup.sql) verified against a real
// database: reagents persist as jsonb, fuel_expires no longer exists on
// either table, and a re-sync of identical detail data changes no
// updated_at (the §3.5 IS DISTINCT FROM guard, same as every other Phase 8
// domain).
func TestSkyhookAndSovereigntyHubRoundTrip(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const corporationID int64 = 98000004
	seedCorporation(t, s, corporationID)

	t.Run("skyhook detail", func(t *testing.T) {
		const skyhookID, planetID int64 = 1040000000001, 40000123
		seedSkyhook(t, s, corporationID, skyhookID, planetID)

		dto, err := handlers.ParseCorporationSkyhookDetail(mustReadCorpFixture(t, "skyhook_detail.json"))
		require.NoError(t, err)
		require.NotEmpty(t, dto.Reagents, "precondition: the fixture must carry reagent line items")

		res, err := handlers.SyncCorporationSkyhookDetail(ctx, s, corporationID, skyhookID, dto)
		require.NoError(t, err)
		require.Equal(t, int32(1), res.RowsAffected)

		rows, err := s.ListCorporationSkyhooks(ctx, corporationID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.NotEmpty(t, rows[0].Reagents, "reagents must round-trip through the database")
		require.Contains(t, string(rows[0].Reagents), `"type_id"`)
		require.Nil(t, rows[0].TypeID, "the detail sync alone never resolves type_id — only the LIST sync's SDE backfill does (TestSkyhookAndSovereigntyHubBackfillFromSDE), and this test never calls it")
		first := rows[0].UpdatedAt

		_, err = handlers.SyncCorporationSkyhookDetail(ctx, s, corporationID, skyhookID, dto)
		require.NoError(t, err)
		rows2, err := s.ListCorporationSkyhooks(ctx, corporationID)
		require.NoError(t, err)
		require.Equal(t, first, rows2[0].UpdatedAt, "re-syncing identical skyhook detail must not change updated_at")
	})

	t.Run("sovereignty hub detail", func(t *testing.T) {
		const hubID int64 = 1050000000001
		const systemID int32 = 30000142
		seedSovereigntyHub(t, s, corporationID, hubID, systemID)

		dto, err := handlers.ParseCorporationSovereigntyHubDetail(mustReadCorpFixture(t, "sovereignty_hub_detail.json"))
		require.NoError(t, err)
		require.NotEmpty(t, dto.ReagentBay.Reagents, "precondition: the fixture must carry reagent line items")

		res, err := handlers.SyncCorporationSovereigntyHubDetail(ctx, s, corporationID, dto)
		require.NoError(t, err)
		require.Equal(t, int32(1), res.RowsAffected)

		rows, err := s.ListCorporationSovereigntyHubs(ctx, corporationID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.NotEmpty(t, rows[0].Reagents, "reagents must round-trip through the database")
		require.Contains(t, string(rows[0].Reagents), `"type_id"`)
		require.Nil(t, rows[0].TypeID, "the detail sync alone never resolves type_id — only the LIST sync's SDE backfill does (TestSkyhookAndSovereigntyHubBackfillFromSDE), and this test never calls it")
		first := rows[0].UpdatedAt

		_, err = handlers.SyncCorporationSovereigntyHubDetail(ctx, s, corporationID, dto)
		require.NoError(t, err)
		rows2, err := s.ListCorporationSovereigntyHubs(ctx, corporationID)
		require.NoError(t, err)
		require.Equal(t, first, rows2[0].UpdatedAt, "re-syncing identical sovereignty-hub detail must not change updated_at")
	})
}

// TestSkyhookAndSovereigntyHubBackfillFromSDE (closing the gap
// 00033_phase8_1_skyhook_reagent_fixup.sql's header left open, blocked on
// Phase 9's sde.planet/sde.type landing): once the sde schema carries a
// matching planet row and a type row named by CCP's real structure names,
// the LIST sync resolves corporation_skyhook.system_id/type_id and
// corporation_sovereignty_hub.type_id — never a hardcoded guess, and never
// an error before the sde data exists (verified separately below).
func TestSkyhookAndSovereigntyHubBackfillFromSDE(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const corporationID int64 = 98000005
	seedCorporation(t, s, corporationID)

	t.Run("backfill runs safely with no matching sde data yet (empty sde schema, never an error)", func(t *testing.T) {
		const skyhookID, planetID int64 = 1040000000010, 40000999
		_, err := handlers.SyncCorporationSkyhooks(ctx, s, corporationID, []handlers.CorporationSkyhookListEntryDTO{
			{ID: skyhookID, PlanetID: planetID},
		})
		require.NoError(t, err, "the backfill must be a safe no-op against an sde schema with no matching rows")

		rows, err := s.ListCorporationSkyhooks(ctx, corporationID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Nil(t, rows[0].SystemID)
		require.Nil(t, rows[0].TypeID)
	})

	t.Run("skyhook system_id and type_id resolve once sde.planet/sde.type are populated", func(t *testing.T) {
		const skyhookID, planetID int64 = 1040000000011, 40000124
		const solarSystemID int32 = 30000142
		const skyhookTypeID int32 = 81826

		_, err := pool.Exec(ctx, `INSERT INTO sde.solar_system (solar_system_id, constellation_id, region_id, name, data) VALUES ($1, 1, 1, 'Jita', '{}'::jsonb)`, solarSystemID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO sde.planet (planet_id, solar_system_id, name, data) VALUES ($1, $2, 'Jita I', '{}'::jsonb)`, planetID, solarSystemID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO sde.type (type_id, group_id, name, published, data) VALUES ($1, 1, 'Skyhook', true, '{}'::jsonb)`, skyhookTypeID)
		require.NoError(t, err)

		_, err = handlers.SyncCorporationSkyhooks(ctx, s, corporationID, []handlers.CorporationSkyhookListEntryDTO{
			{ID: skyhookID, PlanetID: planetID},
		})
		require.NoError(t, err)

		rows, err := s.ListCorporationSkyhooks(ctx, corporationID)
		require.NoError(t, err)
		var got gen.AppCorporationSkyhook
		for _, r := range rows {
			if r.SkyhookID == skyhookID {
				got = r
			}
		}
		require.NotNil(t, got.SystemID, "planet_id -> sde.planet -> solar_system_id must resolve system_id")
		require.Equal(t, solarSystemID, *got.SystemID)
		require.NotNil(t, got.TypeID, "type_id must resolve by name against sde.type, never a hardcoded guess")
		require.Equal(t, skyhookTypeID, *got.TypeID)
	})

	t.Run("sovereignty hub type_id resolves once sde.type is populated", func(t *testing.T) {
		const hubID int64 = 1050000000010
		const systemID int32 = 30000142
		const hubTypeID int32 = 81825

		_, err := pool.Exec(ctx, `INSERT INTO sde.type (type_id, group_id, name, published, data) VALUES ($1, 1, 'Sovereignty Hub', true, '{}'::jsonb)`, hubTypeID)
		require.NoError(t, err)

		_, err = handlers.SyncCorporationSovereigntyHubs(ctx, s, corporationID, []handlers.CorporationSovereigntyHubListEntryDTO{
			{ID: hubID, SolarSystemID: systemID},
		})
		require.NoError(t, err)

		rows, err := s.ListCorporationSovereigntyHubs(ctx, corporationID)
		require.NoError(t, err)
		var got gen.AppCorporationSovereigntyHub
		for _, r := range rows {
			if r.HubID == hubID {
				got = r
			}
		}
		require.NotNil(t, got.TypeID, "type_id must resolve by name against sde.type, never a hardcoded guess")
		require.Equal(t, hubTypeID, *got.TypeID)
	})
}
