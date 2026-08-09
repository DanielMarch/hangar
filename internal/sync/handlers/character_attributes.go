package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterAttributesDTO is GET /characters/{character_id}/attributes
// (CharactersCharacterIdAttributesGet).
type CharacterAttributesDTO struct {
	AccruedRemapCooldownDate time.Time `json:"accrued_remap_cooldown_date,omitempty"`
	BonusRemaps              *int32    `json:"bonus_remaps,omitempty"`
	Charisma                 int32     `json:"charisma"`
	Intelligence             int32     `json:"intelligence"`
	LastRemapDate            time.Time `json:"last_remap_date,omitempty"`
	Memory                   int32     `json:"memory"`
	Perception               int32     `json:"perception"`
	Willpower                int32     `json:"willpower"`
}

func ParseCharacterAttributes(body []byte) (CharacterAttributesDTO, error) {
	var dto CharacterAttributesDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterAttributesDTO{}, fmt.Errorf("handlers: parsing character attributes: %w", err)
	}
	return dto, nil
}

func SyncCharacterAttributes(ctx context.Context, s *store.Store, characterID int64, dto CharacterAttributesDTO) (SyncResult, error) {
	if _, err := s.UpsertCharacterAttributes(ctx, gen.UpsertCharacterAttributesParams{
		CharacterID:              characterID,
		Charisma:                 dto.Charisma,
		Intelligence:             dto.Intelligence,
		Memory:                   dto.Memory,
		Perception:               dto.Perception,
		Willpower:                dto.Willpower,
		BonusRemaps:              dto.BonusRemaps,
		LastRemapDate:            nilIfZero(dto.LastRemapDate),
		AccruedRemapCooldownDate: nilIfZero(dto.AccruedRemapCooldownDate),
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting attributes for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
