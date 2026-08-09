package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterMedalDTO is one element of GET
// /characters/{character_id}/medals (CharactersCharacterIdMedalsGet).
// Graphics are parsed for field-loss coverage but not persisted — no
// table models a medal's cosmetic layout, and nothing in Appendix A's
// capability list needs it.
type CharacterMedalDTO struct {
	CorporationID int64                      `json:"corporation_id"`
	Date          time.Time                  `json:"date"`
	Description   string                     `json:"description"`
	Graphics      []CharacterMedalGraphicDTO `json:"graphics"`
	IssuerID      int64                      `json:"issuer_id"`
	MedalID       int64                      `json:"medal_id"`
	Reason        string                     `json:"reason"`
	Status        string                     `json:"status"`
	Title         string                     `json:"title"`
}

type CharacterMedalGraphicDTO struct {
	Color   *int64 `json:"color,omitempty"`
	Graphic string `json:"graphic"`
	Layer   int64  `json:"layer"`
	Part    int64  `json:"part"`
}

func ParseCharacterMedals(body []byte) ([]CharacterMedalDTO, error) {
	var dto []CharacterMedalDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character medals: %w", err)
	}
	return dto, nil
}

// SyncCharacterMedals upserts both the medal DEFINITION (title/description
// only — created_at/creator_id are Phase 8's to fill, and UpsertMedal's
// COALESCE means this call can never clobber them) and the issuance
// record. Issuances are immutable once recorded, so nothing here prunes —
// a medal, once issued, is never un-issued via this endpoint.
func SyncCharacterMedals(ctx context.Context, s *store.Store, characterID int64, medals []CharacterMedalDTO) (SyncResult, error) {
	for _, m := range medals {
		title, description := m.Title, m.Description
		if _, err := s.UpsertMedal(ctx, gen.UpsertMedalParams{
			CorporationID: m.CorporationID, MedalID: m.MedalID, Title: title, Description: &description,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting medal definition %d: %w", m.MedalID, err)
		}
		reason, status := m.Reason, m.Status
		if _, err := s.InsertMedalIssued(ctx, gen.InsertMedalIssuedParams{
			CorporationID: m.CorporationID, MedalID: m.MedalID, CharacterID: characterID,
			Reason: &reason, Status: &status, IssuerID: m.IssuerID, IssuedAt: m.Date,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: inserting medal issuance %d for character %d: %w", m.MedalID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(medals))}, nil
}
