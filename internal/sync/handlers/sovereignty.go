// Sovereignty (campaigns, systems) is global reference data — not owned by
// a character or corporation (02_DATABASE_SCHEMA.md §5.2's "Sovereignty
// (2)" group) — synced by a GlobalWorker (worker/global.go) against
// entity_kind = "global" subscriptions, entity_id = 0.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ---- GET /sovereignty/campaigns ----
// `participants` (per-alliance score breakdown) is parsed for field-loss
// coverage but not persisted — app.sovereignty_campaign only models the
// attacker/defender aggregate scores (attackers_score/defender_score); no
// table in this phase's authorized schema surface models a per-alliance
// breakdown, and Appendix A names no capability that needs one.
type SovereigntyCampaignDTO struct {
	AttackersScore  *float64                            `json:"attackers_score,omitempty"`
	CampaignID      int64                               `json:"campaign_id"`
	ConstellationID int32                               `json:"constellation_id"`
	DefenderID      *int64                              `json:"defender_id,omitempty"`
	DefenderScore   *float64                            `json:"defender_score,omitempty"`
	EventType       string                              `json:"event_type"`
	Participants    []SovereigntyCampaignParticipantDTO `json:"participants,omitempty"`
	SolarSystemID   int32                               `json:"solar_system_id"`
	StartTime       time.Time                           `json:"start_time"`
	StructureID     int64                               `json:"structure_id"`
}

type SovereigntyCampaignParticipantDTO struct {
	AllianceID int64   `json:"alliance_id"`
	Score      float64 `json:"score"`
}

func ParseSovereigntyCampaigns(body []byte) ([]SovereigntyCampaignDTO, error) {
	var dto []SovereigntyCampaignDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing sovereignty campaigns: %w", err)
	}
	return dto, nil
}

// SyncSovereigntyCampaigns upserts every live campaign and prunes any
// campaign_id absent from the current feed — a concluded campaign simply
// vanishes upstream (sovereignty.sql's DeleteSovereigntyCampaignsNotIn doc
// comment), there is no "resolved" state to soft-delete against.
func SyncSovereigntyCampaigns(ctx context.Context, s *store.Store, campaigns []SovereigntyCampaignDTO) (SyncResult, error) {
	ids := make([]int64, len(campaigns))
	for i, c := range campaigns {
		ids[i] = c.CampaignID
		structureID := c.StructureID
		if _, err := s.UpsertSovereigntyCampaign(ctx, gen.UpsertSovereigntyCampaignParams{
			CampaignID: c.CampaignID, ConstellationID: c.ConstellationID, SolarSystemID: c.SolarSystemID,
			StructureID: &structureID, DefenderID: c.DefenderID, EventType: c.EventType,
			StartTime: c.StartTime, AttackersScore: c.AttackersScore, DefenderScore: c.DefenderScore,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting sovereignty campaign %d: %w", c.CampaignID, err)
		}
	}
	if err := s.DeleteSovereigntyCampaignsNotIn(ctx, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale sovereignty campaigns: %w", err)
	}
	return SyncResult{RowsAffected: int32(len(campaigns))}, nil
}

// ---- GET /sovereignty/systems ----

type SovereigntySystemsDTO struct {
	SolarSystems []SovereigntySystemDTO `json:"solar_systems"`
}

type SovereigntySystemDTO struct {
	AllianceID    *int64 `json:"alliance_id,omitempty"`
	CorporationID *int64 `json:"corporation_id,omitempty"`
	FactionID     *int32 `json:"faction_id,omitempty"`
	SystemID      int32  `json:"system_id"`
}

func ParseSovereigntySystems(body []byte) (SovereigntySystemsDTO, error) {
	var dto SovereigntySystemsDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return SovereigntySystemsDTO{}, fmt.Errorf("handlers: parsing sovereignty systems: %w", err)
	}
	return dto, nil
}

func SyncSovereigntySystems(ctx context.Context, s *store.Store, dto SovereigntySystemsDTO) (SyncResult, error) {
	for _, sys := range dto.SolarSystems {
		if _, err := s.UpsertSovereigntySystem(ctx, gen.UpsertSovereigntySystemParams{
			SystemID: sys.SystemID, AllianceID: sys.AllianceID, CorporationID: sys.CorporationID, FactionID: sys.FactionID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting sovereignty system %d: %w", sys.SystemID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(dto.SolarSystems))}, nil
}
