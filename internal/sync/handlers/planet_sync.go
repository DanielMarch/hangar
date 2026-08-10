// Planetary interaction sync (Phase 9): colony list plus per-colony detail
// (pins/links/routes). Character-scoped only — PI has no corporation
// concept in ESI.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// PlanetColonyDTO mirrors one element of GET /characters/{id}/planets.
type PlanetColonyDTO struct {
	LastUpdate    time.Time `json:"last_update"`
	NumPins       int32     `json:"num_pins"`
	OwnerID       int64     `json:"owner_id"`
	PlanetID      int64     `json:"planet_id"`
	PlanetType    string    `json:"planet_type"`
	SolarSystemID int32     `json:"solar_system_id"`
	UpgradeLevel  int32     `json:"upgrade_level"`
}

func ParsePlanetColonies(body []byte) ([]PlanetColonyDTO, error) {
	var dto []PlanetColonyDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing planet colonies: %w", err)
	}
	return dto, nil
}

func SyncPlanetColonies(ctx context.Context, s *store.Store, characterID int64, colonies []PlanetColonyDTO) (SyncResult, error) {
	for _, c := range colonies {
		if _, err := s.UpsertPlanetColony(ctx, gen.UpsertPlanetColonyParams{
			CharacterID: characterID, PlanetID: c.PlanetID, SolarSystemID: c.SolarSystemID,
			PlanetType: c.PlanetType, OwnerID: c.OwnerID, LastUpdate: c.LastUpdate,
			UpgradeLevel: c.UpgradeLevel, NumPins: c.NumPins,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting planet colony %d for character %d: %w", c.PlanetID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(colonies))}, nil
}

// PlanetColonyDetailDTO mirrors GET /characters/{id}/planets/{planet_id}.
// `pins`, `links`, `routes` are stored verbatim as JSONB rather than
// normalised into their own tables — 02_DATABASE_SCHEMA.md §5.2 assigns
// this whole detail to a single `planet_colony_detail` row, and each
// array's element shape is deep and PI-specific (extractor heads, factory
// schematics, storage contents) with no cross-domain reuse that would
// justify separate tables the way contract items or mail recipients have.
type PlanetColonyDetailDTO struct {
	Links  []json.RawMessage `json:"links"`
	Pins   []json.RawMessage `json:"pins"`
	Routes []json.RawMessage `json:"routes"`
}

func ParsePlanetColonyDetail(body []byte) (PlanetColonyDetailDTO, error) {
	var dto PlanetColonyDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return PlanetColonyDetailDTO{}, fmt.Errorf("handlers: parsing planet colony detail: %w", err)
	}
	return dto, nil
}

func SyncPlanetColonyDetail(ctx context.Context, s *store.Store, characterID, planetID int64, dto PlanetColonyDetailDTO) (SyncResult, error) {
	pins, err := json.Marshal(dto.Pins)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: re-marshalling pins for planet %d of character %d: %w", planetID, characterID, err)
	}
	links, err := json.Marshal(dto.Links)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: re-marshalling links for planet %d of character %d: %w", planetID, characterID, err)
	}
	routes, err := json.Marshal(dto.Routes)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: re-marshalling routes for planet %d of character %d: %w", planetID, characterID, err)
	}
	if _, err := s.UpsertPlanetColonyDetail(ctx, gen.UpsertPlanetColonyDetailParams{
		CharacterID: characterID, PlanetID: planetID, Pins: pins, Links: links, Routes: routes,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting planet colony detail for planet %d of character %d: %w", planetID, characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
