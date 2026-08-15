package capability

import (
	"testing"

	"github.com/stretchr/testify/require"

	hangarsync "github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
)

const corp = hangarsync.EntityCorporation

// TestSyncCorporationAssets — Appendix A #18, and defect B47 itself.
//
// B47 was ONE MISSING MAP ENTRY. handlers.SyncAssets had been owner-generic
// since Phase 6 and ran for characters every day;
// GET /corporations/{corporation_id}/assets had no corporationDispatch
// entry, so it was in no dispatch table, so worker.SyncSet() never named it,
// so tools/scopedump never derived its scope, so
// esi-assets.read_corporation_assets.v1 never reached the developer-portal
// registration — and no corporation's assets had ever synced on any
// installation. The dispatch gap and the scope gap were the same defect
// observed at two layers.
//
// The row cited TestEveryCatalogedGetRouteIsClassified, which is a real and
// valuable test and is NOT this capability's: it asserts the partition is
// total, not that corporation assets in particular are delivered. This is
// the one that says so, and the assertion that would have failed before B47
// is the first line.
//
// Live state at the time of writing: 10 corporation-owned rows, the first
// real data B47's fix has produced.
func TestSyncCorporationAssets(t *testing.T) {
	requireDispatched(t, corp, "/corporations/{corporation_id}/assets")
	requireEndpoints(t, "/api/v1/corporations/{id}/assets")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/assets", "", handlers.AssetDTO{})

	// The names enrichment is a SECOND upstream call made inside the assets
	// pass, and its corporation half was deliberately left unwired in 20.5
	// because there was nothing to enrich. It must not be subscribable in
	// its own right: it is a POST with a request body, and a subscription
	// for it would be a polling schedule for a route that cannot be polled.
	requireNotSubscribable(t,
		"/characters/{character_id}/assets/names",
		"/corporations/{corporation_id}/assets/names")

	assets := parsed(t, "character/assets.json", handlers.ParseAssets)
	require.NotEmpty(t, assets, "one owner-generic parser serves both owners; this is the body shape both send")
	require.NotZero(t, assets[0].TypeID)
	require.NotEmpty(t, assets[0].LocationFlag,
		"location_flag is what separates a corporation's seven hangar divisions from each other")
}

// TestSyncCorporationContacts — Appendix A #20.
//
// The corporation contact schema declares NO is_blocked property at all —
// not "omitted when false", genuinely absent, because blocking is a personal
// action a corporation cannot take. handlers/corporation_social.go records
// that, and this asserts it stays true of the spec: if CCP ever adds the
// property, the DTO check below starts failing and the omission stops being
// correct.
func TestSyncCorporationContacts(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/contacts",
		"/corporations/{corporation_id}/contacts/labels")
	requireEndpoints(t, "/api/v1/corporations/{id}/contacts")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/contacts", "", handlers.CorporationContactDTO{})

	contacts := parsed(t, "corporation/contacts.json", handlers.ParseCorporationContacts)
	requireLen(t, contacts, 1, "corporation contacts")
	require.Equal(t, int64(90000005), contacts[0].ContactID)
	require.InDelta(t, -10.0, contacts[0].Standing, 0.0001)
	require.Equal(t, []int64{1}, contacts[0].LabelIDs)

	labels := parsed(t, "corporation/contact_labels.json", handlers.ParseCorporationContactLabels)
	require.NotEmpty(t, labels, "the label ids on a contact are meaningless without the label names")
}

// TestSyncCorporationDivisions — Appendix A #22, divisions and facilities.
//
// A division with no custom name is the case worth pinning: EVE shows the
// default ("2nd Hangar") and ESI omits the property entirely, so a
// non-pointer string would record every unnamed division as literally empty
// and the UI would render a blank tab. The recorded response has one of
// each.
func TestSyncCorporationDivisions(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/divisions",
		"/corporations/{corporation_id}/facilities")
	requireEndpoints(t, "/api/v1/corporations/{id}/divisions")

	div := parsed(t, "corporation/divisions.json", handlers.ParseCorporationDivisions)
	requireLen(t, div.Hangar, 2, "hangar divisions")
	requireLen(t, div.Wallet, 1, "wallet divisions")
	require.EqualValues(t, 1, div.Hangar[0].Division)
	require.NotNil(t, div.Hangar[0].Name)
	require.Equal(t, "Main Hangar", *div.Hangar[0].Name)
	require.Nil(t, div.Hangar[1].Name, "an unnamed division omits `name`; it must not decode as the empty string")
	require.Equal(t, "Master Wallet", *div.Wallet[0].Name)
}

// TestSyncCorporationMembers — Appendix A #24.
//
// The members route returns a BARE ARRAY of character ids with no wrapper
// object — the shape defect B49/B50 turned on for the projects route next
// door — so the parse is asserted against the recorded body rather than
// against a schema with properties to compare.
//
// It also feeds §6.3's acting-character election: app.corporation_member is
// the candidate pool internal/sync/election.go reads, so a corporation whose
// members never sync can elect nobody and every corporation-scoped route
// goes dark. That is why the four member routes are one capability.
func TestSyncCorporationMembers(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/members",
		"/corporations/{corporation_id}/membertracking",
		"/corporations/{corporation_id}/members/limit",
		"/corporations/{corporation_id}/members/titles")
	requireEndpoints(t, "/api/v1/corporations/{id}/members")

	members := parsed(t, "corporation/members.json", handlers.ParseCorporationMembers)
	require.Equal(t, []int64{90000001, 90000002, 90000003}, members,
		"a bare id array, in order — this is the elector's candidate pool")

	tracking := parsed(t, "corporation/membertracking.json", handlers.ParseCorporationMemberTracking)
	require.NotEmpty(t, tracking)
	require.NotZero(t, tracking[0].CharacterID)
}

// TestSyncCorporationProjects — Appendix A #25, and the capability that
// carried defects B49 and B50 one after the other.
//
// B49: the listing was parsed as a bare array and failed on the first real
// response, because the route returns an ENVELOPE {cursor, projects} —
// the only cursor-paginated route in HANGAR's scope. B50: once it parsed,
// the DETAIL DTO still matched no response ESI has ever sent.
//
// So this test asserts the envelope explicitly, the uuid identity (a project
// id is a uuid, not an integer — the reason fanoutDetailItem grew idText),
// and the real project the live installation holds.
func TestSyncCorporationProjects(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/projects",
		"/corporations/{corporation_id}/projects/{project_id}",
		"/corporations/{corporation_id}/projects/{project_id}/contributors",
		"/corporations/{corporation_id}/projects/{project_id}/contribution/{character_id}")
	requireEndpoints(t, "/api/v1/corporations/{id}/projects")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/projects", "projects", handlers.CorporationProjectDTO{})

	projects := parsed(t, "corporation/projects.json", handlers.ParseCorporationProjects)
	requireLen(t, projects, 1, "corporation projects")
	require.Equal(t, "d564ecab-38d0-49c1-8400-de57a05f9250", projects[0].ID.String(),
		"a project id is a uuid; coercing it to an integer is what fanoutDetailItem.idText exists to avoid")
	require.Equal(t, "This is a test project", projects[0].Name)
	require.Equal(t, "Active", projects[0].State)
	require.EqualValues(t, 1, projects[0].Progress.Desired)

	contributors := parsed(t, "corporation/project_contributors.json", handlers.ParseCorporationProjectContributors)
	require.NotNil(t, contributors, "an envelope that parses to nil is B49 again")
}

// TestSyncCorporationRoles — Appendix A #26, roles, role history and titles.
//
// EVE's roles are hierarchical and ESI reports them literally: a Director
// holds every role and the response never enumerates the subordinate ones.
// internal/sync/election.go's satisfiesRoles depends on reading the literal
// list AND on recognising Director (defect B46), so the recorded response
// deliberately contains a Director and asserting it is what keeps the
// fixture able to exercise that branch.
func TestSyncCorporationRoles(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/roles",
		"/corporations/{corporation_id}/roles/history",
		"/corporations/{corporation_id}/titles")
	requireEndpoints(t, "/api/v1/corporations/{id}/roles")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/roles", "", handlers.CorporationRolesDTO{})

	roles := parsed(t, "corporation/roles.json", handlers.ParseCorporationRoles)
	requireLen(t, roles, 1, "corporation roles")
	require.Equal(t, int64(90000001), roles[0].CharacterID)
	require.Contains(t, roles[0].Roles, hangarsync.RoleDirector,
		"a Director holds every other role implicitly; the elector's B46 branch needs this case to exist")
	require.Equal(t, []string{"Station_Manager"}, roles[0].RolesAtHQ,
		"roles_at_hq is a DIFFERENT grant from roles and must not be merged into it")
	require.Empty(t, roles[0].RolesAtBase)
}

// TestSyncCorporationStarbases — Appendix A #27.
//
// The starbase DETAIL route is the source §4.4 names for the
// corporation.starbase.fuel_low threshold alert, and it was one of defect
// B30's unreachable fan-outs: `case starbaseDetailPath` had never once been
// reached on any installation, so app.starbase_detail had no writer and the
// alert could never fire. requireDispatched on the detail path is the
// assertion that was false for five phases.
func TestSyncCorporationStarbases(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/starbases",
		"/corporations/{corporation_id}/starbases/{starbase_id}")
	requireEndpoints(t, "/api/v1/corporations/{id}/starbases")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/starbases", "", handlers.CorporationStarbaseDTO{})

	bases := parsed(t, "corporation/starbases.json", handlers.ParseCorporationStarbases)
	requireLen(t, bases, 1, "starbases")
	require.Equal(t, int64(1030000000001), bases[0].StarbaseID)
	require.EqualValues(t, 30000142, bases[0].SystemID)
	require.NotNil(t, bases[0].State)
	require.Equal(t, "online", *bases[0].State)

	detail := parsed(t, "corporation/starbase_detail.json", handlers.ParseCorporationStarbaseDetail)
	require.NotEmpty(t, detail.Fuels, "the fuel bay is the only thing the detail route adds, and the alert reads it")
}

// TestSyncCorporationStructures — Appendix A #28, Upwell structures plus the
// skyhook and sovereignty-hub families.
//
// fuel_expires is the field the corporation.structure.fuel_low threshold
// evaluates, and ESI OMITS it for an unfuelled structure. That distinction
// is load-bearing: alerting's TestStarbaseWithNoFuelDataIsNotAStarbaseWithNoFuel
// exists because "no fuel data" and "no fuel" must not be the same reading,
// and it is a zero time.Time here that carries it.
func TestSyncCorporationStructures(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/structures",
		"/corporations/{corporation_id}/structures/skyhooks",
		"/corporations/{corporation_id}/structures/skyhooks/{skyhook_id}",
		"/corporations/{corporation_id}/structures/sovereignty-hubs",
		"/corporations/{corporation_id}/structures/sovereignty-hubs/{sovereignty_hub_id}")
	requireEndpoints(t, "/api/v1/corporations/{id}/structures")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/structures", "", handlers.CorporationStructureDTO{})

	structures := parsed(t, "corporation/structures.json", handlers.ParseCorporationStructures)
	requireLen(t, structures, 1, "corporation structures")
	require.Equal(t, "shield_vulnerable", structures[0].State)
	require.False(t, structures[0].FuelExpires.IsZero(), "the recorded structure is fuelled")
	require.NotEmpty(t, structures[0].Services)
	require.Equal(t, "market", structures[0].Services[0].Name)

	// The structure ids these rows carry are one of the four sources
	// capability #41's resolution enumerates — see
	// db/queries/reference.sql's ListCharacterStructureIDs.
	require.Equal(t, int64(1029999999001), structures[0].StructureID)
}

// TestSyncMiningObservers — Appendix A #30, the corporation mining ledger.
//
// The observer LIST gives ids and nothing else; every actual mined-ore row
// comes from the per-observer records fan-out, which was another of B30's
// thirteen. observer_id is also the one place HANGAR meets a "structure id
// that is not in app.corporation_structure" — a rented refinery — which is
// why it is not one of capability #41's enumeration sources.
func TestSyncMiningObservers(t *testing.T) {
	requireDispatched(t, corp,
		"/corporation/{corporation_id}/mining/extractions",
		"/corporation/{corporation_id}/mining/observers",
		"/corporation/{corporation_id}/mining/observers/{observer_id}")
	requireEndpoints(t, "/api/v1/corporations/{id}/mining")

	observers := parsed(t, "corporation/mining_observers.json", handlers.ParseCorporationMiningObservers)
	requireLen(t, observers, 1, "mining observers")
	require.Equal(t, int64(1029999999002), observers[0].ObserverID)
	require.Equal(t, "structure", observers[0].ObserverType)

	records := parsed(t, "corporation/mining_observer_records.json", handlers.ParseCorporationMiningObserverRecords)
	require.NotEmpty(t, records, "the records fan-out is the only source of mined quantities")
	require.NotZero(t, records[0].Quantity)
}

// TestSyncCorporationCustomsOffices — Appendix A #31, customs offices and
// container logs.
//
// The tax rates are the substance: seven separate standing bands, all
// floats, all of which a naive DTO collapses. They are NOT money — they are
// rates — so Principle 9 does not apply and float64 is correct here; the
// assertion pins that the seven are read as seven distinct values rather
// than one.
func TestSyncCorporationCustomsOffices(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/customs_offices",
		"/corporations/{corporation_id}/containers/logs")
	requireEndpoints(t, "/api/v1/corporations/{id}/customs-offices")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/customs_offices", "", handlers.CorporationCustomsOfficeDTO{})

	offices := parsed(t, "corporation/customs_offices.json", handlers.ParseCorporationCustomsOffices)
	requireLen(t, offices, 1, "customs offices")
	require.Equal(t, int64(1042220401), offices[0].OfficeID)
	require.True(t, offices[0].AllowAllianceAccess)
	require.NotNil(t, offices[0].ExcellentStandingTaxRate)
	require.InDelta(t, 0.02, *offices[0].ExcellentStandingTaxRate, 0.0001)
	require.NotNil(t, offices[0].BadStandingTaxRate)
	require.InDelta(t, 0.5, *offices[0].BadStandingTaxRate, 0.0001,
		"the seven standing bands are seven distinct rates, not one")
}

// TestSyncCorporationMedals — Appendix A #32, medal definitions and issues.
//
// Two routes with two tables: a medal DEFINITION exists whether or not
// anyone holds it, and an ISSUE points at a definition. Syncing only the
// definitions would render a medal case nobody has been awarded.
func TestSyncCorporationMedals(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/medals",
		"/corporations/{corporation_id}/medals/issued")
	requireEndpoints(t, "/api/v1/corporations/{id}/medals")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/medals", "", handlers.CorporationMedalDTO{})

	medals := parsed(t, "corporation/medals.json", handlers.ParseCorporationMedals)
	requireLen(t, medals, 1, "corporation medals")
	require.Equal(t, int64(1), medals[0].MedalID)
	require.Equal(t, "Valor", medals[0].Title)
	require.Equal(t, "For valor", medals[0].Description)

	issued := parsed(t, "corporation/medals_issued.json", handlers.ParseCorporationMedalsIssued)
	require.NotEmpty(t, issued, "an issued medal is a different row from the medal itself")
}

// TestSyncCorporationSheet — Appendix A #33.
//
// alliance_id is the assertion that matters beyond this capability: it is
// the second of UpsertAllianceStub's two callers, and Phase 20.8's alliance
// subscription reconcile joins through app.corporation.alliance_id to find
// its candidate pool. A sheet sync that dropped it would leave capability
// #37 with nothing to poll on an installation that IS in an alliance, and
// the failure would look exactly like the empty table it is supposed to
// fill.
func TestSyncCorporationSheet(t *testing.T) {
	requireDispatched(t, corp,
		"/corporations/{corporation_id}",
		"/corporations/{corporation_id}/shareholders",
		"/corporations/{corporation_id}/standings",
		"/corporations/{corporation_id}/alliancehistory")
	requireEndpoints(t, "/api/v1/corporations/{id}")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}", "", handlers.CorporationSheetDTO{})

	sheet := parsed(t, "corporation/corporation_sheet.json", handlers.ParseCorporationSheet)
	require.Equal(t, "Test Corporation", sheet.Name)
	require.Equal(t, int64(180548812), sheet.CeoID, "ceo_id is what the elector's CEO branch reads (defect B46)")
	require.NotNil(t, sheet.AllianceID)
	require.Equal(t, int64(434243723), *sheet.AllianceID,
		"alliance_id is the join Phase 20.8's ReconcileAllianceSubscriptions makes")
	require.EqualValues(t, 25, sheet.MemberCount)
	require.Equal(t, int64(60003760), sheet.HomeStationID)
}

// TestSyncMarketOrders — Appendix A #34 (character) and #35 (corporation).
//
// One handler, both owners, and the corporation half additionally carries
// wallet_division — the field that says which of the seven corporate wallets
// an order belongs to and that a character order does not have.
//
// app.market_order holding zero rows on the development installation is NOT
// evidence of a missing writer: CEODude has no orders. That distinction is
// recorded in worker/unmapped.go and is why the two regional market routes
// are ReasonNoCapability rather than ReasonNotBuilt.
func TestSyncMarketOrders(t *testing.T) {
	requireDispatched(t, char,
		"/characters/{character_id}/orders",
		"/characters/{character_id}/orders/history")
	requireDispatched(t, corp,
		"/corporations/{corporation_id}/orders",
		"/corporations/{corporation_id}/orders/history")
	requireEndpoints(t, "/api/v1/characters/{id}/orders", "/api/v1/corporations/{id}/orders")
	requireDTOCoversSpec(t, "/characters/{character_id}/orders", "", handlers.MarketOrderDTO{})

	orders := parsed(t, "character/orders.json", handlers.ParseMarketOrders)
	requireLen(t, orders, 1, "character market orders")
	require.Equal(t, int64(3001), orders[0].OrderID)
	require.NotNil(t, orders[0].IsBuyOrder)
	require.False(t, *orders[0].IsBuyOrder)
	require.Equal(t, "12.5", orders[0].Price.String(), "an order price is exact ISK, never a float")
	require.Equal(t, int64(60003760), orders[0].LocationID)

	corpOrders := parsed(t, "corporation/market_orders.json", handlers.ParseMarketOrders)
	require.NotEmpty(t, corpOrders)
	require.NotZero(t, corpOrders[0].WalletDivision,
		"a corporation order names its wallet division; a character order has none")

	// DEFECT B53. The CHARACTER routes carry is_corporation and the
	// corporation ones do not, so the two sources must not be collapsed: a
	// director's corp-placed order appears in their personal list with the
	// flag true, and HANGAR passed a hard-coded false for every character
	// order on every installation. The recorded corporation body omits the
	// field entirely, which is what the endpoint fallback is for.
	require.Nil(t, corpOrders[0].IsCorporation,
		"the corporation order routes do not declare is_corporation; the caller's flag supplies it")
}

// TestSyncMarketPrices — Appendix A #36, EVE-wide adjusted and average
// prices.
//
// PHASE 20.5 (B30) gave this route its dispatch entry: it takes no
// parameter, needs no scope and needs no token, and it had been the
// longest-unreachable of the thirteen — GET /api/v1/markets/prices served an
// empty collection from Phase 15 onward because nothing ever wrote
// app.market_price.
//
// The two regional routes Appendix A also lists under #36 are deliberately
// NOT dispatched and are classified ReasonNoCapability: HANGAR's
// /api/v1/markets/{region_id}/{orders,types} are owner-scoped projections
// over app.market_order, not mirrors of ESI's public regional order book.
// Asserting that they are absent is how that decision stays a decision
// rather than becoming an oversight.
func TestSyncMarketPrices(t *testing.T) {
	requireDispatched(t, hangarsync.EntityGlobal, "/markets/prices", "/markets/{region_id}/history")
	requireEndpoints(t, "/api/v1/markets/prices")
	requireDTOCoversSpec(t, "/markets/prices", "", handlers.MarketPriceDTO{})
	requireDeliberatelyUnmapped(t, "/markets/{region_id}/orders", "/markets/{region_id}/types")
}

// TestSyncSovereignty — Appendix A #38, campaigns and system ownership.
//
// The systems route is the one ESI wraps in an envelope ({solar_systems:
// [...]}) while the campaigns route returns a bare array — the same
// inconsistency that produced B49 for projects. Parsing both from recorded
// bodies is what proves the two shapes are handled as two shapes.
func TestSyncSovereignty(t *testing.T) {
	requireDispatched(t, hangarsync.EntityGlobal, "/sovereignty/campaigns", "/sovereignty/systems")
	requireEndpoints(t, "/api/v1/sovereignty/campaigns")
	requireDTOCoversSpec(t, "/sovereignty/campaigns", "", handlers.SovereigntyCampaignDTO{})

	campaigns := parsed(t, "corporation/sovereignty_campaigns.json", handlers.ParseSovereigntyCampaigns)
	requireLen(t, campaigns, 1, "sovereignty campaigns")
	require.Equal(t, int64(1), campaigns[0].CampaignID)
	require.Equal(t, "ihub_defense", campaigns[0].EventType)
	require.NotEmpty(t, campaigns[0].Participants, "participant scores are a nested array, not a scalar")

	systems := parsed(t, "corporation/sovereignty_systems.json", handlers.ParseSovereigntySystems)
	requireLen(t, systems.SolarSystems, 1, "sovereignty systems")
	require.EqualValues(t, 30000142, systems.SolarSystems[0].SystemID)
	require.NotNil(t, systems.SolarSystems[0].AllianceID)
	require.Nil(t, systems.SolarSystems[0].FactionID, "an explicit null must decode as absent, not faction 0")
}
