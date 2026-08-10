// Package handlers' wallet sync is owner-kind-generic (character or
// corporation) by design — 02_DATABASE_SCHEMA.md §5.1's whole point is that
// the eleven owner-polymorphic concepts, wallets among them, need exactly
// one implementation for both /characters/{id}/... and
// /corporations/{id}/.... Every DTO field that is money uses
// shopspring/decimal directly, never float64 (Principle 9) — decimal.Decimal
// and decimal.NullDecimal both unmarshal straight from a JSON numeric
// literal without a float64 detour.
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

// ---- GET /{owner}/{id}/wallet(s) — balance ----

// WalletBalanceDTO is one element of GET
// /corporations/{corporation_id}/wallets (an array of divisions) or the
// single top-level number GET /characters/{character_id}/wallet returns
// (wrapped into a synthetic one-element slice with Division: 1 by the
// caller — see ParseCharacterWalletBalance below).
type WalletBalanceDTO struct {
	Balance  decimal.Decimal `json:"balance"`
	Division int16           `json:"division"`
}

func ParseCorporationWalletBalances(body []byte) ([]WalletBalanceDTO, error) {
	var dto []WalletBalanceDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation wallet balances: %w", err)
	}
	return dto, nil
}

// ParseCharacterWalletBalance parses GET /characters/{character_id}/wallet,
// which is a bare number (a character has one wallet, always division 1),
// unlike the corporation array.
func ParseCharacterWalletBalance(body []byte) (WalletBalanceDTO, error) {
	var balance decimal.Decimal
	if err := json.Unmarshal(body, &balance); err != nil {
		return WalletBalanceDTO{}, fmt.Errorf("handlers: parsing character wallet balance: %w", err)
	}
	return WalletBalanceDTO{Balance: balance, Division: 1}, nil
}

func SyncWalletBalances(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, balances []WalletBalanceDTO) (SyncResult, error) {
	for _, b := range balances {
		if _, err := s.UpsertWalletBalance(ctx, gen.UpsertWalletBalanceParams{
			OwnerKind: ownerKind, OwnerID: ownerID, Division: b.Division, Balance: b.Balance,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting wallet balance division %d for %s %d: %w", b.Division, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(balances))}, nil
}

// ---- GET /{owner}/{id}/wallet(s)/{division}/journal ----
// Page-paginated with X-Pages (01_ARCHITECTURE.md §5.9, roadmap edge
// case) — the multi-page walk and Last-Modified torn-set check live in
// worker/corporation.go's fetchAllPages, not here; this Sync function
// receives the already-assembled, already-validated full page set.

// WalletJournalEntryDTO is one element of GET .../wallet/journal (character:
// journal_id is exposed as "id"; corporation:
// /corporations/{id}/wallets/{division}/journal, same field name).
type WalletJournalEntryDTO struct {
	Amount        decimal.NullDecimal `json:"amount"`
	Balance       decimal.NullDecimal `json:"balance"`
	ContextID     *int64              `json:"context_id,omitempty"`
	ContextIDType *string             `json:"context_id_type,omitempty"`
	Date          time.Time           `json:"date"`
	Description   string              `json:"description"`
	FirstPartyID  *int64              `json:"first_party_id,omitempty"`
	ID            int64               `json:"id"`
	Reason        *string             `json:"reason,omitempty"`
	RefType       string              `json:"ref_type"`
	SecondPartyID *int64              `json:"second_party_id,omitempty"`
	Tax           decimal.NullDecimal `json:"tax"`
	TaxReceiverID *int64              `json:"tax_receiver_id,omitempty"`
}

func ParseWalletJournalPage(body []byte) ([]WalletJournalEntryDTO, error) {
	var dto []WalletJournalEntryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing wallet journal page: %w", err)
	}
	return dto, nil
}

// SyncWalletJournal upserts every entry. `ref_type` is an open vocabulary
// (roadmap edge case) — app.open_vocabulary tracking of unseen values is
// the sync engine's cross-cutting concern (internal/sync/normalize), not
// duplicated per domain here; the row itself is always stored regardless.
func SyncWalletJournal(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, division int16, entries []WalletJournalEntryDTO) (SyncResult, error) {
	for _, e := range entries {
		if _, err := s.UpsertWalletJournalEntry(ctx, gen.UpsertWalletJournalEntryParams{
			OwnerKind: ownerKind, OwnerID: ownerID, Division: division, JournalID: e.ID, RefType: e.RefType,
			Amount: e.Amount, Balance: e.Balance, Tax: e.Tax, TaxReceiverID: e.TaxReceiverID,
			FirstPartyID: e.FirstPartyID, SecondPartyID: e.SecondPartyID, ContextID: e.ContextID,
			ContextIDType: e.ContextIDType, Reason: e.Reason, Description: e.Description, Date: e.Date,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting journal entry %d for %s %d division %d: %w", e.ID, ownerKind, ownerID, division, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(entries))}, nil
}

// ---- GET /{owner}/{id}/wallet(s)/{division}/transactions ----
// Cursor-paginated via `from_id`, not page/X-Pages (confirmed against the
// live embedded spec: this operation declares no X-Pages response header,
// unlike journal) — Phase 8 syncs the single most-recent page ESI returns
// by default; walking further back via repeated from_id requests is not
// implemented in this phase.

type WalletTransactionDTO struct {
	ClientID      int64           `json:"client_id"`
	Date          time.Time       `json:"date"`
	IsBuy         bool            `json:"is_buy"`
	IsPersonal    *bool           `json:"is_personal,omitempty"`
	JournalRefID  int64           `json:"journal_ref_id"`
	LocationID    int64           `json:"location_id"`
	Quantity      int64           `json:"quantity"`
	TransactionID int64           `json:"transaction_id"`
	TypeID        int32           `json:"type_id"`
	UnitPrice     decimal.Decimal `json:"unit_price"`
}

func ParseWalletTransactionsPage(body []byte) ([]WalletTransactionDTO, error) {
	var dto []WalletTransactionDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing wallet transactions page: %w", err)
	}
	return dto, nil
}

func SyncWalletTransactions(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, division int16, entries []WalletTransactionDTO) (SyncResult, error) {
	for _, e := range entries {
		clientID, journalRefID := e.ClientID, e.JournalRefID
		if _, err := s.UpsertWalletTransaction(ctx, gen.UpsertWalletTransactionParams{
			OwnerKind: ownerKind, OwnerID: ownerID, Division: division, TransactionID: e.TransactionID,
			ClientID: &clientID, Date: e.Date, IsBuy: e.IsBuy, IsPersonal: e.IsPersonal,
			JournalRefID: &journalRefID, LocationID: e.LocationID, Quantity: e.Quantity,
			TypeID: e.TypeID, UnitPrice: e.UnitPrice,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting transaction %d for %s %d division %d: %w", e.TransactionID, ownerKind, ownerID, division, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(entries))}, nil
}
