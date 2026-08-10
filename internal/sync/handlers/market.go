// Market order sync. SyncMarketOrders/SyncMarketOrderHistory were already
// owner-kind-generic as of Phase 8, same rationale as wallet.go, though
// Phase 8 only ever called them with ownerKind = "corporation"
// (worker/corporation.go). Phase 9 wires the character-side call sites
// into CharacterWorker's dispatch map (worker/character.go) — a
// dispatch-table entry, not a rewrite of these functions — and closes out
// 02_DATABASE_SCHEMA.md §5.2's broader "Market (4)" group in full
// (market_history.go, market_price.go: region/type history and global
// adjusted/average prices, neither owner-scoped).
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

// MarketOrderDTO is one element of GET /corporations/{id}/orders (or the
// character equivalent). `issued_by` (the character who placed the order)
// is a required field on the live spec and IS persisted, into
// app.market_order.issued_by (00034_phase9_market_issued_by.sql closes the
// gap Phase 8 reported and carried forward) — nullable because a corp
// order placed by a since-departed or since-unsynced character is a
// legitimate case; on a character's own orders it is always that same
// character, so it is never actually absent there in practice, but the
// column stays nullable rather than assuming that holds for every past and
// future spec revision. `is_corporation` does not appear on either owner's
// response at all; the caller supplies it (SyncMarketOrders'
// isCorporation parameter) since it is a property of WHICH endpoint
// answered, not of the order.
type MarketOrderDTO struct {
	Duration       int32               `json:"duration"`
	Escrow         decimal.NullDecimal `json:"escrow"`
	IsBuyOrder     *bool               `json:"is_buy_order,omitempty"`
	Issued         time.Time           `json:"issued"`
	IssuedBy       *int64              `json:"issued_by,omitempty"`
	LocationID     int64               `json:"location_id"`
	MinVolume      *int64              `json:"min_volume,omitempty"`
	OrderID        int64               `json:"order_id"`
	Price          decimal.Decimal     `json:"price"`
	Range          string              `json:"range"`
	RegionID       int32               `json:"region_id"`
	TypeID         int32               `json:"type_id"`
	VolumeRemain   int64               `json:"volume_remain"`
	VolumeTotal    int64               `json:"volume_total"`
	WalletDivision int16               `json:"wallet_division"`
}

func ParseMarketOrders(body []byte) ([]MarketOrderDTO, error) {
	var dto []MarketOrderDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing market orders: %w", err)
	}
	return dto, nil
}

// SyncMarketOrders upserts every open order and prunes any order_id for
// this owner not present in the current page — an order missing from the
// live list has closed/expired/cancelled (market.sql's
// DeleteMarketOrdersNotIn doc comment), which is why callers are expected
// to have already projected the outgoing set into market_order_history
// before pruning (Phase 9's job for the general case; this phase's
// worker does not yet do that projection — see worker/corporation.go).
func SyncMarketOrders(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, isCorporation bool, orders []MarketOrderDTO) (SyncResult, error) {
	ids := make([]int64, len(orders))
	for i, o := range orders {
		ids[i] = o.OrderID
		isBuy := false
		if o.IsBuyOrder != nil {
			isBuy = *o.IsBuyOrder
		}
		division := o.WalletDivision
		if _, err := s.UpsertMarketOrder(ctx, gen.UpsertMarketOrderParams{
			OwnerKind: ownerKind, OwnerID: ownerID, OrderID: o.OrderID, TypeID: o.TypeID,
			RegionID: o.RegionID, LocationID: o.LocationID, Range: o.Range, IsBuyOrder: isBuy,
			IsCorporation: isCorporation, Escrow: o.Escrow, Price: o.Price, VolumeTotal: o.VolumeTotal,
			VolumeRemain: o.VolumeRemain, MinVolume: o.MinVolume, Duration: o.Duration, Issued: o.Issued,
			WalletDivision: &division, IssuedBy: o.IssuedBy,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting market order %d for %s %d: %w", o.OrderID, ownerKind, ownerID, err)
		}
	}
	if err := s.DeleteMarketOrdersNotIn(ctx, ownerKind, ownerID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale market orders for %s %d: %w", ownerKind, ownerID, err)
	}
	return SyncResult{RowsAffected: int32(len(orders))}, nil
}

// MarketOrderHistoryDTO adds `state` (the "still active" vs
// "historical/delivered" distinction the roadmap warns against
// collapsing: 'cancelled'|'expired', an open vocabulary) over MarketOrderDTO.
type MarketOrderHistoryDTO struct {
	Duration       int32               `json:"duration"`
	Escrow         decimal.NullDecimal `json:"escrow"`
	IsBuyOrder     *bool               `json:"is_buy_order,omitempty"`
	Issued         time.Time           `json:"issued"`
	IssuedBy       *int64              `json:"issued_by,omitempty"`
	LocationID     int64               `json:"location_id"`
	MinVolume      *int64              `json:"min_volume,omitempty"`
	OrderID        int64               `json:"order_id"`
	Price          decimal.Decimal     `json:"price"`
	Range          string              `json:"range"`
	RegionID       int32               `json:"region_id"`
	State          string              `json:"state"`
	TypeID         int32               `json:"type_id"`
	VolumeRemain   int64               `json:"volume_remain"`
	VolumeTotal    int64               `json:"volume_total"`
	WalletDivision int16               `json:"wallet_division"`
}

func ParseMarketOrderHistory(body []byte) ([]MarketOrderHistoryDTO, error) {
	var dto []MarketOrderHistoryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing market order history: %w", err)
	}
	return dto, nil
}

func SyncMarketOrderHistory(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, isCorporation bool, orders []MarketOrderHistoryDTO) (SyncResult, error) {
	for _, o := range orders {
		isBuy := false
		if o.IsBuyOrder != nil {
			isBuy = *o.IsBuyOrder
		}
		division := o.WalletDivision
		if _, err := s.UpsertMarketOrderHistory(ctx, gen.UpsertMarketOrderHistoryParams{
			OwnerKind: ownerKind, OwnerID: ownerID, OrderID: o.OrderID, TypeID: o.TypeID,
			RegionID: o.RegionID, LocationID: o.LocationID, Range: o.Range, IsBuyOrder: isBuy,
			IsCorporation: isCorporation, Escrow: o.Escrow, Price: o.Price, VolumeTotal: o.VolumeTotal,
			VolumeRemain: o.VolumeRemain, MinVolume: o.MinVolume, Duration: o.Duration, Issued: o.Issued,
			State: o.State, WalletDivision: &division, IssuedBy: o.IssuedBy,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting market order history %d for %s %d: %w", o.OrderID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(orders))}, nil
}
