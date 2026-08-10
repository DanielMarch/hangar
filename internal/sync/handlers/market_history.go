// Region/type market history (Phase 9): the largest partitioned table in
// the schema (roadmap edge case) — bulk, region-scoped, one row per
// (region_id, type_id, date). Global reference data, not owner-scoped;
// completes 02_DATABASE_SCHEMA.md §5.2's "Market (4)" group alongside
// market.go's owner-polymorphic order tables and market_price.go.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/shopspring/decimal"
)

// MarketHistoryDTO mirrors one element of GET /markets/{region_id}/history.
type MarketHistoryDTO struct {
	Average    decimal.NullDecimal `json:"average"`
	Date       string              `json:"date"` // ESI returns a date-only string, e.g. "2026-08-01"
	Highest    decimal.NullDecimal `json:"highest"`
	Lowest     decimal.NullDecimal `json:"lowest"`
	OrderCount int64               `json:"order_count"`
	Volume     int64               `json:"volume"`
}

func ParseMarketHistory(body []byte) ([]MarketHistoryDTO, error) {
	var dto []MarketHistoryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing market history: %w", err)
	}
	return dto, nil
}

// SyncMarketHistory upserts one region/type's full daily history page.
// typeID comes from the caller (the endpoint is scoped by
// {region_id}/history?type_id=..., not carried in the response body).
func SyncMarketHistory(ctx context.Context, s *store.Store, regionID, typeID int32, history []MarketHistoryDTO) (SyncResult, error) {
	for _, h := range history {
		date, err := time.Parse("2006-01-02", h.Date)
		if err != nil {
			return SyncResult{}, fmt.Errorf("handlers: parsing market history date %q for region %d type %d: %w", h.Date, regionID, typeID, err)
		}
		if _, err := s.UpsertMarketHistory(ctx, gen.UpsertMarketHistoryParams{
			RegionID: regionID, TypeID: typeID, Date: pgDate(date), Average: h.Average,
			Highest: h.Highest, Lowest: h.Lowest, OrderCount: h.OrderCount, Volume: h.Volume,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting market history for region %d type %d date %s: %w", regionID, typeID, h.Date, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(history))}, nil
}
