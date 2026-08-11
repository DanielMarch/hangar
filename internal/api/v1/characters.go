// characters.go implements SRS §6.2 — every /api/v1/characters/{id}/...
// route. Every handler here is gated by the closed RBAC vocabulary's one
// matching permission, "characters.view" (internal/domain/vocabulary.go);
// there is no finer-grained per-sub-resource permission in that closed
// set, so every route in this file shares it rather than inventing an ad
// hoc permission name.
package v1

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const charTag = "characters"
const permCharView = "characters.view"

func registerCharacters(hapi huma.API, deps api.Deps) {
	get[IDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}", "get-character", "Character sheet", charTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCharacter, error) { return deps.Store.GetCharacter(ctx, id) }))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/skills", "list-character-skills", "Trained skills", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterSkill, error) {
			return deps.Store.ListCharacterSkills(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/skillqueue", "list-character-skillqueue", "Skill training queue", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterSkillqueue, error) {
			return deps.Store.ListCharacterSkillqueue(ctx, id)
		}))
	get[IDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/attributes", "get-character-attributes", "Attribute remap state", charTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCharacterAttribute, error) {
			return deps.Store.GetCharacterAttributes(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/clones", "list-character-clones", "Jump clones", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterClone, error) {
			return deps.Store.ListCharacterClones(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/implants", "list-character-implants", "Active clone implants", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterImplant, error) {
			return deps.Store.ListCharacterImplants(ctx, id)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/fittings", "list-character-fittings", "Saved fittings", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterFitting, error) {
			return deps.Store.ListCharacterFittings(ctx, id)
		}))
	get[SubIDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/fittings/{sub_id}", "get-character-fitting", "One saved fitting", charTag, fittingDetailHandler(deps))
	get[SubIDIn, EFTOut](hapi, deps, permCharView, "/api/v1/characters/{id}/fittings/{sub_id}/eft", "get-character-fitting-eft", "One saved fitting, EFT text format", charTag, fittingEFTHandler(deps))

	get[IDPageIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/assets", "list-character-assets", "Asset list (keyset-paginated)", charTag, assetsHandler(deps, domain.OwnerCharacter))
	get[AssetTreeIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/assets/tree/{location_id}", "get-character-asset-tree", "Recursive asset tree under one location", charTag, assetTreeHandler(deps, domain.OwnerCharacter))

	get[WalletPageIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/wallet/journal", "list-character-wallet-journal", "Wallet journal (keyset-paginated)", charTag, walletJournalHandler(deps, domain.OwnerCharacter))
	get[WalletPageIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/wallet/transactions", "list-character-wallet-transactions", "Wallet transactions (keyset-paginated)", charTag, walletTransactionsHandler(deps, domain.OwnerCharacter))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/wallet/summary", "get-character-wallet-summary", "Wallet balance by division", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppWalletBalance, error) {
			return deps.Store.ListWalletBalances(ctx, string(domain.OwnerCharacter), id)
		}))

	get[IDPageIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/contracts", "list-character-contracts", "Contracts (keyset-paginated)", charTag, contractsHandler(deps, domain.OwnerCharacter))
	get[SubIDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/contracts/{sub_id}/items", "list-character-contract-items", "Items on one contract", charTag, contractItemsHandler(deps, domain.OwnerCharacter))
	get[SubIDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/contracts/{sub_id}/bids", "list-character-contract-bids", "Bids on one auction contract", charTag, contractBidsHandler(deps, domain.OwnerCharacter))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/industry/jobs", "list-character-industry-jobs", "Industry jobs", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppIndustryJob, error) {
			return deps.Store.ListIndustryJobsByOwner(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/mining", "list-character-mining", "Personal mining ledger", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMiningLedger, error) {
			return deps.Store.ListMiningLedgerByOwner(ctx, string(domain.OwnerCharacter), id)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/planets", "list-character-planets", "Planetary interaction colonies", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppPlanetColony, error) {
			return deps.Store.ListPlanetColonies(ctx, id)
		}))
	get[SubIDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/planets/{sub_id}", "get-character-planet", "One PI colony", charTag, planetDetailHandler(deps))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/calendar", "list-character-calendar", "Calendar events", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCalendarEvent, error) {
			return deps.Store.ListCalendarEvents(ctx, id)
		}))
	get[SubIDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/calendar/{sub_id}", "get-character-calendar-event", "One calendar event", charTag, calendarEventDetailHandler(deps))
	get[SubIDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/calendar/{sub_id}/attendees", "list-character-calendar-attendees", "Attendees for one calendar event", charTag,
		subListHandler(func(ctx context.Context, id, subID int64) ([]gen.AppCalendarEventAttendee, error) {
			return deps.Store.ListCalendarEventAttendees(ctx, id, subID)
		}))

	get[IDPageIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/mail", "list-character-mail", "Mail headers (keyset-paginated)", charTag, mailHandler(deps))
	get[SubIDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/mail/{sub_id}", "get-character-mail-body", "One mail's body", charTag, mailBodyHandler(deps))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/mail/labels", "list-character-mail-labels", "Mail labels", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMailLabel, error) {
			return deps.Store.ListMailLabels(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/mail/lists", "list-character-mail-lists", "Mailing lists", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMailList, error) {
			return deps.Store.ListMailLists(ctx, id)
		}))

	get[NotificationsIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/notifications", "list-character-notifications", "EVE notifications (keyset-paginated)", charTag, notificationsHandler(deps))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/notifications/contacts", "list-character-notification-contacts", "Contact-change notification detail", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppNotificationContact, error) {
			return deps.Store.ListNotificationContacts(ctx, id)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/contacts", "list-character-contacts", "Contacts", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContact, error) {
			return deps.Store.ListContacts(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/contacts/labels", "list-character-contact-labels", "Contact labels", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppContactLabel, error) {
			return deps.Store.ListContactLabels(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/killmails", "list-character-killmails", "Killmails", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppKillmail, error) {
			return deps.Store.ListKillmailsByOwner(ctx, string(domain.OwnerCharacter), id, api.MaxLimit)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/blueprints", "list-character-blueprints", "Blueprints", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppBlueprint, error) {
			return deps.Store.ListBlueprintsByOwner(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/agents_research", "list-character-agents-research", "Agent research", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterAgentResearch, error) {
			return deps.Store.ListCharacterAgentResearch(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/loyalty/points", "list-character-loyalty-points", "Loyalty points by corporation", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterLoyaltyPoint, error) {
			return deps.Store.ListCharacterLoyaltyPoints(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/medals", "list-character-medals", "Medals issued to this character", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppMedalIssued, error) {
			return deps.Store.ListMedalsIssuedToCharacter(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/standings", "list-character-standings", "NPC/faction standings", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppStanding, error) {
			return deps.Store.ListStandings(ctx, string(domain.OwnerCharacter), id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/titles", "list-character-titles", "Corporation titles held", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterTitle, error) {
			return deps.Store.ListCharacterTitles(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/roles", "list-character-roles", "Corporation roles held", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterRole, error) {
			return deps.Store.ListCharacterRoles(ctx, id)
		}))
	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/corporationhistory", "list-character-corp-history", "Corporation employment history", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterCorporationHistory, error) {
			return deps.Store.ListCharacterCorporationHistory(ctx, id)
		}))
	get[IDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/fatigue", "get-character-fatigue", "Jump fatigue state", charTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCharacterJumpFatigue, error) {
			return deps.Store.GetCharacterJumpFatigue(ctx, id)
		}))
	get[IDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/location", "get-character-location", "Current solar system/station/structure", charTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCharacterLocation, error) {
			return deps.Store.GetCharacterLocation(ctx, id)
		}))
	get[IDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/online", "get-character-online", "Online status (same underlying row as /location)", charTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCharacterLocation, error) {
			return deps.Store.GetCharacterLocation(ctx, id)
		}))
	get[IDIn, ItemOut](hapi, deps, permCharView, "/api/v1/characters/{id}/ship", "get-character-ship", "Current ship (same underlying row as /location)", charTag,
		ownerDetailHandler(func(ctx context.Context, id int64) (gen.AppCharacterLocation, error) {
			return deps.Store.GetCharacterLocation(ctx, id)
		}))

	get[IDIn, CollectionOut](hapi, deps, permCharView, "/api/v1/characters/{id}/intel", "get-character-intel", "Interaction graph derived from mail, contacts, killmails and standings", charTag,
		ownerListHandler(func(ctx context.Context, id int64) ([]gen.AppCharacterIntelEdge, error) {
			return deps.Store.ListCharacterIntelEdges(ctx, id)
		}))
}

// ---- shapes shared with corporations.go for owner-polymorphic resources ----

type AssetTreeIn struct {
	ID         int64 `path:"id"`
	LocationID int64 `path:"location_id"`
}

type WalletPageIn struct {
	ID       int64  `path:"id"`
	Division int16  `query:"division" default:"1" doc:"Wallet division, 1-7. Defaults to 1 (a character has exactly one division)."`
	After    string `query:"after"`
	Before   string `query:"before"`
	Limit    int32  `query:"limit" default:"50"`
}

type NotificationsIn struct {
	ID     int64  `path:"id"`
	After  string `query:"after"`
	Before string `query:"before"`
	Limit  int32  `query:"limit" default:"50"`
}

type EFTOut struct {
	Body string `contentType:"text/plain"`
}

// subListHandler is ownerListHandler's counterpart for a nested
// sub-resource list keyed by (ownerID, subID) — e.g. one calendar event's
// attendees.
func subListHandler[Row any](fetch func(ctx context.Context, id, subID int64) ([]Row, error)) func(context.Context, *SubIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*CollectionOut, error) {
		rows, err := fetch(ctx, in.ID, in.SubID)
		if err != nil {
			return nil, api.Internal("listing resource", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func fittingDetailHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		fittings, err := deps.Store.ListCharacterFittings(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing fittings", err)
		}
		for _, f := range fittings {
			if f.FittingID == in.SubID {
				data := rowOf(f)
				return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
			}
		}
		return nil, api.NotFound("fitting")
	}
}

func fittingEFTHandler(deps api.Deps) func(context.Context, *SubIDIn) (*EFTOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*EFTOut, error) {
		fittings, err := deps.Store.ListCharacterFittings(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing fittings", err)
		}
		var fitting *gen.AppCharacterFitting
		for i := range fittings {
			if fittings[i].FittingID == in.SubID {
				fitting = &fittings[i]
				break
			}
		}
		if fitting == nil {
			return nil, api.NotFound("fitting")
		}
		items, err := deps.Store.ListCharacterFittingItems(ctx, in.ID, in.SubID)
		if err != nil {
			return nil, api.Internal("listing fitting items", err)
		}
		// PHASE 15.1: resolve type ids to real names via the SDE. Phase 15
		// rendered `[<type_id>]` placeholders because no name lookup query
		// existed — EFT is a text format players paste into a real fitting
		// tool, and a numeric id there is not a fitting.
		wanted := make([]int32, 0, len(items)+1)
		wanted = append(wanted, fitting.ShipTypeID)
		for _, it := range items {
			wanted = append(wanted, it.TypeID)
		}
		names := map[int32]string{}
		if rows, err := deps.Store.ListSdeTypeNames(ctx, wanted); err == nil {
			for _, r := range rows {
				names[r.TypeID] = r.Name
			}
		}
		// A lookup failure (no SDE imported yet) is not fatal: renderEFT
		// falls back to the id placeholder per missing line rather than
		// failing the whole export.
		return &EFTOut{Body: renderEFT(*fitting, items, names)}, nil
	}
}

// renderEFT builds an EFT-format export. `names` maps type ids to SDE
// names (Phase 15.1 — see ListSdeTypeNames); a type missing from it falls
// back to a `[<type_id>]` placeholder for that line only, which is what
// happens on an installation that has never run an SDE import. The header
// line is EFT's `[<ship>, <fitting name>]`.
func renderEFT(fitting gen.AppCharacterFitting, items []gen.AppCharacterFittingItem, names map[int32]string) string {
	out := "[" + typeLabel(fitting.ShipTypeID, names) + ", " + fitting.Name + "]\n"
	for _, it := range items {
		line := typeLabel(it.TypeID, names)
		if it.Quantity > 1 {
			line += " x" + itoa(it.Quantity)
		}
		out += line + "\n"
	}
	return out
}

func typeLabel(typeID int32, names map[int32]string) string {
	if n, ok := names[typeID]; ok && n != "" {
		return n
	}
	return "[" + itoa(int64(typeID)) + "]"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func planetDetailHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		colonies, err := deps.Store.ListPlanetColonies(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing planet colonies", err)
		}
		for _, c := range colonies {
			if c.PlanetID == in.SubID {
				data := rowOf(c)
				return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
			}
		}
		return nil, api.NotFound("planet colony")
	}
}

func calendarEventDetailHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		events, err := deps.Store.ListCalendarEvents(ctx, in.ID)
		if err != nil {
			return nil, api.Internal("listing calendar events", err)
		}
		for _, e := range events {
			if e.EventID == in.SubID {
				data := rowOf(e)
				return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
			}
		}
		return nil, api.NotFound("calendar event")
	}
}

func mailBodyHandler(deps api.Deps) func(context.Context, *SubIDIn) (*ItemOut, error) {
	return func(ctx context.Context, in *SubIDIn) (*ItemOut, error) {
		body, err := deps.Store.GetMailBody(ctx, in.ID, in.SubID)
		if err != nil {
			return nil, api.NotFound("mail")
		}
		data := rowOf(body)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func mailHandler(deps api.Deps) func(context.Context, *IDPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *IDPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		before := cursorTime(page, "sent_at")
		rows, err := deps.Store.ListMailHeadersPage(ctx, gen.ListMailHeadersPageParams{
			CharacterID: in.ID, BeforeSentAt: before, BeforeMailID: before, PageSize: page.Limit,
		})
		if err != nil {
			return nil, api.Internal("listing mail", err)
		}
		return mailCollection(rows, page.Limit), nil
	}
}

func notificationsHandler(deps api.Deps) func(context.Context, *NotificationsIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *NotificationsIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		before := cursorTime(page, "sent_at")
		rows, err := deps.Store.ListCharacterNotificationsPage(ctx, in.ID, before, page.Limit)
		if err != nil {
			return nil, api.Internal("listing notifications", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			next = api.EncodeCursor(api.Keyset{"before": rows[len(rows)-1]})
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
		}}, nil
	}
}
