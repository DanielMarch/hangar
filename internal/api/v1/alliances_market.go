// alliances_market.go implements SRS §6.4 (alliances & sovereignty) and
// §6.5 (market). Neither group has a matching permission in the closed RBAC
// vocabulary (internal/domain/vocabulary.go only defines
// characters.view/corporations.view/squads.*/admin.*/provisioning.*/
// alerting.*/api_tokens.*/webhooks.manage — nothing for alliances,
// sovereignty or markets) — a genuine SRS/RBAC-vocabulary gap this phase
// reports rather than invents an ad hoc permission name for. Every route
// below sits behind requireAuthenticated only (get(..., "", ...)), matching
// how this file's sibling files treat every other unauthenticated-adjacent
// case: a resolved session is still mandatory, just not a specific
// permission.
//
// GET /markets/{region_id}/orders and /markets/{region_id}/types have no
// backing table in the Tier-2 schema at all: app.market_order is
// owner-scoped (a character/corporation's OWN orders — SRS §6.2/§6.3), not
// the public, anonymous regional order book, which 02_DATABASE_SCHEMA.md
// never defines a table for (likely a deliberate MVP scope cut given its
// size — hundreds of thousands of rows per region). Both routes are
// registered so the OpenAPI surface matches SRS §6.5, but respond 501 with
// an explanation rather than fabricating data or silently 200ing an empty
// list. Reported in this phase's closing summary.
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
	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/alliances", "list-alliances", "Tracked alliances", allianceTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListAlliances(ctx)
			if err != nil {
				return nil, api.Internal("listing alliances", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[IDIn, ItemOut](hapi, deps, "", "/api/v1/alliances/{id}", "get-alliance", "One alliance", allianceTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppAlliance, error) { return deps.Store.GetAlliance(ctx, id) }))
	get[IDIn, CollectionOut](hapi, deps, "", "/api/v1/alliances/{id}/corporations", "list-alliance-corporations", "Member corporations", allianceTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporation, error) {
			return deps.Store.ListCorporationsByAlliance(ctx, &id)
		}))
	get[IDIn, CollectionOut](hapi, deps, "", "/api/v1/alliances/{id}/contacts", "list-alliance-contacts", "Contacts", allianceTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContact, error) {
			return deps.Store.ListContacts(ctx, string(domain.OwnerAlliance), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, "", "/api/v1/alliances/{id}/contacts/labels", "list-alliance-contact-labels", "Contact labels", allianceTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContactLabel, error) {
			return deps.Store.ListContactLabels(ctx, string(domain.OwnerAlliance), id)
		}))

	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/sovereignty/campaigns", "list-sovereignty-campaigns", "Active sovereignty campaigns", allianceTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSovereigntyCampaigns(ctx)
			if err != nil {
				return nil, api.Internal("listing sovereignty campaigns", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/sovereignty/systems", "list-sovereignty-systems", "Sovereignty by solar system", allianceTag,
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

	get[RegionOrdersIn, CollectionOut](hapi, deps, "", "/api/v1/markets/{region_id}/orders", "list-market-region-orders", "Regional order book (not backed — see file doc comment)", marketTag,
		func(ctx context.Context, _ *RegionOrdersIn) (*CollectionOut, error) {
			return nil, huma.Error501NotImplemented("the public regional order book has no backing table in the Tier-2 schema; only owner-scoped orders and per-type history are stored")
		})
	get[RegionHistoryIn, CollectionOut](hapi, deps, "", "/api/v1/markets/{region_id}/history", "list-market-region-history", "Price history for one type in one region", marketTag,
		func(ctx context.Context, in *RegionHistoryIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListMarketHistory(ctx, in.RegionID, in.TypeID, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing market history", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[RegionOrdersIn, CollectionOut](hapi, deps, "", "/api/v1/markets/{region_id}/types", "list-market-region-types", "Types traded in this region (not backed — see file doc comment)", marketTag,
		func(ctx context.Context, _ *RegionOrdersIn) (*CollectionOut, error) {
			return nil, huma.Error501NotImplemented("the regional traded-types index has no backing table in the Tier-2 schema")
		})
	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/markets/prices", "list-market-prices", "Global adjusted/average prices by type", marketTag,
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

type RegionHistoryIn struct {
	RegionID int32 `path:"region_id"`
	TypeID   int32 `query:"type_id" required:"true"`
}
