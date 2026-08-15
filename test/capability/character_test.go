package capability

import (
	"testing"

	"github.com/stretchr/testify/require"

	hangarsync "github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
)

const char = hangarsync.EntityCharacter

// TestSyncAssets — Appendix A #1, Character Assets.
//
// The recorded fixture is the one that matters most here: it contains an
// asset whose location_type is "item", i.e. nested inside another asset. The
// asset TREE (app.asset_location's materialised root) is built by walking
// those, and a DTO that dropped location_type would flatten a ship's cargo
// into the station. The write and the recursive tree are covered against
// real Postgres by handlers' sync_idempotency_integration_test.go.
func TestSyncAssets(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/assets")
	requireEndpoints(t, "/api/v1/characters/{id}/assets", "/api/v1/characters/{id}/assets/tree")
	requireDTOCoversSpec(t, "/characters/{character_id}/assets", "", handlers.AssetDTO{})

	assets := parsed(t, "character/assets.json", handlers.ParseAssets)
	require.GreaterOrEqual(t, len(assets), 3)
	require.Equal(t, int64(1000000000001), assets[0].ItemID)
	require.Equal(t, "station", assets[0].LocationType)
	require.Equal(t, int64(60003760), assets[0].LocationID)
	require.True(t, assets[0].IsSingleton)

	var nested *handlers.AssetDTO
	for i := range assets {
		if assets[i].LocationType == "item" {
			nested = &assets[i]
			break
		}
	}
	require.NotNil(t, nested, "the recorded response contains a container-nested asset; the tree depends on it")
	require.Equal(t, assets[0].ItemID, nested.LocationID,
		"the nested asset's location_id must be its parent's item_id — this is what app.asset_location walks")
}

// TestSyncBlueprints — Appendix A #2 (character) and #19 (corporation).
//
// One test for both because one handler serves both: handlers.SyncBlueprints
// takes an owner kind, and the capability that was missing for years was the
// corporation DISPATCH ENTRY, not a second handler. Asserting both routes
// here is what makes that concrete.
//
// The value assertion is on runs = -1 and quantity = -1, ESI's sentinels for
// "this is an original, not a copy". A DTO that typed either as unsigned, or
// a schema that stored them as counts, would turn a BPO into a stack of -1
// copies — and the pair is the only thing distinguishing the two.
func TestSyncBlueprints(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/blueprints")
	requireDispatched(t, hangarsync.EntityCorporation, "/corporations/{corporation_id}/blueprints")
	requireEndpoints(t, "/api/v1/characters/{id}/blueprints", "/api/v1/corporations/{id}/blueprints")
	requireDTOCoversSpec(t, "/characters/{character_id}/blueprints", "", handlers.BlueprintDTO{})

	bps := parsed(t, "corporation/blueprints.json", handlers.ParseBlueprints)
	requireLen(t, bps, 1, "corporation blueprints")
	require.Equal(t, int64(1000000000010), bps[0].ItemID)
	require.EqualValues(t, 10, bps[0].MaterialEfficiency)
	require.EqualValues(t, 20, bps[0].TimeEfficiency)
	require.EqualValues(t, -1, bps[0].Runs, "-1 is ESI's original-blueprint sentinel, not a count")
	require.EqualValues(t, -1, bps[0].Quantity, "-1 is ESI's original-blueprint sentinel, not a count")
}

// TestSyncCalendar — Appendix A #3, Calendar.
//
// Three routes, and the two detail ones are why this capability is worth a
// test of its own: they were among defect B30's thirteen unreachable
// handlers — written, tested and impossible to schedule, because no
// subscription could name a fan-out path. requireDispatched is the assertion
// that would have failed then and must never pass falsely again.
func TestSyncCalendar(t *testing.T) {
	requireDispatched(t, char,
		"/characters/{character_id}/calendar",
		"/characters/{character_id}/calendar/{event_id}",
		"/characters/{character_id}/calendar/{event_id}/attendees")
	requireEndpoints(t, "/api/v1/characters/{id}/calendar")
	requireDTOCoversSpec(t, "/characters/{character_id}/calendar", "", handlers.CalendarEventDTO{})
	requireDTOCoversSpec(t, "/characters/{character_id}/calendar/{event_id}/attendees", "", handlers.CalendarAttendeeDTO{})

	// The DETAIL body repeats five fields the LIST route has already landed
	// in app.calendar_event — and event_id is supplied by the fan-out from
	// the request path, never read back out of the body (the same rule
	// SyncStation and SyncKillmail follow). app.calendar_event_detail holds
	// only what the list cannot give: the body text, the duration and the
	// owner. Excusing them here rather than widening the DTO keeps the detail
	// sync from being a second, competing writer of the same five columns.
	requireDTOCoversSpec(t, "/characters/{character_id}/calendar/{event_id}", "", handlers.CalendarEventDetailDTO{},
		"event_id", "date", "title", "importance", "response")
}

// TestSyncCharacterClones — Appendix A #4, Clones and implants.
//
// The clone response is the one place in the character domain where a
// location carries CCP's own station/structure discriminator, and Phase
// 20.8's location resolution reads it (db/queries/reference.sql). The
// fixture holds one of each, which is exactly the pair that would break if
// location_type were ever dropped from the DTO.
func TestSyncCharacterClones(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/clones", "/characters/{character_id}/implants")
	requireEndpoints(t, "/api/v1/characters/{id}/clones")
	requireDTOCoversSpec(t, "/characters/{character_id}/clones", "", handlers.CharacterClonesDTO{})

	clones := parsed(t, "character/clones.json", handlers.ParseCharacterClones)
	require.Equal(t, "station", clones.HomeLocation.LocationType)
	require.Equal(t, int64(60003760), clones.HomeLocation.LocationID)
	require.GreaterOrEqual(t, len(clones.JumpClones), 2)
	require.Equal(t, int64(12345), clones.JumpClones[0].JumpCloneID)
	require.Equal(t, []int64{19540, 19551}, clones.JumpClones[0].Implants)
	require.Equal(t, "structure", clones.JumpClones[1].LocationType,
		"a jump clone in an Upwell structure is what capability #41's structure enumeration reads")
	require.Empty(t, clones.JumpClones[1].Implants, "an empty implant set is data, not an absent field")
}

// TestSyncCharacterContacts — Appendix A #5, Character contacts and labels.
//
// is_blocked and is_watched are the assertion that carries weight. ESI omits
// them when false rather than sending false, so a non-pointer bool would
// make "not blocked" and "field absent" the same value — and the second
// contact in the recorded response omits both, which is what proves the DTO
// distinguishes them.
func TestSyncCharacterContacts(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/contacts", "/characters/{character_id}/contacts/labels")
	requireEndpoints(t, "/api/v1/characters/{id}/contacts")
	requireDTOCoversSpec(t, "/characters/{character_id}/contacts", "", handlers.CharacterContactDTO{})

	contacts := parsed(t, "character/contacts.json", handlers.ParseCharacterContacts)
	requireLen(t, contacts, 2, "character contacts")
	require.Equal(t, int64(2112625428), contacts[0].ContactID)
	require.Equal(t, "character", contacts[0].ContactType)
	require.InDelta(t, 10.0, contacts[0].Standing, 0.0001)
	require.Equal(t, []int64{1, 2}, contacts[0].LabelIDs)
	require.NotNil(t, contacts[0].IsWatched)
	require.True(t, *contacts[0].IsWatched)
	require.Nil(t, contacts[1].IsWatched, "ESI omits is_watched when false; absent must not decode as false")
	require.InDelta(t, -5.0, contacts[1].Standing, 0.0001)
}

// TestSyncContracts — Appendix A #6 (character) and #21 (corporation).
//
// Both owners, and both detail fan-outs for each, in one test: the handlers
// have been owner-generic since Phase 9 and what was missing was dispatch —
// the CHARACTER items/bids entries were defect B47's second half, absent for
// six phases while the corporation twins worked.
//
// The value assertion is on price, which is ISK. Principle 9 forbids float64
// on a money path, so a contract's price must not be a float in anything
// that reaches the database; the DTO's own type is asserted by
// handlers/exact_money_test.go and the column by check-money.
func TestSyncContracts(t *testing.T) {
	requireDispatched(t, char,
		"/characters/{character_id}/contracts",
		"/characters/{character_id}/contracts/{contract_id}/items",
		"/characters/{character_id}/contracts/{contract_id}/bids")
	requireDispatched(t, hangarsync.EntityCorporation,
		"/corporations/{corporation_id}/contracts",
		"/corporations/{corporation_id}/contracts/{contract_id}/items",
		"/corporations/{corporation_id}/contracts/{contract_id}/bids")
	requireEndpoints(t, "/api/v1/characters/{id}/contracts", "/api/v1/corporations/{id}/contracts")
	requireDTOCoversSpec(t, "/characters/{character_id}/contracts", "", handlers.ContractDTO{})

	contracts := parsed(t, "character/contracts.json", handlers.ParseContracts)
	require.GreaterOrEqual(t, len(contracts), 1)
	require.Equal(t, int64(5001), contracts[0].ContractID)
	require.Equal(t, "item_exchange", contracts[0].Type)
	require.Equal(t, "outstanding", contracts[0].Status)
	require.False(t, contracts[0].ForCorporation)
	require.True(t, contracts[0].Price.Valid, "the recorded contract carries a price")
	require.Equal(t, "1000", contracts[0].Price.Decimal.StringFixed(0), "contract price is exact ISK, never a float")

	// A courier contract's item list comes back empty and that is a
	// legitimate result, not a failed fetch — the fan-out treats it as data.
	courier := parsed(t, "character/contract_items_courier.json", handlers.ParseContractItems)
	require.Empty(t, courier, "a courier contract has no items; empty must parse, not error")
}

// TestSyncCharacterFatigue — Appendix A #7, Jump fatigue.
//
// A single-object route whose three fields are all timestamps, and the
// capability is entirely about them being read as instants rather than
// strings: jump_fatigue_expire_date in the future is what "this character
// cannot jump yet" means.
func TestSyncCharacterFatigue(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/fatigue")
	requireEndpoints(t, "/api/v1/characters/{id}")

	f := parsed(t, "character/fatigue.json", handlers.ParseCharacterFatigue)
	require.Equal(t, 2026, f.JumpFatigueExpireDate.Year())
	require.Equal(t, 18, f.JumpFatigueExpireDate.Hour(), "the expiry is 18:00Z in the recorded response")
	require.True(t, f.JumpFatigueExpireDate.After(f.LastJumpDate),
		"fatigue expires after the jump that caused it; if these decode equal the timestamps are being dropped")
}

// TestSyncCharacterFittings — Appendix A #8, Fittings and the EFT export.
//
// PHASE 20.7 (B48) wired this capability's sync; PHASE 20.8 is what gives it
// a test, and the row previously read "(none automated yet — live-verified
// in 20.7; see defect B51)".
//
// #8 had TWO gaps behind one another and the second is the one worth
// pinning: after the writer landed, the SPA had no fittings screen and no
// route, so the data was invisible in the app. Hence the endpoint assertion
// covers all three surfaces including the EFT export, whose path the
// traceability matrix had spelled with a {fitting_id} parameter the router
// does not use (defect B52).
func TestSyncCharacterFittings(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/fittings")
	requireEndpoints(t,
		"/api/v1/characters/{id}/fittings",
		"/api/v1/characters/{id}/fittings/{sub_id}/eft")
	requireDTOCoversSpec(t, "/characters/{character_id}/fittings", "", handlers.FittingDTO{})
}

// TestSyncIndustryJobs — Appendix A #9 (character industry + mining) and #23
// (corporation industry).
//
// cost is ISK and is asserted exact for the same reason contracts' price is.
// The character MINING route rides in #9 because Appendix A's band lists it
// there, and it is a separate upstream route with a separate table.
func TestSyncIndustryJobs(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/industry/jobs", "/characters/{character_id}/mining")
	requireDispatched(t, hangarsync.EntityCorporation, "/corporations/{corporation_id}/industry/jobs")
	requireEndpoints(t, "/api/v1/characters/{id}/industry", "/api/v1/corporations/{id}/industry")
	requireDTOCoversSpec(t, "/corporations/{corporation_id}/industry/jobs", "", handlers.IndustryJobDTO{})

	jobs := parsed(t, "corporation/industry_jobs.json", handlers.ParseIndustryJobs)
	require.GreaterOrEqual(t, len(jobs), 1)
	require.EqualValues(t, 1, jobs[0].ActivityID)
	require.Equal(t, int64(1000000000010), jobs[0].BlueprintID)
	require.True(t, jobs[0].Cost.Valid, "the recorded job carries a cost")
	require.Equal(t, "15000", jobs[0].Cost.Decimal.StringFixed(0), "industry cost is exact ISK, never a float")
}

// TestSyncPlanetColonies — Appendix A #12, Planetary interaction.
//
// The colony LIST reports only a pin COUNT; the whole layout — pins, links,
// routes — arrives only from the per-planet detail route, which was one of
// B30's unreachable fan-outs. So the dispatch assertion on the DETAIL path
// is the substance of this test, not a formality.
func TestSyncPlanetColonies(t *testing.T) {
	requireDispatched(t, char, "/characters/{character_id}/planets", "/characters/{character_id}/planets/{planet_id}")
	requireEndpoints(t, "/api/v1/characters/{id}/planets")
	requireDTOCoversSpec(t, "/characters/{character_id}/planets", "", handlers.PlanetColonyDTO{})

	detail := parsed(t, "character/planet_colony_detail.json", handlers.ParsePlanetColonyDetail)
	require.NotEmpty(t, detail.Pins, "the detail route exists precisely to deliver the pins the list route only counts")
}

// TestSyncCharacterSkills — Appendix A #13, Skills, queue and attributes.
//
// total_sp and unallocated_sp are asserted because they are the two numbers
// every skills screen leads with and neither appears in the per-skill array
// — a DTO that modelled only the array would render a character with no
// skill points at all.
func TestSyncCharacterSkills(t *testing.T) {
	requireDispatched(t, char,
		"/characters/{character_id}/skills",
		"/characters/{character_id}/skillqueue",
		"/characters/{character_id}/attributes")
	requireEndpoints(t, "/api/v1/characters/{id}/skills")
	requireDTOCoversSpec(t, "/characters/{character_id}/skills", "", handlers.CharacterSkillsDTO{})

	skills := parsed(t, "character/skills.json", handlers.ParseCharacterSkills)
	require.Equal(t, int64(45280450), skills.TotalSP)
	require.Equal(t, int64(60000), skills.UnallocatedSP)
	requireLen(t, skills.Skills, 2, "skills")
	require.EqualValues(t, 3300, skills.Skills[0].SkillID)
	require.EqualValues(t, 5, skills.Skills[0].TrainedSkillLevel)
	require.Equal(t, int64(1280000), skills.Skills[0].SkillpointsInSkill)
}

// TestSyncCharacterSheet — Appendix A #15, the character sheet and its seven
// satellite routes.
//
// corporation_id is the load-bearing field across the whole installation:
// db/queries/sync_subscription.sql's corporation reconcile cannot produce a
// row until this route has filled app.character.corporation_id, and the
// alliance reconcile added in 20.8 is one step further downstream again. A
// sheet sync that dropped it would leave every corporation- and
// alliance-scoped subscription uncreated, silently.
func TestSyncCharacterSheet(t *testing.T) {
	requireDispatched(t, char,
		"/characters/{character_id}",
		"/characters/{character_id}/corporationhistory",
		"/characters/{character_id}/medals",
		"/characters/{character_id}/standings",
		"/characters/{character_id}/titles",
		"/characters/{character_id}/roles",
		"/characters/{character_id}/loyalty/points",
		"/characters/{character_id}/agents_research")
	requireEndpoints(t, "/api/v1/characters/{id}")
	requireDTOCoversSpec(t, "/characters/{character_id}", "", handlers.CharacterSheetDTO{})

	sheet := parsed(t, "character/character_sheet.json", handlers.ParseCharacterSheet)
	require.Equal(t, "Test Character", sheet.Name)
	require.Equal(t, int64(98777771), sheet.CorporationID,
		"corporation_id is what the corporation subscription reconcile waits for")
	require.NotNil(t, sheet.AllianceID)
	require.Equal(t, int64(99005338), *sheet.AllianceID,
		"alliance_id is what UpsertAllianceStub keys on, and what capability #37 then resolves a name for")
	require.Nil(t, sheet.FactionID, "an explicit JSON null must decode as absent, not as faction 0")
}

// TestSyncCharacterLocation — Appendix A #16, Location, online and ship.
//
// station_id and structure_id are SEPARATE nullable columns because CCP
// sends exactly one of them, and that split is the third unambiguous source
// capability #41's resolution enumerates (db/queries/reference.sql). A DTO
// that collapsed them into one "location" would make the station/structure
// distinction — and therefore which of the two ESI routes to call — a guess.
func TestSyncCharacterLocation(t *testing.T) {
	requireDispatched(t, char,
		"/characters/{character_id}/location",
		"/characters/{character_id}/online",
		"/characters/{character_id}/ship")
	requireEndpoints(t, "/api/v1/characters/{id}")
	requireDTOCoversSpec(t, "/characters/{character_id}/location", "", handlers.CharacterLocationDTO{})

	loc := parsed(t, "character/location.json", handlers.ParseCharacterLocation)
	require.Equal(t, int64(30000142), loc.SolarSystemID)
	require.NotNil(t, loc.StationID)
	require.Equal(t, int64(60003760), *loc.StationID)
	require.Nil(t, loc.StructureID, "docked in an NPC station means structure_id is absent, not zero")

	// A character who has never logged in since ESI added the field omits
	// last_login/last_logout entirely; the second fixture exists for it.
	never := parsed(t, "character/online_never_logged_in.json", handlers.ParseCharacterOnline)
	require.True(t, never.LastLogin.IsZero(), "an absent last_login must stay zero, not become the epoch")

	ship := parsed(t, "character/ship.json", handlers.ParseCharacterShip)
	require.NotZero(t, ship.ShipTypeID)
	require.NotZero(t, ship.ShipItemID)
}
