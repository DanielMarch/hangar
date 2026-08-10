// Global adjusted/average prices per type (Phase 9): GET /markets/prices.
// Global reference data, not owner-scoped; completes
// 02_DATABASE_SCHEMA.md §5.2's "Market (4)" group.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/shopspring/decimal"
)

// MarketPriceDTO mirrors one element of GET /markets/prices.
type MarketPriceDTO struct {
	AdjustedPrice decimal.NullDecimal `json:"adjusted_price"`
	AveragePrice  decimal.NullDecimal `json:"average_price"`
	TypeID        int32               `json:"type_id"`
}

func ParseMarketPrices(body []byte) ([]MarketPriceDTO, error) {
	var dto []MarketPriceDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing market prices: %w", err)
	}
	return dto, nil
}

func SyncMarketPrices(ctx context.Context, s *store.Store, prices []MarketPriceDTO) (SyncResult, error) {
	for _, p := range prices {
		if _, err := s.UpsertMarketPrice(ctx, p.TypeID, p.AdjustedPrice, p.AveragePrice); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting market price for type %d: %w", p.TypeID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(prices))}, nil
}
