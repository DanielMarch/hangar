// alliances_market.go implements SRS §6.4 (alliances & sovereignty) and
// §6.5 (market).
//
// PERMISSIONS — closed in Phase 15.1. Phase 15 found the closed RBAC
// vocabulary had no permission covering either group and shipped every
// route here on requireAuthenticated as a documented stopgap. Phase 15.1
// added alliances.view, sovereignty.view and markets.view
// (internal/domain/vocabulary.go) and each route below now names one.
package v1

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const allianceTag = "alliances"
const marketTag = "market"

func registerAlliancesAndMarket(hapi huma.API, deps api.Deps) {
	get[EmptyIn, CollectionOut](hapi, deps, "alliances.view", "/api/v1/alliances", "list-alliances", "Tracked alliances", allianceTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListAlliances(ctx)
			if err != nil {
				return nil, api.Internal("listing alliances", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[IDIn, ItemOut](hapi, deps, "alliances.view", "/api/v1/alliances/{id}", "get-alliance", "One alliance", allianceTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppAlliance, error) { return deps.Store.GetAlliance(ctx, id) }))
	get[IDIn, CollectionOut](hapi, deps, "alliances.view", "/api/v1/alliances/{id}/corporations", "list-alliance-corporations", "Member corporations", allianceTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporation, error) {
			return deps.Store.ListCorporationsByAlliance(ctx, &id)
		}))
	get[IDIn, CollectionOut](hapi, deps, "alliances.view", "/api/v1/alliances/{id}/contacts", "list-alliance-contacts", "Contacts", allianceTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContact, error) {
			return deps.Store.ListContacts(ctx, string(domain.OwnerAlliance), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, "alliances.view", "/api/v1/alliances/{id}/contacts/labels", "list-alliance-contact-labels", "Contact labels", allianceTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContactLabel, error) {
			return deps.Store.ListContactLabels(ctx, string(domain.OwnerAlliance), id)
		}))

	get[EmptyIn, CollectionOut](hapi, deps, "sovereignty.view", "/api/v1/sovereignty/campaigns", "list-sovereignty-campaigns", "Active sovereignty campaigns", allianceTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSovereigntyCampaigns(ctx)
			if err != nil {
				return nil, api.Internal("listing sovereignty campaigns", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, "sovereignty.view", "/api/v1/sovereignty/systems", "list-sovereignty-systems", "Sovereignty by solar system", allianceTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSovereigntySystems(ctx)
			if err != nil {
				return nil, api.Internal("listing sovereignty systems", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/orders", "list-character-orders", "Open market orders", marketTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMarketOrder, error) {
			return deps.Store.ListMarketOrdersByOwner(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/orders/history", "list-character-order-history", "Historical (closed/expired) orders", marketTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMarketOrderHistory, error) {
			return deps.Store.ListMarketOrderHistoryByOwner(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/orders", "list-corporation-orders", "Open market orders", marketTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMarketOrder, error) {
			return deps.Store.ListMarketOrdersByOwner(ctx, string(domain.OwnerCorporation), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/orders/history", "list-corporation-order-history", "Historical (closed/expired) orders", marketTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMarketOrderHistory, error) {
			return deps.Store.ListMarketOrderHistoryByOwner(ctx, string(domain.OwnerCorporation), id)
		}))

	get[RegionOrdersPageIn, CollectionOut](hapi, deps, "markets.view", "/api/v1/markets/{region_id}/orders", "list-market-region-orders", "Orders HANGAR has synced for tracked owners in this region (keyset-paginated)", marketTag,
		func(ctx context.Context, in *RegionOrdersPageIn) (*CollectionOut, error) {
			page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
			if err != nil {
				return nil, api.PageError(err)
			}
			rows, err := deps.Store.ListMarketOrdersByRegion(ctx, in.RegionID, cursorAfterID(page, "order_id"), page.Limit)
			if err != nil {
				return nil, api.Internal("listing regional market orders", err)
			}
			data := rowSliceOf(rows)
			next := api.ZeroSentinel
			if len(rows) == int(page.Limit) {
				next = api.EncodeCursor(api.Keyset{"order_id": float64(rows[len(rows)-1].OrderID)})
			}
			return &CollectionOut{Body: api.Collection[map[string]any]{
				Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
			}}, nil
		})
	get[RegionHistoryIn, CollectionOut](hapi, deps, "markets.view", "/api/v1/markets/{region_id}/history", "list-market-region-history", "Price history for one type in one region", marketTag,
		func(ctx context.Context, in *RegionHistoryIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListMarketHistory(ctx, in.RegionID, in.TypeID, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing market history", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[RegionOrdersIn, CollectionOut](hapi, deps, "markets.view", "/api/v1/markets/{region_id}/types", "list-market-region-types", "Distinct type ids present in this region's synced orders", marketTag,
		func(ctx context.Context, in *RegionOrdersIn) (*CollectionOut, error) {
			typeIDs, err := deps.Store.ListMarketTypesByRegion(ctx, in.RegionID)
			if err != nil {
				return nil, api.Internal("listing regional market types", err)
			}
			data := make([]map[string]any, len(typeIDs))
			for i, id := range typeIDs {
				data[i] = map[string]any{"type_id": id}
			}
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, "markets.view", "/api/v1/markets/prices", "list-market-prices", "Global adjusted/average prices by type", marketTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListMarketPrices(ctx)
			if err != nil {
				return nil, api.Internal("listing market prices", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
}

type RegionOrdersIn struct {
	RegionID int32 `path:"region_id"`
}

type RegionOrdersPageIn struct {
	RegionID int32  `path:"region_id"`
	After    string `query:"after"`
	Before   string `query:"before"`
	Limit    int32  `query:"limit" default:"50"`
}

type RegionHistoryIn struct {
	RegionID int32 `path:"region_id"`
	TypeID   int32 `query:"type_id" required:"true"`
}
