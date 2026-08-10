// Contract, contract-item and contract-bid sync (Phase 9). Owner-generic
// over app.contract's (owner_kind, owner_id, contract_id) PK — same
// rationale as wallet.go/market.go/asset_sync.go. Contract lists
// themselves were left to Phase 9 in full by 02_DATABASE_SCHEMA.md §5.2's
// table map; this file covers the list plus items/bids.
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

// ContractDTO mirrors GET /{owner}/{id}/contracts.
type ContractDTO struct {
	AcceptorID          *int64              `json:"acceptor_id,omitempty"`
	AssigneeID          *int64              `json:"assignee_id,omitempty"`
	Availability        string              `json:"availability"`
	Buyout              decimal.NullDecimal `json:"buyout"`
	Collateral          decimal.NullDecimal `json:"collateral"`
	ContractID          int64               `json:"contract_id"`
	DateAccepted        time.Time           `json:"date_accepted,omitempty"`
	DateCompleted       time.Time           `json:"date_completed,omitempty"`
	DateExpired         time.Time           `json:"date_expired"`
	DateIssued          time.Time           `json:"date_issued"`
	DaysToComplete      *int32              `json:"days_to_complete,omitempty"`
	EndLocationID       *int64              `json:"end_location_id,omitempty"`
	ForCorporation      bool                `json:"for_corporation"`
	IssuerCorporationID int64               `json:"issuer_corporation_id"`
	IssuerID            int64               `json:"issuer_id"`
	Price               decimal.NullDecimal `json:"price"`
	Reward              decimal.NullDecimal `json:"reward"`
	StartLocationID     *int64              `json:"start_location_id,omitempty"`
	Title               *string             `json:"title,omitempty"`
	Type                string              `json:"type"`
	Status              string              `json:"status"`
	Volume              *float64            `json:"volume,omitempty"`
}

func ParseContracts(body []byte) ([]ContractDTO, error) {
	var dto []ContractDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing contracts: %w", err)
	}
	return dto, nil
}

// SyncContracts upserts the full contract list for one owner. Contracts
// are never soft-deleted here (unlike assets) — a contract that vanishes
// from the live list has simply aged out of ESI's own retention window,
// which is not the same event as an item leaving an inventory; the
// existing row (with whatever terminal status it last reported) is kept.
func SyncContracts(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, contracts []ContractDTO) (SyncResult, error) {
	for _, c := range contracts {
		if _, err := s.UpsertContract(ctx, gen.UpsertContractParams{
			OwnerKind: ownerKind, OwnerID: ownerID, ContractID: c.ContractID, IssuerID: c.IssuerID,
			IssuerCorporationID: c.IssuerCorporationID, AssigneeID: c.AssigneeID, AcceptorID: c.AcceptorID,
			StartLocationID: c.StartLocationID, EndLocationID: c.EndLocationID, Type: c.Type, Status: c.Status,
			Title: c.Title, ForCorporation: c.ForCorporation, Availability: c.Availability,
			DateIssued: c.DateIssued, DateExpired: c.DateExpired, DateAccepted: nilIfZero(c.DateAccepted),
			DaysToComplete: c.DaysToComplete, DateCompleted: nilIfZero(c.DateCompleted),
			Price: c.Price, Reward: c.Reward, Collateral: c.Collateral, Buyout: c.Buyout, Volume: c.Volume,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contract %d for %s %d: %w", c.ContractID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(contracts))}, nil
}

// ContractItemDTO mirrors GET /{owner}/{id}/contracts/{contract_id}/items.
// A courier contract's item list is empty by design (roadmap edge case) —
// SyncContractItems treats a zero-length slice as a normal, successful
// sync of zero rows, never as a failure signal; callers must not
// special-case an empty items response as an error.
type ContractItemDTO struct {
	IsBlueprintCopy    *bool  `json:"is_blueprint_copy,omitempty"`
	IsIncluded         bool   `json:"is_included"`
	IsSingleton        bool   `json:"is_singleton"`
	ItemID             *int64 `json:"item_id,omitempty"`
	MaterialEfficiency *int16 `json:"material_efficiency,omitempty"`
	Quantity           int64  `json:"quantity"`
	RecordID           int64  `json:"record_id"`
	Runs               *int32 `json:"runs,omitempty"`
	TimeEfficiency     *int16 `json:"time_efficiency,omitempty"`
	TypeID             int32  `json:"type_id"`
}

func ParseContractItems(body []byte) ([]ContractItemDTO, error) {
	var dto []ContractItemDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing contract items: %w", err)
	}
	return dto, nil
}

func SyncContractItems(ctx context.Context, s *store.Store, ownerKind string, ownerID, contractID int64, items []ContractItemDTO) (SyncResult, error) {
	for _, it := range items {
		// raw_quantity: ESI overloads `quantity` itself as the "raw"
		// negative-sentinel form for BPCs/singleton stacks on some
		// contract-item responses (SeAT's legacy Contracts job carries the
		// same distinction under `raw_quantity`) — captured 1:1 here since
		// there's no separate field on this endpoint to disambiguate; both
		// columns get the same parsed value on purpose.
		if _, err := s.UpsertContractItem(ctx, gen.UpsertContractItemParams{
			OwnerKind: ownerKind, OwnerID: ownerID, ContractID: contractID, RecordID: it.RecordID,
			TypeID: it.TypeID, Quantity: it.Quantity, RawQuantity: &it.Quantity, IsSingleton: it.IsSingleton,
			IsIncluded: it.IsIncluded, IsBlueprintCopy: it.IsBlueprintCopy,
			MaterialEfficiency: it.MaterialEfficiency, TimeEfficiency: it.TimeEfficiency, Runs: it.Runs,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contract item %d of contract %d for %s %d: %w", it.RecordID, contractID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(items))}, nil
}

// ContractBidDTO mirrors GET /{owner}/{id}/contracts/{contract_id}/bids
// (auction contracts only).
type ContractBidDTO struct {
	Amount   decimal.Decimal `json:"amount"`
	BidID    int64           `json:"bid_id"`
	BidderID int64           `json:"bidder_id"`
	DateBid  time.Time       `json:"date_bid"`
}

func ParseContractBids(body []byte) ([]ContractBidDTO, error) {
	var dto []ContractBidDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing contract bids: %w", err)
	}
	return dto, nil
}

func SyncContractBids(ctx context.Context, s *store.Store, ownerKind string, ownerID, contractID int64, bids []ContractBidDTO) (SyncResult, error) {
	for _, b := range bids {
		if _, err := s.UpsertContractBid(ctx, gen.UpsertContractBidParams{
			OwnerKind: ownerKind, OwnerID: ownerID, ContractID: contractID, BidID: b.BidID,
			BidderID: b.BidderID, DateBid: b.DateBid, Amount: b.Amount,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contract bid %d of contract %d for %s %d: %w", b.BidID, contractID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(bids))}, nil
}
