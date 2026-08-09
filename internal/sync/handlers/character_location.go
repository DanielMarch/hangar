package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterLocationDTO is GET /characters/{character_id}/location.
type CharacterLocationDTO struct {
	SolarSystemID int64  `json:"solar_system_id"`
	StationID     *int64 `json:"station_id,omitempty"`
	StructureID   *int64 `json:"structure_id,omitempty"`
}

func ParseCharacterLocation(body []byte) (CharacterLocationDTO, error) {
	var dto CharacterLocationDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterLocationDTO{}, fmt.Errorf("handlers: parsing character location: %w", err)
	}
	return dto, nil
}

func SyncCharacterLocation(ctx context.Context, s *store.Store, characterID int64, dto CharacterLocationDTO) (SyncResult, error) {
	if _, err := s.UpsertCharacterLocationOnly(ctx, gen.UpsertCharacterLocationOnlyParams{
		CharacterID: characterID, SolarSystemID: dto.SolarSystemID, StationID: dto.StationID, StructureID: dto.StructureID,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting location for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// CharacterOnlineDTO is GET /characters/{character_id}/online.
// last_login/last_logout are absent for a character who has never logged
// in since the field existed (roadmap edge case) — both are already
// pointer-typed to carry that.
type CharacterOnlineDTO struct {
	LastLogin  time.Time `json:"last_login,omitempty"`
	LastLogout time.Time `json:"last_logout,omitempty"`
	Logins     *int64    `json:"logins,omitempty"`
	Online     bool      `json:"online"`
}

func ParseCharacterOnline(body []byte) (CharacterOnlineDTO, error) {
	var dto CharacterOnlineDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterOnlineDTO{}, fmt.Errorf("handlers: parsing character online: %w", err)
	}
	return dto, nil
}

func SyncCharacterOnline(ctx context.Context, s *store.Store, characterID int64, dto CharacterOnlineDTO) (SyncResult, error) {
	online := dto.Online
	if _, err := s.UpsertCharacterOnlineOnly(ctx, gen.UpsertCharacterOnlineOnlyParams{
		CharacterID: characterID, IsOnline: &online,
		LastLogin: nilIfZero(dto.LastLogin), LastLogout: nilIfZero(dto.LastLogout), Logins: dto.Logins,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting online state for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// CharacterShipDTO is GET /characters/{character_id}/ship. A 404 here for
// a docked character is DATA, not an error (roadmap edge case) —
// ParseCharacterShip/SyncCharacterShip are never called in that case at
// all; the caller (the KindSyncRoute worker) checks StatusCode == 404
// before reaching for these and treats it as "no change, no error, no
// breaker trip" (internal/esi.Client.Do already never trips the breaker
// on any 4xx — see its Do doc comment).
type CharacterShipDTO struct {
	ShipItemID int64  `json:"ship_item_id"`
	ShipName   string `json:"ship_name"`
	ShipTypeID int64  `json:"ship_type_id"`
}

func ParseCharacterShip(body []byte) (CharacterShipDTO, error) {
	var dto CharacterShipDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterShipDTO{}, fmt.Errorf("handlers: parsing character ship: %w", err)
	}
	return dto, nil
}

func SyncCharacterShip(ctx context.Context, s *store.Store, characterID int64, dto CharacterShipDTO) (SyncResult, error) {
	itemID, typeID, name := dto.ShipItemID, dto.ShipTypeID, dto.ShipName
	if _, err := s.UpsertCharacterShipOnly(ctx, gen.UpsertCharacterShipOnlyParams{
		CharacterID: characterID, ShipItemID: &itemID, ShipTypeID: &typeID, ShipName: &name,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting ship for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
