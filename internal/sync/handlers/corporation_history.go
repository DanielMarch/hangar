package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ---- GET /corporations/{corporation_id}/alliancehistory ----

type CorporationAllianceHistoryDTO struct {
	AllianceID *int64    `json:"alliance_id,omitempty"`
	IsDeleted  *bool     `json:"is_deleted,omitempty"`
	RecordID   int64     `json:"record_id"`
	StartDate  time.Time `json:"start_date"`
}

func ParseCorporationAllianceHistory(body []byte) ([]CorporationAllianceHistoryDTO, error) {
	var dto []CorporationAllianceHistoryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation alliance history: %w", err)
	}
	return dto, nil
}

// SyncCorporationAllianceHistory inserts every entry — history is
// append-only upstream (a null alliance_id on the CURRENT entry means "not
// in an alliance right now", not an absent record), and InsertCorporationAllianceHistory's
// ON CONFLICT DO NOTHING makes a re-synced page idempotent without a prune
// step.
func SyncCorporationAllianceHistory(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationAllianceHistoryDTO) (SyncResult, error) {
	for _, r := range rows {
		isDeleted := false
		if r.IsDeleted != nil {
			isDeleted = *r.IsDeleted
		}
		if _, err := s.InsertCorporationAllianceHistory(ctx, gen.InsertCorporationAllianceHistoryParams{
			CorporationID: corporationID, RecordID: r.RecordID, AllianceID: r.AllianceID,
			IsDeleted: isDeleted, StartDate: r.StartDate,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: inserting alliance history record %d for corp %d: %w", r.RecordID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}
