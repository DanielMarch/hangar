package capability

import (
	"testing"

	"github.com/stretchr/testify/require"

	hangarsync "github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/internal/sync/worker"
)

const global = hangarsync.EntityGlobal

// TestSyncAlliance — Appendix A #37, and the last of B48's nine unreachable
// capabilities to be closed.
//
// The row previously read "(none — no route dispatched; blocked, see
// unmapped.go)", which was honest and stayed true for two phases: an
// alliance has no token, so its contact routes need an alliance-scoped
// acting-character election, and DispatchWorker routed character,
// corporation and global only. Phase 20.8 built the fourth worker.
//
// ── WHAT THIS TEST CAN AND CANNOT SAY ────────────────────────────────────
// app.alliance holds ZERO rows on the development installation — HANGAR
// Corp is in no alliance — so ReconcileAllianceSubscriptions produces
// nothing and AllianceWorker has never been dispatched against real ESI.
// This test therefore verifies the delivery chain (dispatch, DTOs against
// the spec, endpoints) and NOT a live fetch. The SQL and the worker's
// routing are exercised against real Postgres by the integration companion;
// a live verification needs an operator to put a tracked character into an
// alliance, which is not something a test can arrange.
func TestSyncAlliance(t *testing.T) {
	requireDispatched(t, hangarsync.EntityAlliance,
		"/alliances/{alliance_id}",
		"/alliances/{alliance_id}/contacts",
		"/alliances/{alliance_id}/contacts/labels",
		"/alliances/{alliance_id}/corporations")
	requireEndpoints(t, "/api/v1/alliances/{id}")
	requireDTOCoversSpec(t, "/alliances/{alliance_id}", "", handlers.AllianceSheetDTO{})
	requireDTOCoversSpec(t, "/alliances/{alliance_id}/contacts", "", handlers.AllianceContactDTO{})
	requireDTOCoversSpec(t, "/alliances/{alliance_id}/contacts/labels", "", handlers.AllianceContactLabelDTO{})

	// The global /alliances id list stays deliberately unmapped: it returns
	// every alliance in New Eden, and HANGAR resolves the alliances it
	// REFERENCES rather than maintaining a directory of entities it has no
	// relationship with.
	requireDeliberatelyUnmapped(t, "/alliances")

	// The member-corporation route is a bare id array with no wrapper.
	ids, err := handlers.ParseAllianceCorporations([]byte(`[98000001, 98000002]`))
	require.NoError(t, err)
	require.Equal(t, []int64{98000001, 98000002}, ids)

	// The alliance sheet is the write that gives an alliance a NAME —
	// app.alliance's only previous writer was UpsertAllianceStub, which
	// inserts an id and an empty string, so GET /api/v1/alliances served a
	// list of blanks on every installation ever deployed.
	sheet, err := handlers.ParseAllianceSheet([]byte(`{
		"name": "Test Alliance", "ticker": "TEST", "creator_id": 90000001,
		"creator_corporation_id": 98000001, "executor_corporation_id": 98000002,
		"date_founded": "2010-05-01T12:00:00Z"
	}`))
	require.NoError(t, err)
	require.Equal(t, "Test Alliance", sheet.Name)
	require.Equal(t, "TEST", sheet.Ticker)
	require.NotNil(t, sheet.ExecutorCorporationID)
	require.Equal(t, int64(98000002), *sheet.ExecutorCorporationID)
	require.Equal(t, 2010, sheet.DateFounded.Year())
	require.Nil(t, sheet.FactionID, "an alliance not enlisted in factional warfare omits faction_id")
}

// TestSyncKillmails — Appendix A #39.
//
// PHASE 20.7 (B48) wired the two-stage fan-out; the row read "(none
// automated yet — live-verified in 20.7; see defect B51)" until now.
//
// The structural fact worth pinning is the one that looks like an omission
// and is not: the DETAIL route has NO subscription and must not have one.
// app.killmail requires killmail_time NOT NULL and PARTITIONS on it, so no
// row can exist before its detail has been fetched, so a detail
// subscription would enumerate an empty set forever. It is classified
// ReasonFetchedByParent instead — a handler exists and runs, inside the
// recent-list route's own pass.
func TestSyncKillmails(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/killmails/recent")
	requireDispatched(t, corp, "/corporations/{corporation_id}/killmails/recent")
	requireEndpoints(t, "/api/v1/characters/{id}/killmails", "/api/v1/corporations/{id}/killmails")
	requireDTOCoversSpec(t, "/killmails/{killmail_id}/{killmail_hash}", "", handlers.KillmailDetailDTO{})

	reason, ok := worker.DeliberatelyUnmapped()["/killmails/{killmail_id}/{killmail_hash}"]
	require.True(t, ok, "the killmail detail route must be classified, not merely absent")
	require.Equal(t, worker.ReasonFetchedByParent, reason,
		"the detail route is fetched inside the recent list's pass; it cannot have a subscription because "+
			"app.killmail partitions on a NOT NULL killmail_time and no stub row can exist")

	refs, err := handlers.ParseKillmailRefs([]byte(`[{"killmail_id": 42, "killmail_hash": "abc123"}]`))
	require.NoError(t, err)
	requireLen(t, refs, 1, "killmail refs")
	require.Equal(t, int64(42), refs[0].KillmailID)
	require.Equal(t, "abc123", refs[0].KillmailHash,
		"the hash is a string and rides in extraPathParams; it is not an id the engine can format")

	// A zero row count on the development installation is the UPSTREAM's:
	// the cached body for CEODude's recent window is `[]`, two bytes. An
	// empty list must parse to an empty slice, not an error.
	empty, err := handlers.ParseKillmailRefs([]byte(`[]`))
	require.NoError(t, err)
	require.Empty(t, empty)
}

// TestSyncLocationResolution — Appendix A #41, structure and station
// resolution, built in Phase 20.8.
//
// The row previously read "(none — no route dispatched; blocked, see
// unmapped.go)". What was missing was never the handler — it is thirty lines
// — but the ENUMERATION: neither route can be listed, so the id set has to
// come from HANGAR's own already-synced rows, and no query produced one.
// app.location therefore had no writer and GET
// /api/v1/support/universe/{structures,stations} 404'd on every id.
//
// The two halves are asserted as DIFFERENT entity kinds on purpose. A
// station is unauthenticated, so one global pass resolves it for the whole
// installation; a structure needs esi-universe.read_structures.v1 AND
// per-character docking access, so it is character-scoped and enumerates
// only the structures that character's own rows already sit in.
// TestGlobalRoutesRequireNoScope (internal/sync/worker) holds the invariant
// that makes the first of those safe.
func TestSyncLocationResolution(t *testing.T) {
	requireDispatched(t, global, "/universe/stations/{station_id}")
	requireDispatched(t, char, "/universe/structures/{structure_id}")
	requireEndpoints(t,
		"/api/v1/support/resolve",
		"/api/v1/support/universe/structures",
		"/api/v1/support/universe/stations")
	requireDTOCoversSpec(t, "/universe/stations/{station_id}", "", handlers.StationDTO{},
		// app.location is a four-column resolution table: name, system,
		// type, owner. A station's FACILITIES — its reprocessing rates,
		// office rental, service list and position — are SDE data with
		// nowhere to land here, and CCP publishes them as a download rather
		// than expecting them to be polled per station.
		"position", "reprocessing_efficiency", "reprocessing_stations_take",
		"max_dockable_ship_volume", "office_rental_cost", "services")
	requireDTOCoversSpec(t, "/universe/structures/{structure_id}", "", handlers.StructureDTO{})

	station, err := handlers.ParseStation([]byte(`{
		"station_id": 60015230, "name": "Jita IV - Moon 4", "system_id": 30000142,
		"type_id": 52678, "owner": 1000035, "position": {"x": 1.0, "y": 2.0, "z": 3.0},
		"reprocessing_efficiency": 0.5, "reprocessing_stations_take": 0.05,
		"max_dockable_ship_volume": 50000000.0, "office_rental_cost": 10000.0, "services": ["market"]
	}`))
	require.NoError(t, err)
	require.Equal(t, "Jita IV - Moon 4", station.Name)
	require.EqualValues(t, 30000142, station.SystemID)
	require.NotNil(t, station.Owner)

	// A structure a character can SEE but cannot DOCK at returns only the
	// three required fields. That is data, not a short read, and is why
	// app.location.type_id must be allowed to stay NULL.
	structure, err := handlers.ParseStructure([]byte(`{
		"name": "V-3YG7 VI - The Capital", "solar_system_id": 30000142, "owner_id": 98000001
	}`))
	require.NoError(t, err)
	require.Equal(t, "V-3YG7 VI - The Capital", structure.Name)
	require.EqualValues(t, 30000142, structure.SolarSystemID,
		"the structure route names it solar_system_id where the station route says system_id; "+
			"app.location has one column for both")
	require.Nil(t, structure.TypeID, "no docking access means no type_id — the row lands with a NULL, not a zero")
}

// TestSyncInsurancePrices — Appendix A #42.
//
// PHASE 20.7 (B48) gave this its global dispatch entry; the row read "(none
// automated yet — live-verified in 20.7; see defect B51)" until now. It is
// the one B48 capability guaranteed to land a non-empty result on any
// installation — EVE publishes insurance levels for every insurable hull —
// and it landed 3,414 rows live.
//
// RowsAffected counts LEVELS rather than types, which is the assertion
// below: reporting the type count would under-report the write by roughly
// the number of tiers per hull.
func TestSyncInsurancePrices(t *testing.T) {
	requireDispatched(t, global, "/insurance/prices")
	requireEndpoints(t, "/api/v1/tools/insurance")
	requireDTOCoversSpec(t, "/insurance/prices", "", handlers.InsurancePriceDTO{})

	prices, err := handlers.ParseInsurancePrices([]byte(`[
		{"type_id": 587, "levels": [
			{"name": "Basic", "cost": 10.0, "payout": 100.0},
			{"name": "Standard", "cost": 20.0, "payout": 200.0}
		]}
	]`))
	require.NoError(t, err)
	requireLen(t, prices, 1, "insurance prices")
	requireLen(t, prices[0].Levels, 2, "insurance levels")
	require.Equal(t, "Basic", prices[0].Levels[0].Name,
		"the tier name is CCP's own open vocabulary, which is why app.insurance_price.level is text")
	require.EqualValues(t, 587, prices[0].TypeID)
}

// TestSyncEsiStatus — Appendix A #45, ESI's own per-route service health.
//
// PHASE 20.7 (B48) wired the global dispatch entry AND replaced the
// endpoint's hard-coded `"healthy": true` with a value derived from CCP's
// own per-route statuses — a field that could never say no. The row read
// "(none automated yet — live-verified in 20.7; see defect B51)" until now.
//
// The two status endpoints are never conflated: this is ESI's health, and
// /meta/server-status is Tranquility's. Asserting both routes and both
// endpoints separately is what keeps them apart.
func TestSyncEsiStatus(t *testing.T) {
	requireDispatched(t, global, "/meta/status")
	requireEndpoints(t, "/api/v1/meta/esi-status")

	status, err := handlers.ParseEsiStatus([]byte(`{"routes": [
		{"method": "get", "path": "/characters/{character_id}/assets", "status": "green"},
		{"method": "get", "path": "/markets/prices", "status": "red"}
	]}`))
	require.NoError(t, err)
	requireLen(t, status.Routes, 2, "esi route statuses")
	require.Equal(t, "red", status.Routes[1].Status,
		"a non-green route is what makes the endpoint's health field able to say no")
}

// TestSyncServerStatus — Appendix A #46, Tranquility's own status.
//
// vip is the field to pin: ESI omits it entirely outside a VIP-mode
// downtime, so a plain bool would report "not in VIP mode" and "ESI did not
// say" as the same value — and VIP mode is precisely when an operator needs
// to know why nothing is syncing.
func TestSyncServerStatus(t *testing.T) {
	requireDispatched(t, global, "/status")
	requireEndpoints(t, "/api/v1/meta/server-status")
	requireDTOCoversSpec(t, "/status", "", handlers.ServerStatusDTO{})

	normal, err := handlers.ParseServerStatus([]byte(`{
		"players": 24291, "server_version": "2795641", "start_time": "2026-08-15T11:00:00Z"
	}`))
	require.NoError(t, err)
	require.EqualValues(t, 24291, normal.Players)
	require.Equal(t, "2795641", normal.ServerVersion)
	require.Nil(t, normal.VIP, "vip is omitted outside VIP mode; absent must not decode as false")

	vip, err := handlers.ParseServerStatus([]byte(`{
		"players": 12, "server_version": "2795641", "start_time": "2026-08-15T11:00:00Z", "vip": true
	}`))
	require.NoError(t, err)
	require.NotNil(t, vip.VIP)
	require.True(t, *vip.VIP)
}
