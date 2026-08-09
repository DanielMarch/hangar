package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterFatigueDTO is GET /characters/{character_id}/fatigue
// (CharactersCharacterIdFatigueGet). Every field is optional: a character
// that has never jumped has all three absent.
type CharacterFatigueDTO struct {
	JumpFatigueExpireDate time.Time `json:"jump_fatigue_expire_date,omitempty"`
	LastJumpDate          time.Time `json:"last_jump_date,omitempty"`
	LastUpdateDate        time.Time `json:"last_update_date,omitempty"`
}

func ParseCharacterFatigue(body []byte) (CharacterFatigueDTO, error) {
	var dto CharacterFatigueDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterFatigueDTO{}, fmt.Errorf("handlers: parsing character fatigue: %w", err)
	}
	return dto, nil
}

func SyncCharacterFatigue(ctx context.Context, s *store.Store, characterID int64, dto CharacterFatigueDTO) (SyncResult, error) {
	if _, err := s.UpsertCharacterJumpFatigue(ctx, gen.UpsertCharacterJumpFatigueParams{
		CharacterID:           characterID,
		JumpFatigueExpireDate: nilIfZero(dto.JumpFatigueExpireDate),
		LastJumpDate:          nilIfZero(dto.LastJumpDate),
		LastUpdateDate:        nilIfZero(dto.LastUpdateDate),
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting fatigue for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
