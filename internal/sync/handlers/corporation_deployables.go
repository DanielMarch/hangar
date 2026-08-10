package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ---- GET /corporations/{corporation_id}/structures ----

type CorporationStructureServiceDTO struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type CorporationStructureDTO struct {
	CorporationID      int64                            `json:"corporation_id"`
	FuelExpires        time.Time                        `json:"fuel_expires,omitempty"`
	Name               *string                          `json:"name,omitempty"`
	NextReinforceApply time.Time                        `json:"next_reinforce_apply,omitempty"`
	NextReinforceHour  *int16                           `json:"next_reinforce_hour,omitempty"`
	ProfileID          int32                            `json:"profile_id"`
	ReinforceHour      *int16                           `json:"reinforce_hour,omitempty"`
	Services           []CorporationStructureServiceDTO `json:"services,omitempty"`
	State              string                           `json:"state"`
	StateTimerEnd      time.Time                        `json:"state_timer_end,omitempty"`
	StateTimerStart    time.Time                        `json:"state_timer_start,omitempty"`
	StructureID        int64                            `json:"structure_id"`
	SystemID           int32                            `json:"system_id"`
	TypeID             int32                            `json:"type_id"`
	UnanchorsAt        time.Time                        `json:"unanchors_at,omitempty"`
}

func ParseCorporationStructures(body []byte) ([]CorporationStructureDTO, error) {
	var dto []CorporationStructureDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation structures: %w", err)
	}
	return dto, nil
}

func SyncCorporationStructures(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationStructureDTO) (SyncResult, error) {
	for _, r := range rows {
		services := r.Services
		if services == nil {
			services = []CorporationStructureServiceDTO{}
		}
		servicesJSON, err := json.Marshal(services)
		if err != nil {
			return SyncResult{}, fmt.Errorf("handlers: marshalling services for structure %d: %w", r.StructureID, err)
		}
		profileID := r.ProfileID
		if _, err := s.UpsertCorporationStructure(ctx, gen.UpsertCorporationStructureParams{
			CorporationID: corporationID, StructureID: r.StructureID, TypeID: r.TypeID, SystemID: r.SystemID,
			ProfileID: &profileID, FuelExpires: nilIfZero(r.FuelExpires), State: &r.State,
			StateTimerStart: nilIfZero(r.StateTimerStart), StateTimerEnd: nilIfZero(r.StateTimerEnd),
			UnanchorsAt: nilIfZero(r.UnanchorsAt), ReinforceHour: r.ReinforceHour,
			NextReinforceApply: nilIfZero(r.NextReinforceApply), NextReinforceHour: r.NextReinforceHour,
			Services: servicesJSON,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting structure %d for corp %d: %w", r.StructureID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/starbases ----

type CorporationStarbaseDTO struct {
	MoonID          *int64    `json:"moon_id,omitempty"`
	OnlinedSince    time.Time `json:"onlined_since,omitempty"`
	ReinforcedUntil time.Time `json:"reinforced_until,omitempty"`
	StarbaseID      int64     `json:"starbase_id"`
	State           *string   `json:"state,omitempty"`
	SystemID        int32     `json:"system_id"`
	TypeID          int32     `json:"type_id"`
	UnanchorAt      time.Time `json:"unanchor_at,omitempty"`
}

func ParseCorporationStarbases(body []byte) ([]CorporationStarbaseDTO, error) {
	var dto []CorporationStarbaseDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation starbases: %w", err)
	}
	return dto, nil
}

func SyncCorporationStarbases(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationStarbaseDTO) (SyncResult, error) {
	for _, r := range rows {
		if _, err := s.UpsertCorporationStarbase(ctx, gen.UpsertCorporationStarbaseParams{
			CorporationID: corporationID, StarbaseID: r.StarbaseID, TypeID: r.TypeID, SystemID: r.SystemID,
			MoonID: r.MoonID, OnlinedSince: nilIfZero(r.OnlinedSince), ReinforcedUntil: nilIfZero(r.ReinforcedUntil),
			State: r.State, UnanchorAt: nilIfZero(r.UnanchorAt),
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting starbase %d for corp %d: %w", r.StarbaseID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/starbases/{starbase_id} ----
// Phase 14's fuel-low alert depends on `fuels` landing in
// app.starbase_detail.fuels — see that column's comment in
// 00010_domain_corporation_structure.sql.

type CorporationStarbaseFuelDTO struct {
	Quantity int64 `json:"quantity"`
	TypeID   int32 `json:"type_id"`
}

type CorporationStarbaseDetailDTO struct {
	AllowAllianceMembers    bool                         `json:"allow_alliance_members"`
	AllowCorporationMembers bool                         `json:"allow_corporation_members"`
	AttackStandingThreshold *float64                     `json:"attack_standing_threshold,omitempty"`
	FuelBayView             string                       `json:"fuel_bay_view"`
	Fuels                   []CorporationStarbaseFuelDTO `json:"fuels,omitempty"`
	ReinforcedUntil         time.Time                    `json:"reinforced_until,omitempty"`
	UseAllianceStandings    bool                         `json:"use_alliance_standings"`
}

func ParseCorporationStarbaseDetail(body []byte) (CorporationStarbaseDetailDTO, error) {
	var dto CorporationStarbaseDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationStarbaseDetailDTO{}, fmt.Errorf("handlers: parsing corporation starbase detail: %w", err)
	}
	return dto, nil
}

func SyncCorporationStarbaseDetail(ctx context.Context, s *store.Store, corporationID, starbaseID int64, systemID int32, dto CorporationStarbaseDetailDTO) (SyncResult, error) {
	fuels := dto.Fuels
	if fuels == nil {
		fuels = []CorporationStarbaseFuelDTO{}
	}
	fuelsJSON, err := json.Marshal(fuels)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: marshalling fuels for starbase %d: %w", starbaseID, err)
	}
	allowAlliance, allowCorp, useAllianceStandings := dto.AllowAllianceMembers, dto.AllowCorporationMembers, dto.UseAllianceStandings
	fuelBayView := dto.FuelBayView
	if _, err := s.UpsertStarbaseDetail(ctx, gen.UpsertStarbaseDetailParams{
		CorporationID: corporationID, StarbaseID: starbaseID, SystemID: systemID, FuelBayView: &fuelBayView,
		AllowAllianceMembers: &allowAlliance, AllowCorporationMembers: &allowCorp,
		UseAllianceStandings: &useAllianceStandings, AttackStandingThreshold: dto.AttackStandingThreshold,
		Fuels: fuelsJSON, ReinforcedUntil: nilIfZero(dto.ReinforcedUntil),
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting starbase detail %d for corp %d: %w", starbaseID, corporationID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// ---- GET /corporations/{corporation_id}/structures/skyhooks (list, wrapped) ----

type CorporationSkyhookListDTO struct {
	Skyhooks []CorporationSkyhookListEntryDTO `json:"skyhooks"`
}

type CorporationSkyhookListEntryDTO struct {
	ID       int64 `json:"id"`
	PlanetID int64 `json:"planet_id"`
}

func ParseCorporationSkyhookList(body []byte) (CorporationSkyhookListDTO, error) {
	var dto CorporationSkyhookListDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationSkyhookListDTO{}, fmt.Errorf("handlers: parsing corporation skyhook list: %w", err)
	}
	return dto, nil
}

// ---- GET /corporations/{corporation_id}/structures/skyhooks/{skyhook_id} (detail) ----
// The list endpoint carries only id/planet_id — type_id, system_id and
// fuel_expires (app.corporation_skyhook's other required columns) only
// appear on the detail call, so the sync driver (worker/corporation.go)
// fans out list -> one detail call per id, mirroring the starbase pattern.

type CorporationSkyhookDetailDTO struct {
	ID       int64  `json:"id"`
	IsActive bool   `json:"is_active"`
	PlanetID int64  `json:"planet_id"`
	State    string `json:"state"`
}

func ParseCorporationSkyhookDetail(body []byte) (CorporationSkyhookDetailDTO, error) {
	var dto CorporationSkyhookDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationSkyhookDetailDTO{}, fmt.Errorf("handlers: parsing corporation skyhook detail: %w", err)
	}
	return dto, nil
}

// SyncCorporationSkyhook upserts app.corporation_skyhook from the detail
// call. typeID/systemID/fuelExpires come from the worker's own knowledge
// (the structure catalogue / a separate resolution step) because the
// skyhook detail response itself carries neither type_id nor system_id —
// documented as a further gap below.
//
// SPEC GAP: app.corporation_skyhook requires type_id and system_id
// (00010_domain_corporation_structure.sql, table #15) and fuel_expires is
// its whole fuel-tracking purpose, but NEITHER the list endpoint
// (id/planet_id only) NOR the detail endpoint (id/planet_id/state/is_active/
// reagents/timers) returns type_id, system_id, or fuel_expires anywhere.
// A skyhook is a planetary structure (its type is always the one skyhook
// type, and its system is resolvable via planet_id -> planet -> system,
// which is Phase 9/25's SDE join, not data ESI hands back here). Reported
// rather than worked around: typeID/systemID are accepted as caller-
// supplied parameters (resolved elsewhere) so this function does not
// silently write zero values; fuel_expires has no source at all and is
// always persisted NULL until CCP's skyhook response schema adds it.
func SyncCorporationSkyhook(ctx context.Context, s *store.Store, corporationID, skyhookID int64, typeID, systemID int32, dto CorporationSkyhookDetailDTO) (SyncResult, error) {
	state := dto.State
	if _, err := s.UpsertCorporationSkyhook(ctx, gen.UpsertCorporationSkyhookParams{
		CorporationID: corporationID, SkyhookID: skyhookID, TypeID: typeID, SystemID: systemID,
		PlanetID: &dto.PlanetID, State: &state, FuelExpires: nil,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting skyhook %d for corp %d: %w", skyhookID, corporationID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// ---- GET /corporations/{corporation_id}/structures/sovereignty-hubs (list, wrapped) ----

type CorporationSovereigntyHubListDTO struct {
	SovereigntyHubs []CorporationSovereigntyHubListEntryDTO `json:"sovereignty_hubs"`
}

type CorporationSovereigntyHubListEntryDTO struct {
	ID            int64 `json:"id"`
	SolarSystemID int32 `json:"solar_system_id"`
}

func ParseCorporationSovereigntyHubList(body []byte) (CorporationSovereigntyHubListDTO, error) {
	var dto CorporationSovereigntyHubListDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationSovereigntyHubListDTO{}, fmt.Errorf("handlers: parsing corporation sovereignty hub list: %w", err)
	}
	return dto, nil
}

// SyncCorporationSovereigntyHubs upserts every hub from the LIST response
// directly — unlike skyhooks, the list already carries every column
// app.corporation_sovereignty_hub models (id, solar_system_id); type_id has
// no source here either (a sovereignty hub, like a skyhook, has exactly one
// possible type in EVE, but ESI's list schema doesn't echo it) and
// fuel_expires is on neither list nor detail — the detail response
// (upgrades/resources/reagent_bay/workforce_transport) is a richer payload
// this phase does not have a table for and is out of Phase 8's scope.
func SyncCorporationSovereigntyHubs(ctx context.Context, s *store.Store, corporationID int64, typeID int32, hubs []CorporationSovereigntyHubListEntryDTO) (SyncResult, error) {
	for _, h := range hubs {
		if _, err := s.UpsertCorporationSovereigntyHub(ctx, gen.UpsertCorporationSovereigntyHubParams{
			CorporationID: corporationID, HubID: h.ID, TypeID: typeID, SystemID: h.SolarSystemID, FuelExpires: nil,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting sovereignty hub %d for corp %d: %w", h.ID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(hubs))}, nil
}
