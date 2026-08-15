// Location resolution — Appendix A capability #41.
//
// ── PHASE 20.8: THE TABLE THAT HAD AN ENDPOINT, A QUERY AND NO CALLER ────
// app.location has existed since Phase 1a (00003_platform_reference.sql
// #49) and GET /api/v1/support/universe/{structures,stations} has served it
// since Phase 15. UpsertLocation was generated and called by nothing, so
// both endpoints 404'd on every id on every installation ever deployed.
//
// The missing piece was never the handler — it is thirty lines and it is
// below. It was the ENUMERATION: neither /universe/stations/{station_id} nor
// /universe/structures/{structure_id} can be listed, so the id set has to
// come from HANGAR's own already-synced rows, and no query produced it. See
// db/queries/reference.sql's ListUnresolvedStationIDs and
// ListCharacterStructureIDs for which tables are read and, more importantly,
// which are deliberately not.
//
// ── THE TWO HALVES ARE NOT SYMMETRIC ─────────────────────────────────────
// A station is public: no scope, no token, one global subscription, and the
// response always carries the full sheet.
//
// A structure needs esi-universe.read_structures.v1 AND docking access, and
// its response is TRUNCATED without the latter: the spec marks only name,
// solar_system_id and owner_id required, with type_id and position present
// only for a caller who can dock. So a structure row may legitimately land
// with a NULL type_id, and that is data rather than a short read.
//
// A 403 from the structure route is likewise DATA — "this structure exists
// and is not visible to you" — not a failure to retry. It cannot be a
// missing-scope 403 on a running installation, because the subscription
// reconciler's NOT EXISTS scope gate (db/queries/sync_subscription.sql)
// refuses to create a subscription for a route whose scopes the acting
// token does not hold. That gate is what makes "403 means no docking
// access" a sound reading rather than a guess.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// LocationTypeStation and LocationTypeStructure are app.location's
// discriminator values. They are CCP's own vocabulary — the same two strings
// app.asset.location_type and app.character_clone.location_type carry
// verbatim — which is why the enumeration queries can filter on them and why
// this file does not invent an enum for them (00003's "open vocabulary").
const (
	LocationTypeStation   = "station"
	LocationTypeStructure = "structure"
)

// StationDTO is GET /universe/stations/{station_id}.
//
// Only the fields app.location holds are declared. That is not field loss in
// the Principle 13 sense: reprocessing_efficiency, services and the rest
// describe a station's FACILITIES, which the SDE carries for every station
// in the game and which app.location — a four-column resolution table — has
// nowhere to put. What is stored is the identity: name, system, type, owner.
//
// system_id and type_id are int32 to match app.location's columns. ESI
// declares them int64; json.Unmarshal returns an overflow error rather than
// truncating, so a value that genuinely did not fit would fail the parse
// loudly instead of landing wrong. `owner` is a pointer because the spec
// does not mark it required — an unowned station omits it.
type StationDTO struct {
	StationID int64  `json:"station_id"`
	Name      string `json:"name"`
	SystemID  int32  `json:"system_id"`
	TypeID    int32  `json:"type_id"`
	Owner     *int64 `json:"owner,omitempty"`
}

func ParseStation(body []byte) (StationDTO, error) {
	var dto StationDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return StationDTO{}, fmt.Errorf("handlers: parsing station: %w", err)
	}
	return dto, nil
}

// SyncStation resolves one station into app.location.
//
// The id comes from the CALLER (the fan-out item), not from the body's own
// station_id: the fan-out asked about a specific id and that is the key the
// row must land under. Trusting the body would let a response for a
// different station overwrite the wrong row — the same reasoning
// SyncKillmail applies to its hash.
func SyncStation(ctx context.Context, s *store.Store, stationID int64, dto StationDTO) (SyncResult, error) {
	name, systemID, typeID := dto.Name, dto.SystemID, dto.TypeID
	if _, err := s.UpsertLocation(ctx, gen.UpsertLocationParams{
		LocationType: LocationTypeStation, LocationID: stationID,
		Name: &name, SystemID: &systemID, OwnerID: dto.Owner, TypeID: &typeID,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting station %d: %w", stationID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// StructureDTO is GET /universe/structures/{structure_id}.
//
// solar_system_id, not system_id — the two routes name the same concept
// differently and app.location has one column for it. type_id is a pointer
// because the spec does not mark it required: a caller with visibility but
// no docking access gets name, solar_system_id and owner_id only.
type StructureDTO struct {
	Name          string `json:"name"`
	SolarSystemID int32  `json:"solar_system_id"`
	OwnerID       int64  `json:"owner_id"`
	TypeID        *int32 `json:"type_id,omitempty"`
}

func ParseStructure(body []byte) (StructureDTO, error) {
	var dto StructureDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return StructureDTO{}, fmt.Errorf("handlers: parsing structure: %w", err)
	}
	return dto, nil
}

// SyncStructure resolves one structure into app.location.
//
// The response carries no structure_id of its own — the id is only ever the
// one in the request path — so the caller's id is the only possible key
// here, not merely the safer one.
func SyncStructure(ctx context.Context, s *store.Store, structureID int64, dto StructureDTO) (SyncResult, error) {
	name, systemID, ownerID := dto.Name, dto.SolarSystemID, dto.OwnerID
	if _, err := s.UpsertLocation(ctx, gen.UpsertLocationParams{
		LocationType: LocationTypeStructure, LocationID: structureID,
		Name: &name, SystemID: &systemID, OwnerID: &ownerID, TypeID: dto.TypeID,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting structure %d: %w", structureID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
