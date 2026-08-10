// owner_shared.go holds the handler builders shared between
// characters.go (§6.2) and corporations.go (§6.3) for the owner-polymorphic
// resources both expose identically: assets, wallets and contracts. Each
// takes a domain.OwnerKind so the same handler serves both call sites.
package v1

import (
	"context"
	"time"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// farFuture is the "no upper bound yet" sentinel for the (date, id) < (X, Y)
// keyset queries (wallet journal/transactions, mail, notifications):
// start-of-set means "everything", which these DESC-ordered queries
// express as "before the end of time".
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// cursorTime decodes a (date, id)-keyed backward cursor's date component.
// A nil Cursor (no cursor supplied, or the "0" sentinel) means start-of-set
// — farFuture, so the first page is the newest rows.
func cursorTime(page api.PageRequest, key string) time.Time {
	if page.Cursor == nil {
		return farFuture
	}
	if raw, ok := page.Cursor[key]; ok {
		if s, ok := raw.(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t
			}
		}
	}
	return farFuture
}

// cursorAfterID decodes a forward, integer-id keyset cursor (assets,
// contracts). No cursor / the "0" sentinel means "from the start" — id 0,
// since every real EVE identifier is positive.
func cursorAfterID(page api.PageRequest, key string) int64 {
	if page.Cursor == nil {
		return 0
	}
	if raw, ok := page.Cursor[key]; ok {
		if f, ok := raw.(float64); ok {
			return int64(f)
		}
	}
	return 0
}

func mailCollection(rows []gen.AppMailHeader, limit int32) *CollectionOut {
	data := rowSliceOf(rows)
	next := api.ZeroSentinel
	if len(rows) == int(limit) {
		last := rows[len(rows)-1]
		next = api.EncodeCursor(api.Keyset{"sent_at": last.SentAt.Format(time.RFC3339Nano)})
	}
	return &CollectionOut{Body: api.Collection[map[string]any]{
		Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: limit}, Sync: api.Sync{},
	}}
}

// ---- assets ----

func assetsHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *IDPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *IDPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		after := cursorAfterID(page, "item_id")
		rows, err := deps.Store.ListAssetsByOwner(ctx, gen.ListAssetsByOwnerParams{
			OwnerKind: string(owner), OwnerID: in.ID, AfterItemID: after, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing assets", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			next = api.EncodeCursor(api.Keyset{"item_id": float64(rows[len(rows)-1].ItemID)})
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
		}}, nil
	}
}

func assetTreeHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *AssetTreeIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *AssetTreeIn) (*CollectionOut, error) {
		rows, err := deps.Store.AssetTree(ctx, domain.Owner{Kind: owner, ID: in.ID}, in.LocationID, 10)
		if err != nil {
			return nil, api.Internal("building asset tree", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

// ---- wallet ----

func walletDivision(in *WalletPageIn) int16 {
	return in.Division
}

func walletJournalHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *WalletPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *WalletPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		before := cursorTime(page, "date")
		rows, err := deps.Store.ListWalletJournalPage(ctx, gen.ListWalletJournalPageParams{
			OwnerKind: string(owner), OwnerID: in.ID, Division: walletDivision(in),
			BeforeDate: before, BeforeJournalID: before, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing wallet journal", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			next = api.EncodeCursor(api.Keyset{"date": rows[len(rows)-1].Date.Format(time.RFC3339Nano)})
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
		}}, nil
	}
}

func walletTransactionsHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *WalletPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *WalletPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		before := cursorTime(page, "date")
		rows, err := deps.Store.ListWalletTransactionsPage(ctx, gen.ListWalletTransactionsPageParams{
			OwnerKind: string(owner), OwnerID: in.ID, Division: walletDivision(in),
			BeforeDate: before, BeforeTransactionID: before, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing wallet transactions", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			next = api.EncodeCursor(api.Keyset{"date": rows[len(rows)-1].Date.Format(time.RFC3339Nano)})
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
		}}, nil
	}
}

// ---- contracts ----

func contractsHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *IDPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *IDPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		after := cursorAfterID(page, "contract_id")
		rows, err := deps.Store.ListContractsPage(ctx, gen.ListContractsPageParams{
			OwnerKind: string(owner), OwnerID: in.ID, AfterContractID: after, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing contracts", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			next = api.EncodeCursor(api.Keyset{"contract_id": float64(rows[len(rows)-1].ContractID)})
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
		}}, nil
	}
}

func contractItemsHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *SubIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*CollectionOut, error) {
		rows, err := deps.Store.ListContractItems(ctx, string(owner), in.ID, in.SubID)
		if err != nil {
			return nil, api.Internal("listing contract items", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func contractBidsHandler(deps api.Deps, owner domain.OwnerKind) func(context.Context, *SubIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*CollectionOut, error) {
		rows, err := deps.Store.ListContractBids(ctx, string(owner), in.ID, in.SubID)
		if err != nil {
			return nil, api.Internal("listing contract bids", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}
