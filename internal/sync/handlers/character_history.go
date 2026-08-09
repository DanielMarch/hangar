package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterCorporationHistoryDTO is one element of GET
// /characters/{character_id}/corporationhistory. Note: this endpoint
// requires no scope at all (security: null in the spec) — it's public
// data.
type CharacterCorporationHistoryDTO struct {
	CorporationID int64     `json:"corporation_id"`
	IsDeleted     *bool     `json:"is_deleted,omitempty"`
	RecordID      int64     `json:"record_id"`
	StartDate     time.Time `json:"start_date"`
}

func ParseCharacterCorporationHistory(body []byte) ([]CharacterCorporationHistoryDTO, error) {
	var dto []CharacterCorporationHistoryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character corporation history: %w", err)
	}
	return dto, nil
}

// SyncCharacterCorporationHistory inserts every history record ESI
// reports. Append-only per InsertCharacterCorporationHistory's own
// ON CONFLICT DO NOTHING (Phase 1b, db/queries/character_history.sql) — a
// record_id, once seen, is never re-evaluated for is_deleted changes by
// this call. No pruning: history never shrinks.
func SyncCharacterCorporationHistory(ctx context.Context, s *store.Store, characterID int64, entries []CharacterCorporationHistoryDTO) (SyncResult, error) {
	inserted := 0
	for _, e := range entries {
		isDeleted := false
		if e.IsDeleted != nil {
			isDeleted = *e.IsDeleted
		}
		_, err := s.InsertCharacterCorporationHistory(ctx, gen.InsertCharacterCorporationHistoryParams{
			CharacterID: characterID, RecordID: e.RecordID, CorporationID: e.CorporationID,
			IsDeleted: isDeleted, StartDate: e.StartDate,
		})
		if ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: inserting corp history record %d for character %d: %w", e.RecordID, characterID, err)
		}
		if err == nil {
			inserted++ // nil (not ErrNoRows): the record was genuinely new
		}
	}
	return SyncResult{RowsAffected: int32(inserted)}, nil
}
