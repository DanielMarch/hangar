package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterClonesDTO is GET /characters/{character_id}/clones
// (CharactersCharacterIdClonesGet). home_location is a location tuple, NOT
// a jump_clone_id — it's matched against each jump clone's location_id
// below to derive is_home_clone, since app.character_clone models that as
// a per-row boolean rather than storing home_location separately.
type CharacterClonesDTO struct {
	HomeLocation          *CharacterCloneLocation `json:"home_location,omitempty"`
	JumpClones            []CharacterJumpCloneDTO `json:"jump_clones"`
	LastCloneJumpDate     time.Time               `json:"last_clone_jump_date,omitempty"`
	LastStationChangeDate time.Time               `json:"last_station_change_date,omitempty"`
}

type CharacterCloneLocation struct {
	LocationID   int64  `json:"location_id,omitempty"`
	LocationType string `json:"location_type,omitempty"`
}

type CharacterJumpCloneDTO struct {
	Implants     []int64 `json:"implants"`
	JumpCloneID  int64   `json:"jump_clone_id"`
	LocationID   int64   `json:"location_id"`
	LocationType string  `json:"location_type"`
	Name         *string `json:"name,omitempty"`
}

func ParseCharacterClones(body []byte) (CharacterClonesDTO, error) {
	var dto CharacterClonesDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterClonesDTO{}, fmt.Errorf("handlers: parsing character clones: %w", err)
	}
	return dto, nil
}

func SyncCharacterClones(ctx context.Context, s *store.Store, characterID int64, dto CharacterClonesDTO) (SyncResult, error) {
	ids := make([]int64, len(dto.JumpClones))
	lastJump := nilIfZero(dto.LastCloneJumpDate)
	lastStationChange := nilIfZero(dto.LastStationChangeDate)
	for i, jc := range dto.JumpClones {
		ids[i] = jc.JumpCloneID
		isHome := dto.HomeLocation != nil && dto.HomeLocation.LocationID != 0 && dto.HomeLocation.LocationID == jc.LocationID
		implants := jc.Implants
		if implants == nil {
			implants = []int64{}
		}
		if _, err := s.UpsertCharacterClone(ctx, gen.UpsertCharacterCloneParams{
			CharacterID:           characterID,
			JumpCloneID:           jc.JumpCloneID,
			LocationID:            jc.LocationID,
			LocationType:          jc.LocationType,
			Name:                  jc.Name,
			Implants:              implants,
			IsHomeClone:           isHome,
			LastCloneJumpDate:     lastJump,
			LastStationChangeDate: lastStationChange,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting jump clone %d for character %d: %w", jc.JumpCloneID, characterID, err)
		}
	}
	if err := s.DeleteCharacterClonesNotIn(ctx, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale jump clones for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(dto.JumpClones))}, nil
}

// CharacterImplantsDTO is GET /characters/{character_id}/implants — a bare
// array of type_id (int64).
type CharacterImplantsDTO []int64

func ParseCharacterImplants(body []byte) (CharacterImplantsDTO, error) {
	var dto CharacterImplantsDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character implants: %w", err)
	}
	return dto, nil
}

func SyncCharacterImplants(ctx context.Context, s *store.Store, characterID int64, typeIDs CharacterImplantsDTO) (SyncResult, error) {
	ids := []int64(typeIDs)
	for _, id := range ids {
		if _, err := s.ReplaceCharacterImplant(ctx, characterID, id); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting implant %d for character %d: %w", id, characterID, err)
		}
	}
	if err := s.DeleteCharacterImplantsNotIn(ctx, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale implants for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(ids))}, nil
}
