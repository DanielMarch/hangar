// Package worker implements the River Worker(s) that execute Phase 6's
// "sync_route" jobs (internal/sync/planner.KindSyncRoute). It is a
// separate package from internal/sync itself because
// internal/sync/planner already imports internal/sync (for EntityKind) —
// a Worker living in internal/sync and needing planner.SyncJobArgs would
// be an import cycle.
package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/riverqueue/river"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/internal/sync/normalize"
	"github.com/hangar-project/hangar/internal/sync/planner"
)

// mailBodyPath is the "detail" route the roadmap's mail-body edge case
// covers: one ESI request per mail, subscribed once per character the
// same way a corporation's starbase/skyhook/sovereignty-hub detail routes
// are (worker/corporation.go's fanoutDetail pattern) — a single
// subscription for the templated upstream_path, fanning out over every
// header this character already has that has no body row yet
// (mail.sql's ListMailHeadersWithoutBody). Routed through
// internal/esi.Client with UpstreamPath taken from the catalogue row,
// never a hand-built URL (roadmap: "Legacy invokes this inline, bypassing
// its own endpoint registry — HANGAR must route it through the route
// catalogue like every other call" — TestMailBodyRoutedThroughCatalogue).
const mailBodyPath = "/characters/{character_id}/mail/{mail_id}"

// PHASE 20.5 (B30): the three character-owned DETAIL routes whose handlers
// were written, tested and never dispatched. Each is the same shape as
// mailBodyPath — a second dynamic path parameter enumerated from the parent
// list this character has already synced — and each is now subscribable
// (worker/syncset.go's fanoutRoutes).
//
// The calendar pair is deliberately TWO subscriptions over the same event
// list rather than one that fetches both. They are two routes with two
// operation ids, two cache states and two etags; folding them together would
// make one 304 hide the other's 200, and would report one outcome for two
// upstream calls.
const (
	calendarEventDetailPath = "/characters/{character_id}/calendar/{event_id}"
	calendarAttendeesPath   = "/characters/{character_id}/calendar/{event_id}/attendees"
	planetColonyDetailPath  = "/characters/{character_id}/planets/{planet_id}"

	// characterAssetsPath is NOT a fan-out. It is here because its
	// after-sync enrichment (assets/names, see characterAfterOK) is keyed
	// on it.
	characterAssetsPath = "/characters/{character_id}/assets"

	// characterKillmailsPath is stage 1 of the killmail two-stage sync
	// (PHASE 20.7, B48). Its detail route is fetched inside this route's own
	// pass — killmail_fanout.go explains why it cannot have a subscription of
	// its own.
	characterKillmailsPath = "/characters/{character_id}/killmails/recent"

	// ── DEFECT B47 (PHASE 20.6) ──────────────────────────────────────────
	// The character half of the contract detail fan-outs. The CORPORATION
	// half has been wired since Phase 9 (contractItemsPath/contractBidsPath
	// in corporation.go) and handlers.SyncContractItems /
	// handlers.SyncContractBids have taken an ownerKind since the same
	// phase — so, exactly as with corporation assets, the only thing absent
	// was the map entry and the case arm.
	//
	// Appendix A capability band 1–14 lists Character "Contracts (headers,
	// items, bids)", so this is parity scope and not an extension. What
	// shipped was headers only: app.contract_item and app.contract_bid
	// could hold corporation rows and never a character's.
	characterContractItemsPath = "/characters/{character_id}/contracts/{contract_id}/items"
	characterContractBidsPath  = "/characters/{character_id}/contracts/{contract_id}/bids"

	// ── PHASE 20.8: CAPABILITY #41's STRUCTURE HALF ──────────────────────
	// This path contains NO character placeholder. It is a character-scoped
	// subscription anyway, and that is the design rather than an accident:
	// the route needs esi-universe.read_structures.v1 and DOCKING ACCESS,
	// and docking access is granted per character by the structure's own
	// ACL. Subscribing per character means the enumeration
	// (ListCharacterStructureIDs) asks each token only about the structures
	// that character's own rows already sit in — ids it has demonstrably
	// proven access to — instead of asking one token about every structure
	// the installation has ever seen and collecting a 403 per item.
	//
	// The station half is the opposite shape and is a GLOBAL subscription
	// (worker/global.go): it is unauthenticated, so there is no token whose
	// access could differ, and one pass for the installation is right.
	structureDetailPath = "/universe/structures/{structure_id}"

	// maxStructureResolutions bounds one character's structure pass. The set
	// is already bounded by that character's own rows, but "own rows" for a
	// character in a large alliance with corporation structures is not
	// self-evidently small, and this route carries no x-rate-limit group in
	// the catalogue — so Governor 1 does not throttle it and Governor 2's
	// installation-wide error budget is all that stands behind it. Same
	// reasoning, and the same shape, as maxMarketHistoryPairs.
	maxStructureResolutions = 500
)

// characterHandler is the shape every character-domain sync function in
// internal/sync/handlers reduces to once its Parse+Sync pair is combined:
// raw response body in, rows-affected count out.
type characterHandler func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error)

// wrap adapts a (Parse, Sync) pair — kept separate in internal/sync/handlers
// so the golden-file test can call Parse alone — into one characterHandler.
func wrap[T any](parse func([]byte) (T, error), syncFn func(context.Context, *store.Store, int64, T) (handlers.SyncResult, error)) characterHandler {
	return func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := parse(body)
		if err != nil {
			return 0, err
		}
		res, err := syncFn(ctx, s, characterID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	}
}

// characterDispatch maps app.esi_route.upstream_path, verbatim, to the
// handler that syncs it. Every entry here is one of Phase 7's nineteen
// character sub-resources (roadmap: "skills, skillqueue, attributes,
// clones, implants, contacts, contact labels, standings, titles, roles,
// medals, loyalty points, agent research, fatigue, corporation history,
// location/online/ship" plus the character sheet itself).
var characterDispatch = map[string]characterHandler{
	"/characters/{character_id}":                    wrap(handlers.ParseCharacterSheet, handlers.SyncCharacterSheet),
	"/characters/{character_id}/skills":             wrap(handlers.ParseCharacterSkills, handlers.SyncCharacterSkills),
	"/characters/{character_id}/skillqueue":         wrap(handlers.ParseCharacterSkillQueue, handlers.SyncCharacterSkillQueue),
	"/characters/{character_id}/attributes":         wrap(handlers.ParseCharacterAttributes, handlers.SyncCharacterAttributes),
	"/characters/{character_id}/clones":             wrap(handlers.ParseCharacterClones, handlers.SyncCharacterClones),
	"/characters/{character_id}/implants":           wrap(handlers.ParseCharacterImplants, handlers.SyncCharacterImplants),
	"/characters/{character_id}/contacts":           wrap(handlers.ParseCharacterContacts, handlers.SyncCharacterContacts),
	"/characters/{character_id}/contacts/labels":    wrap(handlers.ParseCharacterContactLabels, handlers.SyncCharacterContactLabels),
	"/characters/{character_id}/standings":          wrap(handlers.ParseCharacterStandings, handlers.SyncCharacterStandings),
	"/characters/{character_id}/titles":             wrap(handlers.ParseCharacterTitles, handlers.SyncCharacterTitles),
	"/characters/{character_id}/roles":              wrap(handlers.ParseCharacterRoles, handlers.SyncCharacterRoles),
	"/characters/{character_id}/medals":             wrap(handlers.ParseCharacterMedals, handlers.SyncCharacterMedals),
	"/characters/{character_id}/loyalty/points":     wrap(handlers.ParseCharacterLoyaltyPoints, handlers.SyncCharacterLoyaltyPoints),
	"/characters/{character_id}/agents_research":    wrap(handlers.ParseCharacterAgentResearch, handlers.SyncCharacterAgentResearch),
	"/characters/{character_id}/fatigue":            wrap(handlers.ParseCharacterFatigue, handlers.SyncCharacterFatigue),
	"/characters/{character_id}/corporationhistory": wrap(handlers.ParseCharacterCorporationHistory, handlers.SyncCharacterCorporationHistory),
	"/characters/{character_id}/location":           wrap(handlers.ParseCharacterLocation, handlers.SyncCharacterLocation),
	"/characters/{character_id}/online":             wrap(handlers.ParseCharacterOnline, handlers.SyncCharacterOnline),
	"/characters/{character_id}/ship":               wrap(handlers.ParseCharacterShip, handlers.SyncCharacterShip),

	// Phase 8 additions: wallet division 1 and the character's own mining
	// ledger are character-scoped concepts (02_DATABASE_SCHEMA.md §5.2's
	// "Wallets" group is wholly Phase 8's regardless of owner; mining_ledger
	// is a character-only table per 00016_domain_industry_mining.sql's
	// header) — added here rather than duplicating CharacterWorker's
	// dispatch/doSync machinery in a second file.
	"/characters/{character_id}/wallet": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseCharacterWalletBalance(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncWalletBalances(ctx, s, "character", characterID, []handlers.WalletBalanceDTO{dto})
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	// PHASE 20.2 (B31): the Phase 7 "KNOWN GAP" recorded here — this route
	// is page-paginated (X-Pages) and doSync did not walk it, so only page
	// 1 ever synced — is closed. characterPagePaginatedRoutes (below) now
	// routes it through the shared walker before the handler sees a body,
	// exactly as the corporation journal always has.
	"/characters/{character_id}/wallet/journal": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseWalletJournalPage(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncWalletJournal(ctx, s, "character", characterID, 1, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/characters/{character_id}/wallet/transactions": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseWalletTransactionsPage(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncWalletTransactions(ctx, s, "character", characterID, 1, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/characters/{character_id}/mining": wrap(handlers.ParseCharacterMiningLedger, handlers.SyncCharacterMiningLedger),
	"/characters/{character_id}/industry/jobs": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseIndustryJobs(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncIndustryJobs(ctx, s, "character", characterID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/characters/{character_id}/blueprints": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseBlueprints(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncBlueprints(ctx, s, "character", characterID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},

	// Phase 9 additions below. Market orders/history reuse market.go's
	// already owner-generic Sync functions (this file's header comment on
	// market.go explains why they didn't need a rewrite); assets,
	// contracts (list + bids), mail headers/labels/lists, PI colonies,
	// calendar events, and notifications are new domains this phase adds.
	"/characters/{character_id}/orders": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseMarketOrders(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncMarketOrders(ctx, s, "character", characterID, false, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/characters/{character_id}/orders/history": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseMarketOrderHistory(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncMarketOrderHistory(ctx, s, "character", characterID, false, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/characters/{character_id}/assets": func(ctx context.Context, s *store.Store, characterID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseAssets(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncAssets(ctx, s, "character", characterID, dto, nil)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/characters/{character_id}/contracts": wrap(handlers.ParseContracts, func(ctx context.Context, s *store.Store, characterID int64, dto []handlers.ContractDTO) (handlers.SyncResult, error) {
		return handlers.SyncContracts(ctx, s, "character", characterID, dto)
	}),
	"/characters/{character_id}/mail":        wrap(handlers.ParseMailHeaders, handlers.SyncMailHeaders),
	"/characters/{character_id}/mail/labels": wrap(handlers.ParseMailLabels, handlers.SyncMailLabels),
	"/characters/{character_id}/mail/lists":  wrap(handlers.ParseMailLists, handlers.SyncMailLists),
	"/characters/{character_id}/planets":     wrap(handlers.ParsePlanetColonies, handlers.SyncPlanetColonies),
	// DEFECT B38 (Phase 20.2). This read "/characters/{character_id}/calendar
	// /events" — a plural inferred from the resource name. ESI's path is
	// "/characters/{character_id}/calendar", and Principle 5 is explicit
	// that upstream_path is stored VERBATIM, never derived or pluralised.
	// The route could therefore never match a catalogue row, never be
	// scheduled, and never return data; it was invisible because the
	// handlers behind it are B30's and were never dispatched either.
	"/characters/{character_id}/calendar":      wrap(handlers.ParseCalendarEvents, handlers.SyncCalendarEvents),
	"/characters/{character_id}/notifications": wrap(handlers.ParseCharacterNotifications, handlers.SyncCharacterNotifications),

	// ── PHASE 20.7 (B48) ─────────────────────────────────────────────────
	// Two capabilities whose tables, store queries and /api/v1 endpoints all
	// shipped without a sync handler.
	//
	// Fittings backs THREE endpoints including the EFT export, which has
	// been rendering an empty document on every installation. Its scope,
	// esi-fittings.read_fittings.v1, was NOT in the 47-scope grant that
	// existed when this entry was written — adding the entry is what moves
	// the DERIVED scope set (tools/scopedump) and so what tells an operator
	// to enable it; see docs/SSO_APPLICATION.md.
	//
	// Contact notifications is the second half of capability #11, whose
	// first half (the character notifications feed) has been synced since
	// Phase 9. Its scope was already held.
	"/characters/{character_id}/fittings":               wrap(handlers.ParseFittings, handlers.SyncFittings),
	"/characters/{character_id}/notifications/contacts": wrap(handlers.ParseContactNotifications, handlers.SyncContactNotifications),
}

// dataLevel404Routes marks the routes where the roadmap's edge case
// applies literally: a 404 is DATA (e.g. no ship info while podded/some
// docked states), not a failure. internal/esi.Client.Do already never
// trips the RouteBreaker on any 4xx (see its Do doc comment) — this set
// controls a narrower thing: whether the WORKER treats a 404 body as
// "nothing to sync, reschedule normally" (routes in this set) or as a
// genuine failure worth retrying (every other route, where a 404 usually
// means a bad path/expired resource).
var dataLevel404Routes = map[string]bool{
	"/characters/{character_id}/ship": true,
}

// CharacterWorker executes "sync_route" jobs for entity_kind = "character"
// subscriptions. Corp/alliance dispatch is Phase 8/9's; Work returns a
// descriptive error for any other entity_kind rather than silently
// no-oping, so a corp-scoped subscription created before Phase 8 exists
// fails loudly instead of vanishing.
type CharacterWorker struct {
	river.WorkerDefaults[planner.SyncJobArgs]

	Pool    store.Pool
	Gateway *esi.Client
	Tokens  *sso.Refresher
	Policy  sync.PolicyConfig
}

// Work implements river.Worker[planner.SyncJobArgs].
func (w *CharacterWorker) Work(ctx context.Context, job *river.Job[planner.SyncJobArgs]) error {
	args := job.Args
	if args.EntityKind != sync.EntityCharacter {
		return fmt.Errorf("worker: character worker received non-character subscription %s (entity_kind=%q)", args.SubscriptionID, args.EntityKind)
	}

	s := store.New(w.Pool)
	sub, err := s.GetSyncSubscription(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: reading subscription %s: %w", args.SubscriptionID, err)
	}
	if !sub.Enabled {
		return nil // disabled between enqueue and work — not an error
	}
	route, err := s.GetEsiRouteByID(ctx, sub.RouteID)
	if err != nil {
		return fmt.Errorf("worker: reading route %s: %w", sub.RouteID, err)
	}
	if route.BlockedByPin {
		// The claim query already excludes blocked_by_pin routes, but a
		// pin could have advanced between claim and work — re-check
		// rather than trust the enqueue-time state.
		return nil
	}

	tok, err := w.Tokens.EnsureAccessToken(ctx, args.EntityID)
	if err != nil {
		return fmt.Errorf("worker: obtaining access token for character %d: %w", args.EntityID, err)
	}

	run, err := s.StartSyncRun(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: starting sync run for %s: %w", args.SubscriptionID, err)
	}

	var rowsAffected int32
	var outcome string
	var syncErr error
	switch route.UpstreamPath {
	case mailBodyPath:
		// Fanout, not a simple body-in/rows-out handler — see doMailBodyFanout.
		rowsAffected, outcome, syncErr = w.doMailBodyFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case calendarEventDetailPath:
		rowsAffected, outcome, syncErr = w.doCalendarEventDetailFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case calendarAttendeesPath:
		rowsAffected, outcome, syncErr = w.doCalendarAttendeesFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case planetColonyDetailPath:
		rowsAffected, outcome, syncErr = w.doPlanetColonyDetailFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case characterContractItemsPath:
		rowsAffected, outcome, syncErr = w.doContractItemsFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case characterContractBidsPath:
		rowsAffected, outcome, syncErr = w.doContractBidsFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case characterKillmailsPath:
		// PHASE 20.7 (B48). Two-stage: the recent list, then each unseen
		// killmail's detail. See killmail_fanout.go for why both stages live
		// in one subscription.
		rowsAffected, outcome, syncErr = w.doKillmailFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	case structureDetailPath:
		// PHASE 20.8 (capability #41).
		rowsAffected, outcome, syncErr = w.doStructureFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	default:
		handler, ok := characterDispatch[route.UpstreamPath]
		if !ok {
			finishErr := s.FinishSyncRun(ctx, gen.FinishSyncRunParams{RunID: run.RunID, Status: nil, Outcome: nil, Error: strPtr("no handler registered"), RowsAffected: nil})
			if finishErr != nil {
				return finishErr
			}
			return fmt.Errorf("worker: no character handler registered for route %s (%s)", route.UpstreamPath, route.OperationID)
		}
		rowsAffected, outcome, syncErr = w.doSync(ctx, s, sub, route, args.EntityID, tok.Value, handler)
	}

	finishErr := s.FinishSyncRun(ctx, gen.FinishSyncRunParams{
		RunID: run.RunID, Status: statusOf(outcome), Outcome: &outcome,
		Error: errString(syncErr), RowsAffected: &rowsAffected,
	})
	if finishErr != nil {
		// A run-bookkeeping failure must not mask the real sync outcome.
		if syncErr != nil {
			return errors.Join(syncErr, finishErr)
		}
		return finishErr
	}
	return syncErr
}

// doSync performs the actual gateway call, dispatch, and subscription
// bookkeeping. It returns rowsAffected/outcome for FinishSyncRun even on
// error, so a partial failure still leaves an informative sync_run row.
func (w *CharacterWorker) doSync(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string, handler characterHandler) (rowsAffected int32, outcome string, err error) {
	var validators *cache.Validators
	if sub.Etag != nil || sub.LastModified != nil {
		v := &cache.Validators{}
		if sub.Etag != nil {
			v.ETag = *sub.Etag
		}
		if sub.LastModified != nil {
			v.LastModified = *sub.LastModified
			v.HasLastModified = true
		}
		validators = v
	}

	baseReq := esi.Request{
		Method: route.Method, UpstreamPath: route.UpstreamPath,
		PathParams:     map[string]string{"character_id": strconv.FormatInt(characterID, 10)},
		AccessToken:    accessToken,
		CacheMode:      derefStr(route.CacheMode),
		RateLimitGroup: derefStr(route.RateLimitGroup),
		// The route's REAL ceiling — stored as the bucket's max_tokens and
		// measured against by both the B29 reconciler and
		// esi_ledger_divergence. Never a reduced value (reserve.go).
		RateLimitMax: RouteRateLimitMax(derefInt32(route.RateLimitMax)),
		// Phase 14: a background sync is exactly the caller the
		// char-notification reserve holds budget back from — see
		// reserve.go. Zero for every other group, meaning no reduction.
		RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
		RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
		UserKey:               fmt.Sprintf("hangar:%d", characterID),
		EntityID:              characterID,
		Validators:            validators,
	}

	var resp *esi.Response
	var doErr error
	if characterPagePaginatedRoutes[route.UpstreamPath] {
		resp, doErr = fetchAllPages(ctx, w.Gateway, baseReq)
	} else {
		resp, doErr = w.Gateway.Do(ctx, baseReq)
	}
	if doErr != nil {
		if r, ok := classifyRefusal(doErr, w.Policy.TTLFloor, time.Now()); ok {
			return 0, r.reason, snoozeRefusal(ctx, s, sub.SubscriptionID, r)
		}
		return 0, normalize.Outcome(0, true), doErr
	}

	outcome = normalize.Outcome(resp.StatusCode, false)
	routeCfg := sync.RouteCacheConfig{CacheMode: derefStr(route.CacheMode), CacheAge: sync.IntervalToDuration(route.CacheAge), BlockedByPin: route.BlockedByPin}

	switch {
	case resp.StatusCode == http.StatusNotModified:
		next, err := sync.PlanNextDueAt(sync.DueTimeInput{
			Route: routeCfg, Policy: w.Policy, LastSuccess: derefTime(sub.LastSuccessAt),
			Consecutive304: int(sub.Consecutive304) + 1, OptInNoCache: sub.OptInNoCache, Now: time.Now(),
		})
		if err != nil {
			return 0, outcome, err
		}
		if err := s.RecordSync304(ctx, sub.SubscriptionID, next); err != nil {
			return 0, outcome, err
		}
		return 0, outcome, nil

	case resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusNotFound && dataLevel404Routes[route.UpstreamPath]:
		n, syncErr := int32(0), error(nil)
		if resp.StatusCode == http.StatusOK {
			n, syncErr = handler(ctx, s, characterID, resp.Body)
			if syncErr != nil {
				return 0, outcome, syncErr
			}
			// PHASE 20.5 (B30): the assets/names enrichment. A second
			// upstream call whose input is the body just synced — see
			// worker/assetnames.go for why it belongs to this subscription
			// rather than one of its own.
			if route.UpstreamPath == characterAssetsPath {
				named, nerr := syncAssetNames(ctx, w.Gateway, s, characterAssetNamesPath,
					"character_id", "character", characterID, characterID, accessToken, resp.Body)
				if nerr != nil {
					return n, outcome, nerr
				}
				n += named
			}
		}
		next, err := sync.PlanNextDueAt(sync.DueTimeInput{
			Route: routeCfg, Policy: w.Policy, LastSuccess: time.Now(),
			Consecutive304: 0, OptInNoCache: sub.OptInNoCache, Now: time.Now(),
		})
		if err != nil {
			return n, outcome, err
		}
		if err := s.RecordSyncSuccess(ctx, gen.RecordSyncSuccessParams{
			SubscriptionID: sub.SubscriptionID, LastStatus: statusOf(outcome),
			Etag: nonEmpty(resp.ETag), LastModified: lastModPtr(resp), CursorAfter: sub.CursorAfter,
			NextDueAt: next, Consecutive304: 0,
		}); err != nil {
			return n, outcome, err
		}
		return n, outcome, nil

	case resp.StatusCode == http.StatusForbidden:
		if err := s.RecordSync403(ctx, sub.SubscriptionID); err != nil {
			return 0, outcome, err
		}
		return 0, outcome, nil

	case resp.StatusCode == http.StatusTooManyRequests:
		// §5.5: charge nothing (Governor 1's cost table already did),
		// snooze this subscription only, leave siblings alone. It is NOT a
		// job failure — returning an error here would have River retry on
		// its own schedule, which is the "do not spin" rule inverted.
		return 0, outcome, snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)

	default:
		return 0, outcome, fmt.Errorf("worker: unexpected status %d from %s", resp.StatusCode, route.UpstreamPath)
	}
}

// characterPagePaginatedRoutes are the character routes the live spec
// declares a `page` query parameter for, confirmed against the embedded
// snapshot.
//
// ── PHASE 20.2 (B31): THE KNOWN GAP THIS CLOSES ──────────────────────────
// Phase 7 recorded, in a comment on the wallet-journal entry of
// characterDispatch, that these routes are page-paginated and that
// CharacterWorker did not walk them — so only page 1 was ever synced and a
// character with more than ~2500 journal entries silently lost the rest.
// It was left flagged rather than fixed because worker/corporation.go's
// walker was the only one and it was not shared. B31 makes the walker a
// single implementation (internal/esi/pagination), so there is no longer a
// reason for one worker to have pagination and the other not to.
var characterPagePaginatedRoutes = map[string]bool{
	"/characters/{character_id}/assets":         true,
	"/characters/{character_id}/blueprints":     true,
	"/characters/{character_id}/contacts":       true,
	"/characters/{character_id}/contracts":      true,
	"/characters/{character_id}/mining":         true,
	"/characters/{character_id}/orders/history": true,
	"/characters/{character_id}/wallet/journal": true,
}

// doMailBodyFanout fetches the body of every mail header this character
// has that doesn't have one yet (mail.sql's ListMailHeadersWithoutBody),
// one ESI request per mail — the roadmap's own framing. Every call goes
// through w.Gateway.Do with UpstreamPath taken verbatim from the catalogue
// route row (never a hand-built URL string), mirroring
// worker/corporation.go's fanoutDetail for the same reason: Principle 5's
// "the catalogue's upstream_path is used verbatim, never derived" applies
// here exactly as it does to every other route. A 404 on one mail (it was
// deleted after the header sync ran) is data, not a failure, and is
// skipped; a 403 stops the whole fanout for this attempt, matching
// fanoutDetail's same reasoning (a role failure is homogeneous across
// every item of one character's token).
func (w *CharacterWorker) doMailBodyFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (rowsAffected int32, outcome string, err error) {
	headers, err := s.ListMailHeadersWithoutBody(ctx, characterID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing mail headers without a body for character %d: %w", characterID, err)
	}

	outcome = "200"
	for _, h := range headers {
		resp, doErr := w.Gateway.Do(ctx, esi.Request{
			Method: route.Method, UpstreamPath: route.UpstreamPath,
			PathParams: map[string]string{
				"character_id": strconv.FormatInt(characterID, 10),
				"mail_id":      strconv.FormatInt(h.MailID, 10),
			},
			AccessToken:           accessToken,
			CacheMode:             derefStr(route.CacheMode),
			RateLimitGroup:        derefStr(route.RateLimitGroup),
			RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
			RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
			RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
			UserKey:               fmt.Sprintf("hangar:%d", characterID),
			EntityID:              characterID,
		})
		if doErr != nil {
			// A refusal part-way through the fanout keeps whatever bodies
			// already committed (each is its own upsert) and snoozes the
			// rest for the next attempt, which re-reads
			// ListMailHeadersWithoutBody and picks up exactly where this
			// one stopped. There is no partial-set hazard here the way
			// there is for a paged collection: mail bodies are independent
			// rows, not one dataset read in slices.
			if r, ok := classifyRefusal(doErr, w.Policy.TTLFloor, time.Now()); ok {
				return rowsAffected, r.reason, snoozeRefusal(ctx, s, sub.SubscriptionID, r)
			}
			return rowsAffected, normalize.Outcome(0, true), fmt.Errorf("worker: fetching body for mail %d of character %d: %w", h.MailID, characterID, doErr)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			dto, perr := handlers.ParseMailBody(resp.Body)
			if perr != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), perr
			}
			res, serr := handlers.SyncMailBody(ctx, s, characterID, h.MailID, dto)
			if serr != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), serr
			}
			rowsAffected += res.RowsAffected
			outcome = normalize.Outcome(resp.StatusCode, false)
		case http.StatusNotFound:
			continue
		case http.StatusForbidden:
			if err := s.RecordSync403(ctx, sub.SubscriptionID); err != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), err
			}
			return rowsAffected, normalize.Outcome(resp.StatusCode, false), nil
		case http.StatusTooManyRequests:
			return rowsAffected, normalize.Outcome(resp.StatusCode, false),
				snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)
		default:
			return rowsAffected, normalize.Outcome(resp.StatusCode, false), fmt.Errorf("worker: body fetch for mail %d of character %d returned status %d", h.MailID, characterID, resp.StatusCode)
		}
	}

	next, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:  sync.RouteCacheConfig{CacheMode: derefStr(route.CacheMode), CacheAge: sync.IntervalToDuration(route.CacheAge), BlockedByPin: route.BlockedByPin},
		Policy: w.Policy, LastSuccess: time.Now(), Consecutive304: 0, OptInNoCache: sub.OptInNoCache, Now: time.Now(),
	})
	if err != nil {
		return rowsAffected, outcome, err
	}
	if err := s.RecordSyncSuccess(ctx, gen.RecordSyncSuccessParams{
		SubscriptionID: sub.SubscriptionID, LastStatus: statusOf(outcome), CursorAfter: sub.CursorAfter,
		NextDueAt: next, Consecutive304: 0,
	}); err != nil {
		return rowsAffected, outcome, err
	}
	return rowsAffected, outcome, nil
}

// fanoutDetail is the character-side binding of worker/fanout.go's shared
// engine. Unlike the corporation form it records no acting-character 403
// history: a character acts for itself, there is no election to inform, and
// writing a candidate row for a character that is not a candidate for
// anything would put rows in app.sync_acting_character_history that §6.3's
// elector would then have to learn to ignore.
func (w *CharacterWorker) fanoutDetail(
	ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute,
	characterID int64, accessToken, idPathParamName string,
	items []fanoutDetailItem,
	syncOne func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error),
) (int32, string, error) {
	return runDetailFanout(ctx, w.Gateway, w.Policy, s, detailFanout{
		sub: sub, route: route,
		ownerParam: "character_id", ownerID: characterID, idParam: idPathParamName,
		actingCharacterID: characterID, entityID: characterID, accessToken: accessToken,
		items: items, syncOne: syncOne,
	})
}

// doCalendarEventDetailFanout fetches the body text, owner and duration of
// every calendar event this character's list sync already knows about.
//
// Every event is re-fetched every pass rather than only the ones with no
// detail row: an event's text and duration are mutable — a fleet op moves,
// an alliance edits the briefing — and UpsertCalendarEventDetail's own
// IS DISTINCT FROM guard already makes an unchanged detail a zero-row write.
// That is the opposite of the mail-body fan-out next door, which fetches
// only headers WITHOUT a body, because a sent mail's body never changes.
func (w *CharacterWorker) doCalendarEventDetailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	events, err := s.ListCalendarEvents(ctx, characterID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known calendar events for character %d: %w", characterID, err)
	}
	items := make([]fanoutDetailItem, len(events))
	for i, e := range events {
		items[i] = fanoutDetailItem{id: e.EventID}
	}
	return w.fanoutDetail(ctx, s, sub, route, characterID, accessToken, "event_id", items,
		func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			eventID := item.id
			dto, err := handlers.ParseCalendarEventDetail(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncCalendarEventDetail(ctx, s, characterID, eventID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doCalendarAttendeesFanout mirrors the detail fan-out for the attendee
// roster of each known event.
func (w *CharacterWorker) doCalendarAttendeesFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	events, err := s.ListCalendarEvents(ctx, characterID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known calendar events for character %d: %w", characterID, err)
	}
	items := make([]fanoutDetailItem, len(events))
	for i, e := range events {
		items[i] = fanoutDetailItem{id: e.EventID}
	}
	return w.fanoutDetail(ctx, s, sub, route, characterID, accessToken, "event_id", items,
		func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			eventID := item.id
			dto, err := handlers.ParseCalendarAttendees(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncCalendarAttendees(ctx, s, characterID, eventID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doPlanetColonyDetailFanout fetches the pins/links/routes of every planetary
// colony this character's list sync already knows about — the whole PI
// layout, which the colony LIST route reports only as a `num_pins` count.
func (w *CharacterWorker) doPlanetColonyDetailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	colonies, err := s.ListPlanetColonies(ctx, characterID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known planet colonies for character %d: %w", characterID, err)
	}
	items := make([]fanoutDetailItem, len(colonies))
	for i, c := range colonies {
		items[i] = fanoutDetailItem{id: int64(c.PlanetID)}
	}
	return w.fanoutDetail(ctx, s, sub, route, characterID, accessToken, "planet_id", items,
		func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			planetID := item.id
			dto, err := handlers.ParsePlanetColonyDetail(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncPlanetColonyDetail(ctx, s, characterID, planetID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doContractItemsFanout fans out over every contract this character's list
// sync already knows about (defect B47 — see characterContractItemsPath).
//
// Deliberately the same shape as CorporationWorker.doContractItemsFanout,
// down to reading the contract set through ListContractsPage with the owner
// kind switched. The two could be folded into one owner-generic function,
// and are not, because the two workers keep separate fanoutDetail wrappers
// carrying different election and 403 semantics (a corporation route picks
// an acting character and records a per-(entity, route, character) breaker;
// a character route has exactly one token and no election). Sharing the body
// while those differ would mean a parameter that means "which worker am I",
// which is the thing the two-worker split exists to avoid.
//
// A courier contract's item list comes back empty; empty is a legitimate
// result, not a failure, exactly as on the corporation side.
func (w *CharacterWorker) doContractItemsFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	items, err := w.knownContractItems(ctx, s, characterID)
	if err != nil {
		return 0, "", err
	}
	return w.fanoutDetail(ctx, s, sub, route, characterID, accessToken, "contract_id", items,
		func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			contractID := item.id
			dto, err := handlers.ParseContractItems(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncContractItems(ctx, s, "character", characterID, contractID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doContractBidsFanout mirrors doContractItemsFanout for auction-contract
// bids. ESI returns an empty or 404 body for a non-auction contract, which
// fanoutDetail already treats as a skip rather than a failure.
func (w *CharacterWorker) doContractBidsFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	items, err := w.knownContractItems(ctx, s, characterID)
	if err != nil {
		return 0, "", err
	}
	return w.fanoutDetail(ctx, s, sub, route, characterID, accessToken, "contract_id", items,
		func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			contractID := item.id
			dto, err := handlers.ParseContractBids(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncContractBids(ctx, s, "character", characterID, contractID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doStructureFanout resolves every Upwell structure this character's own
// rows reference into app.location — Appendix A capability #41's structure
// half, and the reason UpsertLocation had no production caller until this
// phase.
//
// It calls runDetailFanout directly rather than through w.fanoutDetail
// because it is the one fan-out in HANGAR whose 403 is DATA, and
// forbiddenIsData is not something a shared wrapper should be able to set by
// accident — see the field's comment in worker/fanout.go.
//
// AN EMPTY ITEM SET IS A COMPLETE PASS. On an installation whose characters
// keep nothing in a structure, this resolves nothing and records a success,
// which is correct and is NOT the same thing as the capability being
// unreachable. The difference is visible: a subscription exists, it runs, and
// its sync_run rows say 0 — as opposed to no subscription at all, which is
// what every installation had before this phase.
func (w *CharacterWorker) doStructureFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	ids, err := s.ListCharacterStructureIDs(ctx, characterID, maxStructureResolutions)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing structure ids seen by character %d: %w", characterID, err)
	}
	items := make([]fanoutDetailItem, len(ids))
	for i, id := range ids {
		items[i] = fanoutDetailItem{id: id}
	}

	return runDetailFanout(ctx, w.Gateway, w.Policy, s, detailFanout{
		sub: sub, route: route,
		// ownerParam names a placeholder this path does not contain;
		// esi.Client.buildURL ignores path params with no matching
		// placeholder, which is what lets one engine serve owned and unowned
		// detail routes alike (the killmail detail route relies on the same).
		ownerParam: "character_id", ownerID: characterID, idParam: "structure_id",
		actingCharacterID: characterID, entityID: characterID, accessToken: accessToken,
		items:           items,
		forbiddenIsData: true,
		syncOne: func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			dto, perr := handlers.ParseStructure(body)
			if perr != nil {
				return 0, perr
			}
			res, serr := handlers.SyncStructure(ctx, s, item.id, dto)
			if serr != nil {
				return 0, serr
			}
			return res.RowsAffected, nil
		},
	})
}

// knownContractItems is the contract set both character contract fan-outs
// enumerate: every contract the list sync has already landed for this
// character. PageSize is the same "everything" value the corporation side
// uses — this is an internal enumeration of rows already held locally, not a
// paginated API response.
func (w *CharacterWorker) knownContractItems(ctx context.Context, s *store.Store, characterID int64) ([]fanoutDetailItem, error) {
	contracts, err := s.ListContractsPage(ctx, gen.ListContractsPageParams{
		OwnerKind: "character", OwnerID: characterID, AfterContractID: 0, PageSize: 1_000_000,
	})
	if err != nil {
		return nil, fmt.Errorf("worker: listing known contracts for character %d: %w", characterID, err)
	}
	items := make([]fanoutDetailItem, len(contracts))
	for i, c := range contracts {
		items[i] = fanoutDetailItem{id: c.ContractID}
	}
	return items, nil
}

func statusOf(outcome string) *int16 {
	n, err := strconv.ParseInt(outcome, 10, 16)
	if err != nil {
		return nil
	}
	v := int16(n)
	return &v
}

func errString(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	return &s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func lastModPtr(resp *esi.Response) *time.Time {
	if resp == nil || !resp.HasLastModified {
		return nil
	}
	return &resp.LastModified
}
