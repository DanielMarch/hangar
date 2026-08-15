// owner_shared.go holds the handler builders shared between
// characters.go (§6.2) and corporations.go (§6.3) for the owner-polymorphic
// resources both expose identically: assets, wallets and contracts. Each
// takes a domain.OwnerKind so the same handler serves both call sites.
package v1

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// farFuture and maxID are the start-of-set sentinel for the
// (time, id) < (X, Y) keyset queries — wallet journal, wallet transactions,
// mail and notifications. Start-of-set means "everything", which a
// DESC-ordered keyset expresses as "before the end of time".
//
// ── DEFECT B46 (PHASE 20.6): WHY THE SENTINEL IS A PAIR ──────────────────
// It used to be farFuture alone, and the single value was handed to BOTH
// parameters of the row comparison — which is how a timestamp arrived at a
// bigint column and produced 22P02 on every call. The sentinel for a
// two-column keyset is necessarily two values, one per column's own type.
//
// maxID is the maximum bigint rather than a merely large number because the
// comparison is lexicographic: the pair must be strictly greater than every
// representable row, and for a row that somehow carried farFuture as its
// date the id component is what decides. Anything less than the maximum
// would silently exclude such a row instead of including it.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

const maxID int64 = math.MaxInt64

// cursorTimeID decodes a (time, id)-keyed backward cursor into both of its
// components. A nil Cursor (no cursor supplied, or the "0" sentinel) means
// start-of-set — (farFuture, maxID), so the first page is the newest rows.
//
// A cursor that is PRESENT but does not carry both components is an error,
// not a silent restart. The previous helper returned farFuture for anything
// it could not decode, which turned every malformed cursor into "page 1"
// — and that is precisely how the notifications endpoint came to serve its
// first page forever (it encoded the whole row under the key "before" and
// then looked for "sent_at", found nothing, and restarted, with a 200 on
// every request). Rejecting it surfaces the bug at the first request
// instead of hiding it behind an infinite scroll that never advances.
func cursorTimeID(page api.PageRequest, timeKey, idKey string) (time.Time, int64, error) {
	if page.Cursor == nil {
		return farFuture, maxID, nil
	}
	rawTime, ok := page.Cursor[timeKey]
	if !ok {
		return time.Time{}, 0, fmt.Errorf("%w: cursor is missing %q", api.ErrCursorMalformed, timeKey)
	}
	s, ok := rawTime.(string)
	if !ok {
		return time.Time{}, 0, fmt.Errorf("%w: cursor %q is not a string", api.ErrCursorMalformed, timeKey)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("%w: cursor %q is not an RFC3339 timestamp", api.ErrCursorMalformed, timeKey)
	}
	rawID, ok := page.Cursor[idKey]
	if !ok {
		return time.Time{}, 0, fmt.Errorf("%w: cursor is missing %q", api.ErrCursorMalformed, idKey)
	}
	idStr, ok := rawID.(string)
	if !ok {
		return time.Time{}, 0, fmt.Errorf("%w: cursor %q is not a string", api.ErrCursorMalformed, idKey)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("%w: cursor %q is not an integer", api.ErrCursorMalformed, idKey)
	}
	return t, id, nil
}

// timeIDKeyset builds the cursor for the last row of a (time, id) page.
//
// The id is encoded as a STRING, not a JSON number. Keyset values round-trip
// through encoding/json, where every number becomes a float64 — exact only
// below 2^53. EVE journal and transaction ids are comfortably under that
// today, so a float64 would work and would keep working for years, and then
// would start rounding a cursor to a neighbouring row with no error
// anywhere. A decimal string is exact at every magnitude an int64 can hold,
// and a cursor is opaque to clients, so the representation costs nothing.
func timeIDKeyset(timeKey string, t time.Time, idKey string, id int64) api.Keyset {
	return api.Keyset{
		timeKey: t.Format(time.RFC3339Nano),
		idKey:   strconv.FormatInt(id, 10),
	}
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
		next = api.EncodeCursor(timeIDKeyset("sent_at", last.SentAt, "mail_id", last.MailID))
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
		before, beforeID, err := cursorTimeID(page, "date", "journal_id")
		if err != nil {
			return nil, api.PageError(err)
		}
		rows, err := deps.Store.ListWalletJournalPage(ctx, gen.ListWalletJournalPageParams{
			OwnerKind: string(owner), OwnerID: in.ID, Division: walletDivision(in),
			BeforeDate: before, BeforeJournalID: beforeID, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing wallet journal", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			last := rows[len(rows)-1]
			next = api.EncodeCursor(timeIDKeyset("date", last.Date, "journal_id", last.JournalID))
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
		before, beforeID, err := cursorTimeID(page, "date", "transaction_id")
		if err != nil {
			return nil, api.PageError(err)
		}
		rows, err := deps.Store.ListWalletTransactionsPage(ctx, gen.ListWalletTransactionsPageParams{
			OwnerKind: string(owner), OwnerID: in.ID, Division: walletDivision(in),
			BeforeDate: before, BeforeTransactionID: beforeID, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing wallet transactions", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			last := rows[len(rows)-1]
			next = api.EncodeCursor(timeIDKeyset("date", last.Date, "transaction_id", last.TransactionID))
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
