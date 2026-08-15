package v2shim

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// corporationSheet — legacy's CorporationSheetResource, and the shim's first
// single-resource route: it is what makes ItemEnvelope and Float reachable.
//
// ── WHY THIS SHEET AND NOT THE CHARACTER ONE ─────────────────────────────
// character.sheet looks like the obvious first single-resource route and is
// not shimmable — see the CharacterController entry in Classification() for
// the two fields that make it impossible. This one is: app.corporation and
// app.alliance carry every field the recording contains, with no derived and
// no discarded values.
//
// `ceo` and `creator` go through entityCategoryFirst, which always emits
// legacy's "Unknown" rather than the name HANGAR holds — see entity.go for
// why that is byte-compatibility working correctly rather than data being
// withheld.
func corporationSheet(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	corporation, err := req.Deps.Store.GetCorporation(ctx, req.IDs[0])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errShimNotFound
		}
		return nil, internalError("reading corporation sheet", err)
	}

	alliance, err := corporationAlliance(req, corporation.AllianceID)
	if err != nil {
		return nil, err
	}

	sheet := NewObj(13).
		Set("name", corporation.Name).
		Set("ticker", corporation.Ticker).
		Set("member_count", optInt32Num(corporation.MemberCount)).
		Set("ceo", entityCategoryFirst(corporation.CeoID, "character")).
		Set("alliance", alliance).
		Set("description", corporation.Description).
		// tax_rate is a float in BOTH schemas — Float, never Money. Naming
		// it apart from the money path is the whole point of the helper.
		Set("tax_rate", Float(corporation.TaxRate)).
		Set("date_founded", optLegacyTime(corporation.DateFounded)).
		Set("creator", entityCategoryFirst(corporation.CreatorID, "character")).
		Set("url", corporation.Url).
		// Legacy emitted a ONE-KEY object here when faction_id is null —
		// `{"entity_id": null}`, not the three-key EntityResource the other
		// fields use. Reproduced verbatim from the recording. The non-null
		// shape is NOT in the corpus, so the full entity object is used for
		// that case by analogy with ceo/creator and is marked in
		// APPENDIX_C_MIGRATION.md §7 as unverified against a recording.
		Set("faction", corporationFaction(corporation.FactionID)).
		Set("home_station_id", optInt(corporation.HomeStationID)).
		Set("shares", optInt(corporation.Shares))

	return ItemEnvelope(sheet), nil
}

// corporationAlliance is the nested alliance object — a full alliance row
// rather than an EntityResource, with its own key order taken from the
// recording. A corporation in no alliance emits JSON null.
func corporationAlliance(req *Request, allianceID *int64) (any, error) {
	if allianceID == nil {
		return nil, nil
	}
	alliance, err := req.Deps.Store.GetAlliance(req.HTTP.Context(), *allianceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, internalError("resolving alliance", err)
	}
	return NewObj(8).
		Set("alliance_id", Int(alliance.AllianceID)).
		Set("name", alliance.Name).
		Set("creator_id", optInt(alliance.CreatorID)).
		Set("creator_corporation_id", optInt(alliance.CreatorCorporationID)).
		Set("ticker", alliance.Ticker).
		Set("executor_corporation_id", optInt(alliance.ExecutorCorporationID)).
		Set("date_founded", optLegacyTime(alliance.DateFounded)).
		Set("faction_id", optInt32Num(alliance.FactionID)), nil
}

func corporationFaction(factionID *int32) *Obj {
	if factionID == nil {
		return NewObj(1).Set("entity_id", nil)
	}
	return NewObj(3).
		Set("entity_id", Int(int64(*factionID))).
		Set("category", "faction").
		Set("name", legacyUnknownEntityName)
}

func optInt32Num(v *int32) any {
	if v == nil {
		return nil
	}
	return Int(int64(*v))
}

func optLegacyTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return legacyTime(*t)
}
