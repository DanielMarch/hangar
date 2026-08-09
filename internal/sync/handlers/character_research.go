package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterAgentResearchDTO is one element of GET
// /characters/{character_id}/agents_research.
type CharacterAgentResearchDTO struct {
	AgentID         int64     `json:"agent_id"`
	PointsPerDay    float64   `json:"points_per_day"`
	RemainderPoints float64   `json:"remainder_points"`
	SkillTypeID     int64     `json:"skill_type_id"`
	StartedAt       time.Time `json:"started_at"`
}

func ParseCharacterAgentResearch(body []byte) ([]CharacterAgentResearchDTO, error) {
	var dto []CharacterAgentResearchDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character agent research: %w", err)
	}
	return dto, nil
}

func SyncCharacterAgentResearch(ctx context.Context, s *store.Store, characterID int64, entries []CharacterAgentResearchDTO) (SyncResult, error) {
	ids := make([]int64, len(entries))
	for i, e := range entries {
		ids[i] = e.AgentID
		if _, err := s.UpsertCharacterAgentResearch(ctx, gen.UpsertCharacterAgentResearchParams{
			CharacterID: characterID, AgentID: e.AgentID, SkillTypeID: e.SkillTypeID,
			StartedAt: e.StartedAt, PointsPerDay: e.PointsPerDay, RemainderPoints: e.RemainderPoints,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting agent research %d for character %d: %w", e.AgentID, characterID, err)
		}
	}
	if err := s.DeleteCharacterAgentResearchNotIn(ctx, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale agent research for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(entries))}, nil
}
