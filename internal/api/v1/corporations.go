// corporations.go implements SRS §6.3 — every /api/v1/corporations/{id}/...
// route, gated by "corporations.view" (the closed RBAC vocabulary has no
// finer per-sub-resource permission, same situation as characters.go).
//
// starbasesHandler/structuresHandler additionally demonstrate the
// blocked_by_pin contract (roadmap Phase 15 edge cases / SRS §6 design
// notes: "blocked_by_pin data renders as unavailable with an
// administrator-facing explanation, never as an empty result"):
// starbases and structures are exactly the resources the design notes
// call out ("required by the fuel-low alert"), so if the upstream ESI
// route backing them is currently gated by internal/esi/catalogue's
// compatibility pin, the response is api.UnavailableCollection, not an
// empty list.
package v1

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const corpTag = "corporations"

// bountyRefTypes / piRefTypes are the app.wallet_journal ref_type values
// SRS §6.3's /ledger/bounties and /ledger/pi aggregate over.
//
// ref_type is an OPEN vocabulary (Principle 14): CCP adds values without
// notice, so these are a best-known starting set, NOT a closed
// classification — an unrecognised bounty-like ref_type simply won't be
// counted rather than breaking the query, and extending these slices is
// the whole change needed when one appears. They live in Go rather than
// SQL for exactly that reason (see AggregateWalletJournalByRefType).
var bountyRefTypes = []string{
	"bounty_prize",
	"bounty_prizes",
	"bounty_prize_corporation_tax",
}

var piRefTypes = []string{
	"planetary_import_tax",
	"planetary_export_tax",
}

const permCorpView = "corporations.view"

func registerCorporations(hapi huma.API, deps api.Deps) {
	get[IDIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}", "get-corporation", "Corporation sheet", corpTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCorporation, error) {
			return deps.Store.GetCorporation(ctx, id)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/members", "list-corporation-members", "Members", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationMember, error) {
			return deps.Store.ListCorporationMembers(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/member-tracking", "list-corporation-member-tracking", "Member tracking (last login, location, ship)", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationMemberTracking, error) {
			return deps.Store.ListCorporationMemberTracking(ctx, id)
		}))
	// PHASE 15.1: /members/limit is now backed. Phase 15 could not register
	// it because app.corporation had member_count but no member_limit;
	// 00040_phase15_1_defect_closure.sql adds the column and
	// handlers.SyncCorporationMemberLimit populates it from the upstream
	// route's bare-integer response.
	get[IDIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/members/limit", "get-corporation-member-limit", "Maximum members this corporation may hold", corpTag,
		func(ctx context.Context, in *IDIn) (*ItemOut, error) {
			corp, err := deps.Store.GetCorporation(ctx, in.ID)
			if err != nil {
				return nil, api.NotFound("corporation")
			}
			data := map[string]any{
				"corporation_id": corp.CorporationID,
				"member_limit":   corp.MemberLimit,
				"member_count":   corp.MemberCount,
			}
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/members/titles", "list-corporation-member-titles", "Title assignment for every member (corp-wide)", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationMemberTitle, error) {
			return deps.Store.ListAllCorporationMemberTitles(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/roles", "list-corporation-roles", "Roles held across every member (corp-wide)", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationRole, error) {
			return deps.Store.ListAllCorporationRoles(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/roles/history", "list-corporation-role-history", "Role change history", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationRoleHistory, error) {
			return deps.Store.ListCorporationRoleHistory(ctx, id, api.MaxLimit)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/titles", "list-corporation-titles", "Title catalogue", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationTitle, error) {
			return deps.Store.ListCorporationTitles(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/divisions", "list-corporation-divisions", "Wallet/hangar division names", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationDivision, error) {
			return deps.Store.ListCorporationDivisions(ctx, id)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/wallets", "list-corporation-wallets", "Wallet balances by division", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppWalletBalance, error) {
			return deps.Store.ListWalletBalances(ctx, string(domain.OwnerCorporation), id)
		}))
	get[CorpWalletPageIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/wallets/{division}/journal", "list-corporation-wallet-journal", "One division's wallet journal (keyset-paginated)", corpTag, corpWalletJournalHandler(deps))
	get[CorpWalletPageIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/wallets/{division}/transactions", "list-corporation-wallet-transactions", "One division's wallet transactions (keyset-paginated)", corpTag, corpWalletTransactionsHandler(deps))

	get[IDPageIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/assets", "list-corporation-assets", "Asset list (keyset-paginated)", corpTag, assetsHandler(deps, domain.OwnerCorporation))
	get[AssetTreeIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/assets/tree/{location_id}", "get-corporation-asset-tree", "Recursive asset tree under one location", corpTag, assetTreeHandler(deps, domain.OwnerCorporation))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/projects", "list-corporation-projects", "Corp projects", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationProject, error) {
			return deps.Store.ListCorporationProjects(ctx, id)
		}))
	get[CorpProjectIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/projects/{project_id}", "get-corporation-project", "One project", corpTag, projectDetailHandler(deps))
	get[CorpProjectIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/projects/{project_id}/contributors", "list-corporation-project-contributors", "One project's contributors", corpTag, projectContributorsHandler(deps))
	get[ProjectContributionIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/projects/{project_id}/contribution/{character_id}", "get-corporation-project-contribution", "One member's contribution to one project", corpTag, projectContributionHandler(deps))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/structures", "list-corporation-structures", "Upwell structures", corpTag, structuresHandler(deps))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/structures/skyhooks", "list-corporation-skyhooks", "Skyhooks", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationSkyhook, error) {
			return deps.Store.ListCorporationSkyhooks(ctx, id)
		}))
	get[SubIDIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/structures/skyhooks/{sub_id}", "get-corporation-skyhook", "One skyhook", corpTag, skyhookDetailHandler(deps))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/structures/sovereignty-hubs", "list-corporation-sovereignty-hubs", "Sovereignty hubs", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationSovereigntyHub, error) {
			return deps.Store.ListCorporationSovereigntyHubs(ctx, id)
		}))
	get[SubIDIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/structures/sovereignty-hubs/{sub_id}", "get-corporation-sovereignty-hub", "One sovereignty hub", corpTag, sovHubDetailHandler(deps))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/starbases", "list-corporation-starbases", "Starbases (POS) — fuel bay and settings", corpTag, starbasesHandler(deps))
	get[SubIDIn, ItemOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/starbases/{sub_id}", "get-corporation-starbase", "One starbase", corpTag, starbaseDetailHandler(deps))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/customs-offices", "list-corporation-customs-offices", "Customs offices", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationCustomsOffice, error) {
			return deps.Store.ListCorporationCustomsOffices(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/containers/logs", "list-corporation-container-logs", "Container access audit log", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationContainerLog, error) {
			return deps.Store.ListCorporationContainerLog(ctx, id, api.MaxLimit)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/facilities", "list-corporation-facilities", "Facilities", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationFacility, error) {
			return deps.Store.ListCorporationFacilities(ctx, id)
		}))

	get[IDPageIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/contracts", "list-corporation-contracts", "Contracts (keyset-paginated)", corpTag, contractsHandler(deps, domain.OwnerCorporation))
	get[SubIDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/contracts/{sub_id}/items", "list-corporation-contract-items", "Items on one contract", corpTag, contractItemsHandler(deps, domain.OwnerCorporation))
	get[SubIDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/contracts/{sub_id}/bids", "list-corporation-contract-bids", "Bids on one auction contract", corpTag, contractBidsHandler(deps, domain.OwnerCorporation))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/industry/jobs", "list-corporation-industry-jobs", "Industry jobs", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppIndustryJob, error) {
			return deps.Store.ListIndustryJobsByOwner(ctx, string(domain.OwnerCorporation), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/blueprints", "list-corporation-blueprints", "Blueprints", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppBlueprint, error) {
			return deps.Store.ListBlueprintsByOwner(ctx, string(domain.OwnerCorporation), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/killmails", "list-corporation-killmails", "Killmails", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppKillmail, error) {
			return deps.Store.ListKillmailsByOwner(ctx, string(domain.OwnerCorporation), id, api.MaxLimit)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/mining/extractions", "list-corporation-mining-extractions", "Moon mining extraction events", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMiningExtraction, error) {
			return deps.Store.ListMiningExtractionsByCorporation(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/mining/observers", "list-corporation-mining-observers", "Mining observers", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMiningObserver, error) {
			return deps.Store.ListMiningObserversByCorporation(ctx, id)
		}))
	get[SubIDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/mining/observers/{sub_id}", "list-corporation-mining-observer-records", "One observer's recorded mining", corpTag,
		subListHandler(func(ctx context.Context, id, subID int64) ([]gen.AppMiningObserverRecord, error) {
			return deps.Store.ListMiningObserverRecords(ctx, id, subID)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/medals", "list-corporation-medals", "Medal catalogue", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMedal, error) {
			return deps.Store.ListCorporationMedals(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/medals/issued", "list-corporation-medals-issued", "Medals issued", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMedalIssued, error) {
			return deps.Store.ListMedalsIssuedByCorporation(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/standings", "list-corporation-standings", "NPC/faction standings", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppStanding, error) {
			return deps.Store.ListStandings(ctx, string(domain.OwnerCorporation), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/shareholders", "list-corporation-shareholders", "Shareholders", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationShareholder, error) {
			return deps.Store.ListCorporationShareholders(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/contacts", "list-corporation-contacts", "Contacts", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContact, error) {
			return deps.Store.ListContacts(ctx, string(domain.OwnerCorporation), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/contacts/labels", "list-corporation-contact-labels", "Contact labels", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContactLabel, error) {
			return deps.Store.ListContactLabels(ctx, string(domain.OwnerCorporation), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/alliancehistory", "list-corporation-alliance-history", "Alliance membership history", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCorporationAllianceHistory, error) {
			return deps.Store.ListCorporationAllianceHistory(ctx, id)
		}))

	// PHASE 15.1: both ledgers are now derived for real (see
	// AggregateWalletJournalByRefType in db/queries/wallet.sql). Phase 15
	// described the derivation correctly and then 501'd instead of doing
	// it.
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/ledger/bounties", "list-corporation-ledger-bounties", "Bounty income by member", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AggregateWalletJournalByRefTypeRow, error) {
			return deps.Store.AggregateWalletJournalByRefType(ctx, string(domain.OwnerCorporation), id, bountyRefTypes)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/ledger/pi", "list-corporation-ledger-pi", "Planetary interaction tax income by member", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AggregateWalletJournalByRefTypeRow, error) {
			return deps.Store.AggregateWalletJournalByRefType(ctx, string(domain.OwnerCorporation), id, piRefTypes)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCorpView, "/api/v1/corporations/{id}/ledger/mining", "list-corporation-ledger-mining", "Mining ledger", corpTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMiningLedger, error) {
			return deps.Store.ListMiningLedgerByOwner(ctx, string(domain.OwnerCorporation), id)
		}))
}

// ---- shapes ----

type CorpWalletPageIn struct {
	ID       int64  `path:"id"`
	Division int16  `path:"division"`
	After    string `query:"after"`
	Before   string `query:"before"`
	Limit    int32  `query:"limit" default:"50"`
}

type CorpProjectIn struct {
	ID        int64  `path:"id"`
	ProjectID string `path:"project_id" format:"uuid"`
}

type ProjectContributionIn struct {
	ID          int64  `path:"id"`
	ProjectID   string `path:"project_id" format:"uuid"`
	CharacterID int64  `path:"character_id"`
}

// ---- wallet (corp, division in the path rather than a query param) ----

func corpWalletJournalHandler(deps api.Deps) func(context.Context, *CorpWalletPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *CorpWalletPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		before, beforeID, err := cursorTimeID(page, "date", "journal_id")
		if err != nil {
			return nil, api.PageError(err)
		}
		rows, err := deps.Store.ListWalletJournalPage(ctx, gen.ListWalletJournalPageParams{
			OwnerKind: string(domain.OwnerCorporation), OwnerID: in.ID, Division: in.Division,
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
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{}}}, nil
	}
}

func corpWalletTransactionsHandler(deps api.Deps) func(context.Context, *CorpWalletPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *CorpWalletPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		before, beforeID, err := cursorTimeID(page, "date", "transaction_id")
		if err != nil {
			return nil, api.PageError(err)
		}
		rows, err := deps.Store.ListWalletTransactionsPage(ctx, gen.ListWalletTransactionsPageParams{
			OwnerKind: string(domain.OwnerCorporation), OwnerID: in.ID, Division: in.Division,
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
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{}}}, nil
	}
}

// ---- projects ----

func projectDetailHandler(deps api.Deps) func(context.Context, *CorpProjectIn) (*ItemOut, error) {
	return func(ctx context.Context, in *CorpProjectIn) (*ItemOut, error) {
		id, err := parseUUID(in.ProjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed project id")
		}
		row, err := deps.Store.GetCorporationProject(ctx, id)
		if err != nil {
			return nil, api.NotFound("project")
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func projectContributorsHandler(deps api.Deps) func(context.Context, *CorpProjectIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *CorpProjectIn) (*CollectionOut, error) {
		id, err := parseUUID(in.ProjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed project id")
		}
		rows, err := deps.Store.ListCorporationProjectContributors(ctx, id)
		if err != nil {
			return nil, api.Internal("listing project contributors", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func projectContributionHandler(deps api.Deps) func(context.Context, *ProjectContributionIn) (*ItemOut, error) {
	return func(ctx context.Context, in *ProjectContributionIn) (*ItemOut, error) {
		id, err := parseUUID(in.ProjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed project id")
		}
		row, err := deps.Store.GetCorporationProjectContribution(ctx, id, in.CharacterID)
		if err != nil {
			return nil, api.NotFound("project contribution")
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// ---- blocked_by_pin-aware structures/starbases ----

// upstreamRouteBlocked reports whether any currently-blocked ESI route's
// upstream_path contains pathHint, and if so returns an
// administrator-facing explanation. Used by the handful of routes the
// roadmap's design notes call out by name (starbases, structures) as the
// blocked_by_pin/unavailable-vs-empty demonstration.
func upstreamRouteBlocked(ctx context.Context, deps api.Deps, pathHint string) (bool, string) {
	blocked, err := deps.Store.ListBlockedEsiRoutes(ctx)
	if err != nil {
		return false, ""
	}
	for _, r := range blocked {
		if strings.Contains(r.UpstreamPath, pathHint) {
			return true, "the ESI route backing this data (" + r.UpstreamPath + ") is currently blocked by the compatibility pin — an administrator must advance the pin (POST /api/v1/admin/esi/catalogue/pin) before this data can sync"
		}
	}
	return false, ""
}

func structuresHandler(deps api.Deps) func(context.Context, *IDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *IDIn) (*CollectionOut, error) {
		if blocked, reason := upstreamRouteBlocked(ctx, deps, "/corporations/{corporation_id}/structures"); blocked {
			c := api.UnavailableCollection[map[string]any](reason)
			return &CollectionOut{Body: c}, nil
		}
		rows, err := deps.Store.ListCorporationStructures(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing structures", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func starbasesHandler(deps api.Deps) func(context.Context, *IDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *IDIn) (*CollectionOut, error) {
		if blocked, reason := upstreamRouteBlocked(ctx, deps, "/corporations/{corporation_id}/starbases"); blocked {
			c := api.UnavailableCollection[map[string]any](reason)
			return &CollectionOut{Body: c}, nil
		}
		rows, err := deps.Store.ListCorporationStarbases(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing starbases", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func starbaseDetailHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		if blocked, reason := upstreamRouteBlocked(ctx, deps, "/corporations/{corporation_id}/starbases"); blocked {
			u := api.UnavailableItem[map[string]any](reason)
			return &ItemOut{Body: u}, nil
		}
		rows, err := deps.Store.ListCorporationStarbases(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing starbases", err)
		}
		for _, r := range rows {
			if r.StarbaseID == in.SubID {
				data := rowOf(r)
				return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
			}
		}
		return nil, api.NotFound("starbase")
	}
}

func skyhookDetailHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		rows, err := deps.Store.ListCorporationSkyhooks(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing skyhooks", err)
		}
		for _, r := range rows {
			if r.SkyhookID == in.SubID {
				data := rowOf(r)
				return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
			}
		}
		return nil, api.NotFound("skyhook")
	}
}

func sovHubDetailHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		rows, err := deps.Store.ListCorporationSovereigntyHubs(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing sovereignty hubs", err)
		}
		for _, r := range rows {
			if r.HubID == in.SubID {
				data := rowOf(r)
				return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
			}
		}
		return nil, api.NotFound("sovereignty hub")
	}
}
