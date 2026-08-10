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
//
// PHASE 8.1 FIX (00033_phase8_1_skyhook_reagent_fixup.sql): skyhooks are
// reagent-powered, not fuel-powered — the whole reason fuel_expires was
// dropped from app.corporation_skyhook. type_id/system_id are genuinely
// unresolvable pre-SDE (see that migration's header) and are now nullable;
// this list sync only ever seeds identity (id, planet_id), never guesses
// at either.

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

// SyncCorporationSkyhooks seeds/prunes app.corporation_skyhook's identity
// rows from the LIST response — the worker.go fan-out then calls
// SyncCorporationSkyhookDetail per id to fill in state/reagents/is_active
// (mirroring corporation_starbase's list-then-starbase_detail pattern,
// except detail lives in the SAME table here, not a separate one).
func SyncCorporationSkyhooks(ctx context.Context, s *store.Store, corporationID int64, skyhooks []CorporationSkyhookListEntryDTO) (SyncResult, error) {
	ids := make([]int64, len(skyhooks))
	for i, sh := range skyhooks {
		ids[i] = sh.ID
		planetID := sh.PlanetID
		if _, err := s.UpsertCorporationSkyhookStub(ctx, corporationID, sh.ID, &planetID); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: seeding skyhook %d for corp %d: %w", sh.ID, corporationID, err)
		}
	}
	if err := s.DeleteCorporationSkyhooksNotIn(ctx, corporationID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale skyhooks for corp %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: int32(len(skyhooks))}, nil
}

// ---- GET /corporations/{corporation_id}/structures/skyhooks/{skyhook_id} (detail) ----

// CorporationSkyhookReagentDTO is one element of the detail response's
// `reagents` array — the skyhook's actual power source (replacing the
// fuel_expires concept dropped in Phase 8.1).
type CorporationSkyhookReagentDTO struct {
	LastCycle      time.Time `json:"last_cycle,omitempty"`
	SecuredStock   int64     `json:"secured_stock"`
	TypeID         int32     `json:"type_id"`
	UnsecuredStock int64     `json:"unsecured_stock"`
}

type CorporationSkyhookDetailDTO struct {
	EffectiveWorkforce int64                          `json:"effective_workforce,omitempty"`
	ID                 int64                          `json:"id"`
	IsActive           bool                           `json:"is_active"`
	PlanetID           int64                          `json:"planet_id"`
	Reagents           []CorporationSkyhookReagentDTO `json:"reagents,omitempty"`
	// ReinforcementTimer/TheftVulnerability are parsed for field-loss
	// coverage but not persisted — no capability in Appendix A needs the
	// raw timer windows independent of `state`, and adding columns for
	// them is outside this fixup's scope (the fuel/reagent mismatch).
	ReinforcementTimer *CorporationSkyhookTimerDTO  `json:"reinforcement_timer,omitempty"`
	State              string                       `json:"state"`
	TheftVulnerability *CorporationSkyhookWindowDTO `json:"theft_vulnerability,omitempty"`
}

type CorporationSkyhookTimerDTO struct {
	End time.Time `json:"end,omitempty"`
}

type CorporationSkyhookWindowDTO struct {
	Start time.Time `json:"start,omitempty"`
	End   time.Time `json:"end,omitempty"`
}

func ParseCorporationSkyhookDetail(body []byte) (CorporationSkyhookDetailDTO, error) {
	var dto CorporationSkyhookDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationSkyhookDetailDTO{}, fmt.Errorf("handlers: parsing corporation skyhook detail: %w", err)
	}
	return dto, nil
}

// SyncCorporationSkyhookDetail updates state/is_active/reagents on the row
// SyncCorporationSkyhooks already seeded. type_id/system_id are never
// touched here — see this file's Phase 8.1 header comment.
func SyncCorporationSkyhookDetail(ctx context.Context, s *store.Store, corporationID, skyhookID int64, dto CorporationSkyhookDetailDTO) (SyncResult, error) {
	reagents := dto.Reagents
	if reagents == nil {
		reagents = []CorporationSkyhookReagentDTO{}
	}
	reagentsJSON, err := json.Marshal(reagents)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: marshalling reagents for skyhook %d: %w", skyhookID, err)
	}
	state, isActive := dto.State, dto.IsActive
	if _, err := s.UpsertCorporationSkyhookDetail(ctx, gen.UpsertCorporationSkyhookDetailParams{
		CorporationID: corporationID, SkyhookID: skyhookID, State: &state, IsActive: &isActive, Reagents: reagentsJSON,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting skyhook detail %d for corp %d: %w", skyhookID, corporationID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// ---- GET /corporations/{corporation_id}/structures/sovereignty-hubs (list, wrapped) ----
//
// PHASE 8.1 FIX: same reagent-not-fuel correction as skyhooks. Unlike
// skyhooks, the list response DOES carry solar_system_id directly, so
// system_id is never a gap here — only type_id (unresolvable pre-SDE,
// same reasoning as skyhooks) and fuel_expires (replaced by reagents) changed.

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

// SyncCorporationSovereigntyHubs upserts every hub's identity from the
// LIST response (id, solar_system_id — both directly available, unlike
// skyhooks). SyncCorporationSovereigntyHubDetail (below) fills in reagents.
func SyncCorporationSovereigntyHubs(ctx context.Context, s *store.Store, corporationID int64, hubs []CorporationSovereigntyHubListEntryDTO) (SyncResult, error) {
	ids := make([]int64, len(hubs))
	for i, h := range hubs {
		ids[i] = h.ID
		if _, err := s.UpsertCorporationSovereigntyHub(ctx, corporationID, h.ID, h.SolarSystemID); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting sovereignty hub %d for corp %d: %w", h.ID, corporationID, err)
		}
	}
	if err := s.DeleteCorporationSovereigntyHubsNotIn(ctx, corporationID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale sovereignty hubs for corp %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: int32(len(hubs))}, nil
}

// ---- GET /corporations/{corporation_id}/structures/sovereignty-hubs/{sovereignty_hub_id} (detail) ----

// CorporationSovereigntyHubReagentBayDTO mirrors the skyhook reagents
// concept — a sovereignty hub is also reagent-powered, not fuel-powered.
type CorporationSovereigntyHubReagentBayDTO struct {
	LastUpdated time.Time                      `json:"last_updated,omitempty"`
	Reagents    []CorporationSkyhookReagentDTO `json:"reagents,omitempty"`
}

// CorporationSovereigntyHubDetailDTO. `upgrades`/`resources`/
// `workforce_transport`/`vulnerability_window`/`fuel_access_list_id` are
// parsed for field-loss coverage but not persisted — a richer payload than
// this fixup's scope (the fuel/reagent mismatch) covers; no Appendix A
// capability needs them yet.
type CorporationSovereigntyHubDetailDTO struct {
	FuelAccessListID    *int64                                 `json:"fuel_access_list_id,omitempty"`
	ID                  int64                                  `json:"id"`
	ReagentBay          CorporationSovereigntyHubReagentBayDTO `json:"reagent_bay"`
	Resources           CorporationSovereigntyHubResourcesDTO  `json:"resources"`
	SolarSystemID       int32                                  `json:"solar_system_id"`
	Upgrades            []CorporationSovereigntyHubUpgradeDTO  `json:"upgrades,omitempty"`
	VulnerabilityWindow *CorporationSkyhookWindowDTO           `json:"vulnerability_window,omitempty"`
	WorkforceTransport  CorporationSovereigntyHubWorkforceDTO  `json:"workforce_transport"`
}

type CorporationSovereigntyHubResourcesDTO struct {
	Power     float64 `json:"power,omitempty"`
	Workforce float64 `json:"workforce,omitempty"`
}

type CorporationSovereigntyHubUpgradeDTO struct {
	PowerState string `json:"power_state,omitempty"`
	TypeID     int32  `json:"type_id"`
}

type CorporationSovereigntyHubWorkforceDTO struct {
	Configuration string `json:"configuration,omitempty"`
	State         string `json:"state,omitempty"`
}

func ParseCorporationSovereigntyHubDetail(body []byte) (CorporationSovereigntyHubDetailDTO, error) {
	var dto CorporationSovereigntyHubDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationSovereigntyHubDetailDTO{}, fmt.Errorf("handlers: parsing corporation sovereignty hub detail: %w", err)
	}
	return dto, nil
}

// SyncCorporationSovereigntyHubDetail updates reagents on the row
// SyncCorporationSovereigntyHubs already seeded. system_id is re-supplied
// from the detail response itself (it's authoritative and directly
// available, unlike skyhooks) rather than trusting the earlier list call.
func SyncCorporationSovereigntyHubDetail(ctx context.Context, s *store.Store, corporationID int64, dto CorporationSovereigntyHubDetailDTO) (SyncResult, error) {
	reagents := dto.ReagentBay.Reagents
	if reagents == nil {
		reagents = []CorporationSkyhookReagentDTO{}
	}
	reagentsJSON, err := json.Marshal(reagents)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: marshalling reagents for sovereignty hub %d: %w", dto.ID, err)
	}
	if _, err := s.UpsertCorporationSovereigntyHubDetail(ctx, gen.UpsertCorporationSovereigntyHubDetailParams{
		CorporationID: corporationID, HubID: dto.ID, SystemID: dto.SolarSystemID, Reagents: reagentsJSON,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting sovereignty hub detail %d for corp %d: %w", dto.ID, corporationID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
