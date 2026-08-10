// Market order sync (Phase 8's explicit scope per the roadmap prompt seed:
// "market orders and order history") is owner-kind-generic, same rationale
// as wallet.go, but this phase only ever calls it with ownerKind =
// "corporation" — 02_DATABASE_SCHEMA.md §5.2's table map assigns the
// broader "Market (4)" group (which includes market_history/market_price,
// global reference data unrelated to any owner) to Phase 9 in full; the
// two owner-polymorphic order tables are the one piece of that group this
// phase's prompt seed explicitly calls out, so only the corp-owned call
// site is wired into worker/corporation.go. The functions below stay
// owner-generic so Phase 9's character-order sync is a dispatch-table
// entry, not a rewrite.
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
// is a required field on the live spec but app.market_order has no column
// for it — reported as a gap, parsed for field-loss coverage, not
// persisted (same precedent as corporation_structure.go's title role
// grants). `is_corporation` does not appear on either owner's response at
// all; the caller supplies it (SyncMarketOrders' isCorporation parameter)
// since it is a property of WHICH endpoint answered, not of the order.
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
			WalletDivision: &division,
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
			State: o.State, WalletDivision: &division,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting market order history %d for %s %d: %w", o.OrderID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(orders))}, nil
}
