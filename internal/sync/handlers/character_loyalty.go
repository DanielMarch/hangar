package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
)

// CharacterLoyaltyPointDTO is one element of GET
// /characters/{character_id}/loyalty/points.
type CharacterLoyaltyPointDTO struct {
	CorporationID int64 `json:"corporation_id"`
	LoyaltyPoints int64 `json:"loyalty_points"`
}

func ParseCharacterLoyaltyPoints(body []byte) ([]CharacterLoyaltyPointDTO, error) {
	var dto []CharacterLoyaltyPointDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character loyalty points: %w", err)
	}
	return dto, nil
}

func SyncCharacterLoyaltyPoints(ctx context.Context, s *store.Store, characterID int64, points []CharacterLoyaltyPointDTO) (SyncResult, error) {
	ids := make([]int64, len(points))
	for i, p := range points {
		ids[i] = p.CorporationID
		if _, err := s.UpsertCharacterLoyaltyPoint(ctx, characterID, p.CorporationID, p.LoyaltyPoints); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting loyalty points for corp %d, character %d: %w", p.CorporationID, characterID, err)
		}
	}
	if err := s.DeleteCharacterLoyaltyPointsNotIn(ctx, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale loyalty points for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(points))}, nil
}
