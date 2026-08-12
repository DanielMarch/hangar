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
	// KNOWN GAP: this route is page-paginated the same way the corporation
	// journal is (X-Pages), but CharacterWorker's doSync (Phase 7) does not
	// page-walk — none of Phase 7's own routes needed it. Only the
	// single-page case (ESI's default page=1 response) is wired, so the
	// route has real, tested coverage rather than none at all. Flagged
	// rather than silently accepted as "good enough":
	// worker/corporation.go's fetchAllPages is the correct mechanism and
	// this route should eventually call it too.
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
	if route.UpstreamPath == mailBodyPath {
		// Fanout, not a simple body-in/rows-out handler — see doMailBodyFanout.
		rowsAffected, outcome, syncErr = w.doMailBodyFanout(ctx, s, sub, route, args.EntityID, tok.Value)
	} else {
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

	resp, doErr := w.Gateway.Do(ctx, esi.Request{
		Method: route.Method, UpstreamPath: route.UpstreamPath,
		PathParams:     map[string]string{"character_id": strconv.FormatInt(characterID, 10)},
		AccessToken:    accessToken,
		CacheMode:      derefStr(route.CacheMode),
		RateLimitGroup: derefStr(route.RateLimitGroup),
		// Phase 14: a background sync is exactly the caller the
		// char-notification reserve holds budget back from — see
		// reserve.go. Every other group is unaffected (the helper returns
		// the route's real ceiling).
		RateLimitMax:    BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
		RateLimitWindow: sync.IntervalToDuration(route.RateLimitWindow),
		UserKey:         fmt.Sprintf("hangar:%d", characterID),
		Validators:      validators,
	})
	if doErr != nil {
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

	default:
		return 0, outcome, fmt.Errorf("worker: unexpected status %d from %s", resp.StatusCode, route.UpstreamPath)
	}
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
			AccessToken:     accessToken,
			CacheMode:       derefStr(route.CacheMode),
			RateLimitGroup:  derefStr(route.RateLimitGroup),
			RateLimitMax:    BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
			RateLimitWindow: sync.IntervalToDuration(route.RateLimitWindow),
			UserKey:         fmt.Sprintf("hangar:%d", characterID),
		})
		if doErr != nil {
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
