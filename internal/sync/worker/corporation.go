package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
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

// corporationHandler is the corp-domain analogue of characterHandler
// (worker/character.go): raw response body in, rows-affected out. Every
// simple (single-call, no dynamic path/query params beyond
// {corporation_id}) Phase 8 route reduces to this shape.
type corporationHandler func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error)

func wrapCorp[T any](parse func([]byte) (T, error), syncFn func(context.Context, *store.Store, int64, T) (handlers.SyncResult, error)) corporationHandler {
	return func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := parse(body)
		if err != nil {
			return 0, err
		}
		res, err := syncFn(ctx, s, corporationID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	}
}

// corporationDispatch maps app.esi_route.upstream_path, verbatim, to the
// handler that syncs it — every "simple" route from Phase 8's scope
// (members, titles, roles, role history, divisions, shareholders,
// facilities, customs offices, container logs, structures, starbases,
// skyhook/sovereignty-hub LISTS, alliance history, medals, standings,
// contacts, industry jobs, blueprints, mining extractions/observers,
// market orders). Wallet division routes and the four DETAIL/per-item
// routes (starbase, skyhook, sovereignty-hub, mining-observer-record) each
// need a dynamic path/query parameter this map's signature can't carry —
// they're special-cased in Work's switch on route.UpstreamPath instead
// (doWalletDivisionSync / doStarbaseDetailFanout / doSkyhookDetailFanout /
// doSovereigntyHubDetailFanout / doMiningObserverRecordsFanout, all below).
//
// The mining routes are deliberately singular — "/corporation/..." not
// "/corporations/..." — copied verbatim from the live spec
// (01_ARCHITECTURE.md Principle 5 / roadmap edge case:
// TestSingularMiningPathsUsedVerbatim).
var corporationDispatch = map[string]corporationHandler{
	"/corporations/{corporation_id}":                 wrapCorp(handlers.ParseCorporationSheet, handlers.SyncCorporationSheet),
	"/corporations/{corporation_id}/members":         wrapCorp(handlers.ParseCorporationMembers, handlers.SyncCorporationMembers),
	"/corporations/{corporation_id}/membertracking":  wrapCorp(handlers.ParseCorporationMemberTracking, handlers.SyncCorporationMemberTracking),
	"/corporations/{corporation_id}/members/titles":  wrapCorp(handlers.ParseCorporationMemberTitles, handlers.SyncCorporationMemberTitles),
	"/corporations/{corporation_id}/members/limit":   wrapCorp(handlers.ParseCorporationMemberLimit, handlers.SyncCorporationMemberLimit),
	"/corporations/{corporation_id}/titles":          wrapCorp(handlers.ParseCorporationTitles, handlers.SyncCorporationTitles),
	"/corporations/{corporation_id}/roles":           wrapCorp(handlers.ParseCorporationRoles, handlers.SyncCorporationRoles),
	"/corporations/{corporation_id}/roles/history":   wrapCorp(handlers.ParseCorporationRoleHistory, handlers.SyncCorporationRoleHistory),
	"/corporations/{corporation_id}/divisions":       wrapCorp(handlers.ParseCorporationDivisions, handlers.SyncCorporationDivisions),
	"/corporations/{corporation_id}/shareholders":    wrapCorp(handlers.ParseCorporationShareholders, handlers.SyncCorporationShareholders),
	"/corporations/{corporation_id}/facilities":      wrapCorp(handlers.ParseCorporationFacilities, handlers.SyncCorporationFacilities),
	"/corporations/{corporation_id}/customs_offices": wrapCorp(handlers.ParseCorporationCustomsOffices, handlers.SyncCorporationCustomsOffices),
	"/corporations/{corporation_id}/containers/logs": wrapCorp(handlers.ParseCorporationContainerLog, handlers.SyncCorporationContainerLog),
	"/corporations/{corporation_id}/structures":      wrapCorp(handlers.ParseCorporationStructures, handlers.SyncCorporationStructures),
	"/corporations/{corporation_id}/starbases":       wrapCorp(handlers.ParseCorporationStarbases, handlers.SyncCorporationStarbases),
	"/corporations/{corporation_id}/structures/skyhooks": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseCorporationSkyhookList(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncCorporationSkyhooks(ctx, s, corporationID, dto.Skyhooks)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporations/{corporation_id}/structures/sovereignty-hubs": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseCorporationSovereigntyHubList(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncCorporationSovereigntyHubs(ctx, s, corporationID, dto.SovereigntyHubs)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporations/{corporation_id}/alliancehistory": wrapCorp(handlers.ParseCorporationAllianceHistory, handlers.SyncCorporationAllianceHistory),
	"/corporations/{corporation_id}/medals":          wrapCorp(handlers.ParseCorporationMedals, handlers.SyncCorporationMedals),
	"/corporations/{corporation_id}/medals/issued":   wrapCorp(handlers.ParseCorporationMedalsIssued, handlers.SyncCorporationMedalsIssued),
	"/corporations/{corporation_id}/standings":       wrapCorp(handlers.ParseCorporationStandings, handlers.SyncCorporationStandings),
	"/corporations/{corporation_id}/contacts":        wrapCorp(handlers.ParseCorporationContacts, handlers.SyncCorporationContacts),
	"/corporations/{corporation_id}/contacts/labels": wrapCorp(handlers.ParseCorporationContactLabels, handlers.SyncCorporationContactLabels),
	"/corporations/{corporation_id}/wallets": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseCorporationWalletBalances(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncWalletBalances(ctx, s, "corporation", corporationID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporations/{corporation_id}/industry/jobs": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseIndustryJobs(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncIndustryJobs(ctx, s, "corporation", corporationID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporations/{corporation_id}/blueprints": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseBlueprints(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncBlueprints(ctx, s, "corporation", corporationID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporation/{corporation_id}/mining/extractions": wrapCorp(handlers.ParseCorporationMiningExtractions, handlers.SyncCorporationMiningExtractions),
	"/corporation/{corporation_id}/mining/observers":   wrapCorp(handlers.ParseCorporationMiningObservers, handlers.SyncCorporationMiningObservers),
	"/corporations/{corporation_id}/orders": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseMarketOrders(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncMarketOrders(ctx, s, "corporation", corporationID, true, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporations/{corporation_id}/orders/history": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseMarketOrderHistory(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncMarketOrderHistory(ctx, s, "corporation", corporationID, true, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},

	// Phase 9 additions.
	"/corporations/{corporation_id}/contracts": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseContracts(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncContracts(ctx, s, "corporation", corporationID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/corporations/{corporation_id}/projects": func(ctx context.Context, s *store.Store, corporationID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseCorporationProjects(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncCorporationProjects(ctx, s, corporationID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
}

// pagePaginatedRoutes marks routes confirmed against the live embedded
// spec to declare an X-Pages response header (01_ARCHITECTURE.md §5.9's
// "page" mechanism) — walked in full by fetchAllPages before the handler
// ever runs, with the Last-Modified-must-match torn-set check the roadmap
// requires (a mismatch discards the whole assembled set and lets the
// subscription's normal retry cadence try again, never a partial commit).
//
// Every route in HANGAR's Phase 8 scope that accepts a `page` query
// parameter in the live spec ends up here except wallet transactions
// (cursor-paginated via `from_id`, no X-Pages — see wallet.go's doc
// comment) and endpoints this phase reads only once (member lists,
// facilities, divisions — no `page` parameter on the live spec at all).
var pagePaginatedRoutes = map[string]bool{
	"/corporations/{corporation_id}/roles/history":              true,
	"/corporations/{corporation_id}/shareholders":               true,
	"/corporations/{corporation_id}/customs_offices":            true,
	"/corporations/{corporation_id}/containers/logs":            true,
	"/corporations/{corporation_id}/structures":                 true,
	"/corporations/{corporation_id}/starbases":                  true,
	"/corporations/{corporation_id}/medals":                     true,
	"/corporations/{corporation_id}/medals/issued":              true,
	"/corporations/{corporation_id}/standings":                  true,
	"/corporations/{corporation_id}/contacts":                   true,
	"/corporations/{corporation_id}/industry/jobs":              true,
	"/corporations/{corporation_id}/blueprints":                 true,
	"/corporation/{corporation_id}/mining/extractions":          true,
	"/corporation/{corporation_id}/mining/observers":            true,
	"/corporations/{corporation_id}/orders":                     true,
	"/corporations/{corporation_id}/orders/history":             true,
	"/corporations/{corporation_id}/wallets/{division}/journal": true,
}

// CorporationWorker executes "sync_route" jobs for entity_kind =
// "corporation" subscriptions — the acting-character election
// (01_ARCHITECTURE.md §6.3) is what makes this worker different from
// CharacterWorker: every call is made with an ELECTED director's token,
// re-elected fresh on every attempt (never cached across attempts), so a
// 403 recorded on one attempt naturally steers the NEXT attempt to a
// different candidate without any explicit retry state machine — see
// doSync's election step and Elect's ordering (internal/sync/election.go).
type CorporationWorker struct {
	river.WorkerDefaults[planner.SyncJobArgs]

	Pool    store.Pool
	Gateway *esi.Client
	Tokens  *sso.Refresher
	Policy  sync.PolicyConfig
	Elector sync.ActingCharacterElector
}

// Work implements river.Worker[planner.SyncJobArgs].
func (w *CorporationWorker) Work(ctx context.Context, job *river.Job[planner.SyncJobArgs]) error {
	args := job.Args
	if args.EntityKind != sync.EntityCorporation {
		return fmt.Errorf("worker: corporation worker received non-corporation subscription %s (entity_kind=%q)", args.SubscriptionID, args.EntityKind)
	}

	s := store.New(w.Pool)
	sub, err := s.GetSyncSubscription(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: reading subscription %s: %w", args.SubscriptionID, err)
	}
	if !sub.Enabled {
		return nil
	}
	route, err := s.GetEsiRouteByID(ctx, sub.RouteID)
	if err != nil {
		return fmt.Errorf("worker: reading route %s: %w", sub.RouteID, err)
	}
	if route.BlockedByPin {
		return nil
	}

	// Elect fresh EVERY attempt (never trust sub.ActingCharacterID from a
	// prior attempt) — this is what turns a recorded 403 into a real
	// re-election on the next scheduled try, per §6.3 and the roadmap's
	// "never inline within the same job" edge case: this whole Work call
	// IS one attempt, and election happens once at its start, not as an
	// inline retry loop within it.
	characterID, err := w.Elector.Elect(ctx, args.EntityKind, args.EntityID, sub.RouteID)
	if err != nil {
		// No eligible director — not a hard failure (a corp with no
		// director token has no data, which must render as unavailable,
		// not as an empty list per the roadmap edge case).
		//
		// ── PHASE 20.2: BUT SAY SO ───────────────────────────────────────
		// This used to `return nil` and record NOTHING. The subscription
		// then sat at last_status = NULL forever with no sync_run row and
		// no explanation anywhere — the same class of silence as the
		// "/status is scheduled" lie 20.1.1 closed, and indistinguishable
		// on every admin surface from a route that has simply never come
		// due. It also re-ran on every planner tick, electing and failing,
		// for a condition that only clears when a human grants a
		// corporation role in-game.
		//
		// Now: one sync_run row saying exactly why, and a snooze so the
		// retry cadence matches how fast the cause can actually change.
		if errors.Is(err, sync.ErrNoEligibleActingCharacter) {
			return w.recordUnavailable(ctx, s, args.SubscriptionID, err)
		}
		return fmt.Errorf("worker: electing acting character for corporation %d route %s: %w", args.EntityID, sub.RouteID, err)
	}
	if sub.ActingCharacterID == nil || *sub.ActingCharacterID != characterID {
		if err := s.ElectActingCharacter(ctx, sub.SubscriptionID, &characterID); err != nil {
			return fmt.Errorf("worker: recording elected character %d for subscription %s: %w", characterID, sub.SubscriptionID, err)
		}
	}

	tok, err := w.Tokens.EnsureAccessToken(ctx, characterID)
	if err != nil {
		return fmt.Errorf("worker: obtaining access token for acting character %d: %w", characterID, err)
	}

	run, err := s.StartSyncRun(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: starting sync run for %s: %w", args.SubscriptionID, err)
	}

	var rowsAffected int32
	var outcome string
	var syncErr error
	switch route.UpstreamPath {
	case walletJournalPath, walletTransactionsPath:
		// Divisions 1-7 need a {division} path substitution the simple
		// corporationHandler shape (worker/character.go's wrap pattern)
		// doesn't carry — fanned out here rather than forcing every other
		// simple route through a signature only two routes need.
		rowsAffected, outcome, syncErr = w.doWalletDivisionSync(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case starbaseDetailPath:
		rowsAffected, outcome, syncErr = w.doStarbaseDetailFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case skyhookDetailPath:
		rowsAffected, outcome, syncErr = w.doSkyhookDetailFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case sovereigntyHubDetailPath:
		rowsAffected, outcome, syncErr = w.doSovereigntyHubDetailFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case miningObserverRecordsPath:
		rowsAffected, outcome, syncErr = w.doMiningObserverRecordsFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case contractItemsPath:
		rowsAffected, outcome, syncErr = w.doContractItemsFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case contractBidsPath:
		rowsAffected, outcome, syncErr = w.doContractBidsFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	case projectContributorsPath:
		rowsAffected, outcome, syncErr = w.doProjectContributorsFanout(ctx, s, sub, route, args.EntityID, characterID, tok.Value)
	default:
		handler, isSimple := corporationDispatch[route.UpstreamPath]
		if !isSimple {
			finishErr := s.FinishSyncRun(ctx, gen.FinishSyncRunParams{RunID: run.RunID, Status: nil, Outcome: nil, Error: strPtr("no handler registered"), RowsAffected: nil})
			if finishErr != nil {
				return finishErr
			}
			return fmt.Errorf("worker: no corporation handler registered for route %s (%s)", route.UpstreamPath, route.OperationID)
		}
		rowsAffected, outcome, syncErr = w.doSync(ctx, s, sub, route, args.EntityID, characterID, tok.Value, handler)
	}

	finishErr := s.FinishSyncRun(ctx, gen.FinishSyncRunParams{
		RunID: run.RunID, Status: statusOf(outcome), Outcome: &outcome,
		Error: errString(syncErr), RowsAffected: &rowsAffected,
	})
	if finishErr != nil {
		if syncErr != nil {
			return errors.Join(syncErr, finishErr)
		}
		return finishErr
	}
	return syncErr
}

// recordUnavailable writes one finished sync_run row explaining why this
// attempt produced nothing, and snoozes the subscription for as long as the
// cause plausibly persists.
//
// It deliberately does NOT touch last_status. That column holds an HTTP
// status, and no request was made — writing a synthetic 403 there would be
// a lie that an operator would reasonably act on by re-checking a token
// that is perfectly fine. app.sync_run.outcome is an open vocabulary and is
// the right place for a non-HTTP outcome.
func (w *CorporationWorker) recordUnavailable(ctx context.Context, s *store.Store, subscriptionID uuid.UUID, cause error) error {
	r, ok := classifyRefusal(cause, w.Policy.TTLFloor, time.Now())
	if !ok {
		return cause
	}
	run, err := s.StartSyncRun(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("worker: starting sync run for %s: %w", subscriptionID, err)
	}
	outcome := r.reason
	zero := int32(0)
	if err := s.FinishSyncRun(ctx, gen.FinishSyncRunParams{
		RunID: run.RunID, Status: nil, Outcome: &outcome,
		Error: errString(cause), RowsAffected: &zero,
	}); err != nil {
		return fmt.Errorf("worker: finishing sync run for %s: %w", subscriptionID, err)
	}
	return snoozeRefusal(ctx, s, subscriptionID, r)
}

const (
	walletJournalPath      = "/corporations/{corporation_id}/wallets/{division}/journal"
	walletTransactionsPath = "/corporations/{corporation_id}/wallets/{division}/transactions"

	// projectsPath is the one route in HANGAR's scope whose §5.9 pagination
	// mechanism is `cursor`, not `page` — see fetchAllCursorPages.
	// projectsItemsField is the array key inside its CorporationsProjectsListing
	// envelope, verbatim from the spec.
	projectsPath       = "/corporations/{corporation_id}/projects"
	projectsItemsField = "projects"

	// Phase 8.1: the detail routes Phase 8 left unwired (see this file's
	// original corporationDispatch comment, which described a
	// "fanoutDispatch" that never actually existed). Each needs a
	// dynamic per-item path/query param the simple corporationHandler
	// shape can't carry, so — like the wallet division routes above —
	// they're special-cased here rather than forced through that shape.
	starbaseDetailPath        = "/corporations/{corporation_id}/starbases/{starbase_id}"
	skyhookDetailPath         = "/corporations/{corporation_id}/structures/skyhooks/{skyhook_id}"
	sovereigntyHubDetailPath  = "/corporations/{corporation_id}/structures/sovereignty-hubs/{sovereignty_hub_id}"
	miningObserverRecordsPath = "/corporation/{corporation_id}/mining/observers/{observer_id}"

	// Phase 9 additions: contract items/bids and project contributions are
	// per-contract/per-project detail fanouts, same shape as the Phase 8.1
	// routes above.
	contractItemsPath = "/corporations/{corporation_id}/contracts/{contract_id}/items"
	contractBidsPath  = "/corporations/{corporation_id}/contracts/{contract_id}/bids"
	// DEFECT B38 (Phase 20.2): was ".../contributions". ESI's path is
	// ".../contributors" — see character.go's note on the same defect. The
	// CONSTANT was renamed in 20.5 too: it had kept the `Contributions`
	// spelling for three phases while naming the contributors path, which is
	// exactly how the wrong DTO stayed attached to it.
	projectContributorsPath = "/corporations/{corporation_id}/projects/{project_id}/contributors"
)

func strPtr(s string) *string { return &s }

// doWalletDivisionSync loops divisions 1-7 (every corporation has all
// seven; unused ones simply return empty/404 bodies), substituting
// {division} into the request path itself — app.esi_route.upstream_path is
// still used verbatim as the TEMPLATE (Principle 5); only the runtime
// substitution value varies. Journal calls go through fetchAllPages (the
// route's X-Pages page-pagination); transactions do not (from_id cursor,
// single page per this phase's scope — wallet.go's doc comment).
func (w *CorporationWorker) doWalletDivisionSync(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	var totalRows int32
	lastOutcome := "200"
	for division := int16(1); division <= 7; division++ {
		baseReq := esi.Request{
			Method: route.Method, UpstreamPath: route.UpstreamPath,
			PathParams: map[string]string{
				"corporation_id": strconv.FormatInt(corporationID, 10),
				"division":       strconv.FormatInt(int64(division), 10),
			},
			AccessToken:           accessToken,
			CacheMode:             derefStr(route.CacheMode),
			RateLimitGroup:        derefStr(route.RateLimitGroup),
			RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
			RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
			RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
			UserKey:               fmt.Sprintf("hangar:%d", characterID),
			EntityID:              corporationID,
		}

		var resp *esi.Response
		var doErr error
		if route.UpstreamPath == walletJournalPath {
			resp, doErr = fetchAllPages(ctx, w.Gateway, baseReq)
		} else {
			resp, doErr = w.Gateway.Do(ctx, baseReq)
		}
		if doErr != nil {
			if r, ok := classifyRefusal(doErr, w.Policy.TTLFloor, time.Now()); ok {
				return totalRows, r.reason, snoozeRefusal(ctx, s, sub.SubscriptionID, r)
			}
			return totalRows, normalize.Outcome(0, true), fmt.Errorf("worker: division %d of %s: %w", division, route.UpstreamPath, doErr)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var n int32
			var err error
			if route.UpstreamPath == walletJournalPath {
				entries, perr := handlers.ParseWalletJournalPage(resp.Body)
				if perr != nil {
					return totalRows, normalize.Outcome(resp.StatusCode, false), perr
				}
				res, serr := handlers.SyncWalletJournal(ctx, s, "corporation", corporationID, division, entries)
				n, err = res.RowsAffected, serr
			} else {
				entries, perr := handlers.ParseWalletTransactionsPage(resp.Body)
				if perr != nil {
					return totalRows, normalize.Outcome(resp.StatusCode, false), perr
				}
				res, serr := handlers.SyncWalletTransactions(ctx, s, "corporation", corporationID, division, entries)
				n, err = res.RowsAffected, serr
			}
			if err != nil {
				return totalRows, normalize.Outcome(resp.StatusCode, false), err
			}
			totalRows += n
			lastOutcome = normalize.Outcome(resp.StatusCode, false)
		case http.StatusForbidden:
			if err := s.RecordSync403(ctx, sub.SubscriptionID); err != nil {
				return totalRows, normalize.Outcome(resp.StatusCode, false), err
			}
			if err := s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
				EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: route.RouteID, CharacterID: characterID,
			}); err != nil {
				return totalRows, normalize.Outcome(resp.StatusCode, false), err
			}
			return totalRows, normalize.Outcome(resp.StatusCode, false), nil
		case http.StatusNotFound:
			// An unused division legitimately 404s — data, not a failure.
			continue
		case http.StatusTooManyRequests:
			return totalRows, normalize.Outcome(resp.StatusCode, false),
				snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)
		default:
			return totalRows, normalize.Outcome(resp.StatusCode, false), fmt.Errorf("worker: division %d of %s returned status %d", division, route.UpstreamPath, resp.StatusCode)
		}
	}

	next, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:  sync.RouteCacheConfig{CacheMode: derefStr(route.CacheMode), CacheAge: sync.IntervalToDuration(route.CacheAge), BlockedByPin: route.BlockedByPin},
		Policy: w.Policy, LastSuccess: time.Now(), Consecutive304: 0, OptInNoCache: sub.OptInNoCache, Now: time.Now(),
	})
	if err != nil {
		return totalRows, lastOutcome, err
	}
	if err := s.RecordSyncSuccess(ctx, gen.RecordSyncSuccessParams{
		SubscriptionID: sub.SubscriptionID, LastStatus: statusOf(lastOutcome), CursorAfter: sub.CursorAfter,
		NextDueAt: next, Consecutive304: 0,
	}); err != nil {
		return totalRows, lastOutcome, err
	}
	if err := s.ResetActingCharacter403(ctx, gen.ResetActingCharacter403Params{
		EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: route.RouteID, CharacterID: characterID,
	}); err != nil {
		return totalRows, lastOutcome, err
	}
	return totalRows, lastOutcome, nil
}

// doSync mirrors CharacterWorker.doSync's shape exactly (worker/character.go)
// with two differences: the acting character's id feeds both the
// UserKey/AccessToken AND the 403 bookkeeping (RecordActingCharacter403 /
// ResetActingCharacter403, keyed per (entity, route, character) —
// 00031_phase8_acting_character_history.sql), and page-paginated routes
// are walked in full via fetchAllPages before the handler ever sees a body.
func (w *CorporationWorker) doSync(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string, handler corporationHandler) (rowsAffected int32, outcome string, err error) {
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
		PathParams:            map[string]string{"corporation_id": strconv.FormatInt(corporationID, 10)},
		AccessToken:           accessToken,
		CacheMode:             derefStr(route.CacheMode),
		RateLimitGroup:        derefStr(route.RateLimitGroup),
		RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
		RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
		RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
		UserKey:               fmt.Sprintf("hangar:%d", characterID),
		// The 403 breaker is keyed on the CORPORATION, not on the acting
		// character (§5.8: "one director losing a corporation role must not
		// break the route for every other corporation"). Keying it on the
		// character would open a circuit that the very next attempt's
		// re-election makes irrelevant, and would never notice a
		// corporation whose whole director pool has lost the role.
		EntityID:   corporationID,
		Validators: validators,
	}

	var resp *esi.Response
	var doErr error
	switch {
	case route.UpstreamPath == projectsPath:
		resp, doErr = fetchAllCursorPages(ctx, w.Gateway, baseReq, projectsItemsField)
	case pagePaginatedRoutes[route.UpstreamPath]:
		resp, doErr = fetchAllPages(ctx, w.Gateway, baseReq)
	default:
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

	switch resp.StatusCode {
	case http.StatusNotModified:
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

	case http.StatusOK:
		n, syncErr := handler(ctx, s, corporationID, resp.Body)
		if syncErr != nil {
			return 0, outcome, syncErr
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
		if err := s.ResetActingCharacter403(ctx, gen.ResetActingCharacter403Params{
			EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: route.RouteID, CharacterID: characterID,
		}); err != nil {
			return n, outcome, err
		}
		return n, outcome, nil

	case http.StatusForbidden:
		// A 403 on THIS route for THIS character does not mean every corp
		// route is broken (roadmap edge case: election is per (subscription,
		// route), a structure/starbase route can 403 for a character who
		// passed election for other routes) — only this (entity, route,
		// character) triple's history is marked. The NEXT scheduled
		// attempt's Elect call (Work, above) reads that history fresh and
		// naturally steers away from this character if a better-scoring
		// candidate exists; nothing here retries inline.
		if err := s.RecordSync403(ctx, sub.SubscriptionID); err != nil {
			return 0, outcome, err
		}
		if err := s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
			EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: route.RouteID, CharacterID: characterID,
		}); err != nil {
			return 0, outcome, err
		}
		return 0, outcome, nil

	case http.StatusTooManyRequests:
		return 0, outcome, snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)

	default:
		return 0, outcome, fmt.Errorf("worker: unexpected status %d from %s", resp.StatusCode, route.UpstreamPath)
	}
}

// fanoutDetail is the corporation-side binding of worker/fanout.go's shared
// engine: for each already-known parent-list id, make one detail call and
// sync it. What it adds over the engine is §6.3's per-candidate 403 history
// — the corporation elector reads it, and the character worker has no
// elector at all, which is why that bookkeeping lives here as a callback
// rather than inside runDetailFanout.
func (w *CorporationWorker) fanoutDetail(
	ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute,
	corporationID, characterID int64, accessToken, idPathParamName string,
	items []fanoutDetailItem,
	syncOne func(ctx context.Context, s *store.Store, id int64, body []byte) (int32, error),
) (rowsAffected int32, outcome string, err error) {
	return runDetailFanout(ctx, w.Gateway, w.Policy, s, detailFanout{
		sub: sub, route: route,
		ownerParam: "corporation_id", ownerID: corporationID, idParam: idPathParamName,
		actingCharacterID: characterID, entityID: corporationID, accessToken: accessToken,
		items: items, syncOne: syncOne,
		on403: func(ctx context.Context, s *store.Store) error {
			return s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
				EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: route.RouteID, CharacterID: characterID,
			})
		},
		onSuccess: func(ctx context.Context, s *store.Store) error {
			return s.ResetActingCharacter403(ctx, gen.ResetActingCharacter403Params{
				EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: route.RouteID, CharacterID: characterID,
			})
		},
	})
}

// doStarbaseDetailFanout fans out over every starbase
// app.corporation_starbase's own list sync (an earlier subscription)
// already knows about -- Phase 14's fuel-low alert depends on the
// resulting app.starbase_detail.fuels (00010's header comment). system_id
// is a required QUERY parameter on this route the list rows already
// carry, never guessed.
func (w *CorporationWorker) doStarbaseDetailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	starbases, err := s.ListCorporationStarbases(ctx, corporationID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known starbases for corp %d: %w", corporationID, err)
	}
	items := make([]fanoutDetailItem, len(starbases))
	systemByID := make(map[int64]int32, len(starbases))
	for i, sb := range starbases {
		items[i] = fanoutDetailItem{id: sb.StarbaseID, extraQuery: map[string]string{"system_id": strconv.FormatInt(int64(sb.SystemID), 10)}}
		systemByID[sb.StarbaseID] = sb.SystemID
	}
	return w.fanoutDetail(ctx, s, sub, route, corporationID, characterID, accessToken, "starbase_id", items,
		func(ctx context.Context, s *store.Store, starbaseID int64, body []byte) (int32, error) {
			dto, err := handlers.ParseCorporationStarbaseDetail(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncCorporationStarbaseDetail(ctx, s, corporationID, starbaseID, systemByID[starbaseID], dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doSkyhookDetailFanout fans out over every skyhook this corporation's
// list sync already knows about, filling in state/reagents/is_active
// (Phase 8.1 -- see corporation_deployables.go's header comment on this
// domain for the fuel-to-reagent correction).
func (w *CorporationWorker) doSkyhookDetailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	skyhooks, err := s.ListCorporationSkyhooks(ctx, corporationID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known skyhooks for corp %d: %w", corporationID, err)
	}
	items := make([]fanoutDetailItem, len(skyhooks))
	for i, sh := range skyhooks {
		items[i] = fanoutDetailItem{id: sh.SkyhookID}
	}
	return w.fanoutDetail(ctx, s, sub, route, corporationID, characterID, accessToken, "skyhook_id", items,
		func(ctx context.Context, s *store.Store, skyhookID int64, body []byte) (int32, error) {
			dto, err := handlers.ParseCorporationSkyhookDetail(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncCorporationSkyhookDetail(ctx, s, corporationID, skyhookID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doSovereigntyHubDetailFanout mirrors doSkyhookDetailFanout for
// sovereignty hubs (Phase 8.1's same fuel-to-reagent correction).
func (w *CorporationWorker) doSovereigntyHubDetailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	hubs, err := s.ListCorporationSovereigntyHubs(ctx, corporationID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known sovereignty hubs for corp %d: %w", corporationID, err)
	}
	items := make([]fanoutDetailItem, len(hubs))
	for i, h := range hubs {
		items[i] = fanoutDetailItem{id: h.HubID}
	}
	return w.fanoutDetail(ctx, s, sub, route, corporationID, characterID, accessToken, "sovereignty_hub_id", items,
		func(ctx context.Context, s *store.Store, hubID int64, body []byte) (int32, error) {
			dto, err := handlers.ParseCorporationSovereigntyHubDetail(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncCorporationSovereigntyHubDetail(ctx, s, corporationID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doMiningObserverRecordsFanout fans out over every observer
// /corporation/{corporation_id}/mining/observers (already synced) knows
// about -- the singular upstream path is used verbatim, same as its
// parent list route (TestSingularMiningPathsUsedVerbatim).
func (w *CorporationWorker) doMiningObserverRecordsFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	observers, err := s.ListMiningObserversByCorporation(ctx, corporationID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known mining observers for corp %d: %w", corporationID, err)
	}
	items := make([]fanoutDetailItem, len(observers))
	for i, o := range observers {
		items[i] = fanoutDetailItem{id: o.ObserverID}
	}
	return w.fanoutDetail(ctx, s, sub, route, corporationID, characterID, accessToken, "observer_id", items,
		func(ctx context.Context, s *store.Store, observerID int64, body []byte) (int32, error) {
			dto, err := handlers.ParseCorporationMiningObserverRecords(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncCorporationMiningObserverRecords(ctx, s, corporationID, observerID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doContractItemsFanout fans out over every contract this corporation's
// list sync already knows about. A courier contract's item list comes
// back empty — the roadmap edge case: empty is a legitimate result, not a
// failure, and SyncContractItems' own doc comment covers it — so nothing
// here treats a 200 with zero items differently from any other 200.
func (w *CorporationWorker) doContractItemsFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	contracts, err := s.ListContractsPage(ctx, gen.ListContractsPageParams{
		OwnerKind: "corporation", OwnerID: corporationID, AfterContractID: 0, PageSize: 1_000_000,
	})
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known contracts for corp %d: %w", corporationID, err)
	}
	items := make([]fanoutDetailItem, len(contracts))
	for i, c := range contracts {
		items[i] = fanoutDetailItem{id: c.ContractID}
	}
	return w.fanoutDetail(ctx, s, sub, route, corporationID, characterID, accessToken, "contract_id", items,
		func(ctx context.Context, s *store.Store, contractID int64, body []byte) (int32, error) {
			dto, err := handlers.ParseContractItems(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncContractItems(ctx, s, "corporation", corporationID, contractID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doContractBidsFanout mirrors doContractItemsFanout for auction-contract
// bids (only meaningful on contracts of type 'auction'; ESI itself simply
// returns an empty/404 body for any other type, handled the same way
// fanoutDetail already treats a 404 on any other detail route: skip, not
// a failure).
func (w *CorporationWorker) doContractBidsFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	contracts, err := s.ListContractsPage(ctx, gen.ListContractsPageParams{
		OwnerKind: "corporation", OwnerID: corporationID, AfterContractID: 0, PageSize: 1_000_000,
	})
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known contracts for corp %d: %w", corporationID, err)
	}
	items := make([]fanoutDetailItem, len(contracts))
	for i, c := range contracts {
		items[i] = fanoutDetailItem{id: c.ContractID}
	}
	return w.fanoutDetail(ctx, s, sub, route, corporationID, characterID, accessToken, "contract_id", items,
		func(ctx context.Context, s *store.Store, contractID int64, body []byte) (int32, error) {
			dto, err := handlers.ParseContractBids(body)
			if err != nil {
				return 0, err
			}
			res, err := handlers.SyncContractBids(ctx, s, "corporation", corporationID, contractID, dto)
			if err != nil {
				return 0, err
			}
			return res.RowsAffected, nil
		})
}

// doProjectContributorsFanout fans out over every project this corporation's
// list sync already knows about. project_id is uuid FROM CCP (Principle 13's
// proof case) — runDetailFanout's item shape is int64-only (every other
// fanout substitutes a bigint id), so this is a small dedicated loop rather
// than forcing a uuid through that helper's int64 field; the uuid is
// formatted to its canonical string form for the path substitution and never
// touches a bigint or text column along the way (internal/store's uuid.UUID
// round trip is the only place it's typed).
//
// ── DEFECT B38's LEFTOVER, CLOSED IN PHASE 20.5 ──────────────────────────
// This route was parsed with the WRONG DTO. B38 renamed the path from
// `.../contributions` to `.../contributors` (ESI has no `contributions`
// collection route at all) and kept the `contributions` parser, which
// expects a bare array of {amount, character_id}. The live spec's
// CorporationsProjectsContributors is an OBJECT of {contributors, cursor}
// whose elements are {id, name, contributed}. It never failed because the
// route was not subscribable, so the parser had never been handed a body —
// the same shape of hiding as B49 one route over.
//
// It is also `x-pagination: cursor`, so it goes through fetchAllCursorPages
// (§5.9's cursor mechanism, implemented for /projects in 20.2) rather than a
// single request. A project with more than the default page of contributors
// would otherwise have synced only the first ten.
func (w *CorporationWorker) doProjectContributorsFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (rowsAffected int32, outcome string, err error) {
	projects, err := s.ListCorporationProjects(ctx, corporationID)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing known projects for corp %d: %w", corporationID, err)
	}

	outcome = "200"
	for _, p := range projects {
		resp, doErr := fetchAllCursorPages(ctx, w.Gateway, esi.Request{
			Method: route.Method, UpstreamPath: route.UpstreamPath,
			PathParams: map[string]string{
				"corporation_id": strconv.FormatInt(corporationID, 10),
				"project_id":     p.ProjectID.String(),
			},
			AccessToken:           accessToken,
			CacheMode:             derefStr(route.CacheMode),
			RateLimitGroup:        derefStr(route.RateLimitGroup),
			RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
			RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
			RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
			UserKey:               fmt.Sprintf("hangar:%d", characterID),
			EntityID:              corporationID,
		}, handlers.ProjectContributorsItemsField)
		if doErr != nil {
			if r, ok := classifyRefusal(doErr, w.Policy.TTLFloor, time.Now()); ok {
				return rowsAffected, r.reason, snoozeRefusal(ctx, s, sub.SubscriptionID, r)
			}
			return rowsAffected, normalize.Outcome(0, true), fmt.Errorf("worker: fetching contributors for project %s of corp %d: %w", p.ProjectID, corporationID, doErr)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			dto, perr := handlers.ParseCorporationProjectContributors(resp.Body)
			if perr != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), perr
			}
			res, serr := handlers.SyncCorporationProjectContributors(ctx, s, p.ProjectID, dto)
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
			return rowsAffected, normalize.Outcome(resp.StatusCode, false), fmt.Errorf("worker: contributors fetch for project %s of corp %d returned status %d", p.ProjectID, corporationID, resp.StatusCode)
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
