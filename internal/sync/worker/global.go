package worker

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/riverqueue/river"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/internal/sync/normalize"
	"github.com/hangar-project/hangar/internal/sync/planner"
)

// globalHandler mirrors characterHandler/corporationHandler but takes no
// owner id at all — a global route's subscription row uses entity_id = 0
// (internal/sync.EntityGlobal's doc comment).
type globalHandler func(ctx context.Context, s *store.Store, body []byte) (int32, error)

var globalDispatch = map[string]globalHandler{
	// PHASE 15.1 — Tranquility status, backing /api/v1/meta/server-status.
	"/status": func(ctx context.Context, s *store.Store, body []byte) (int32, error) {
		dto, err := handlers.ParseServerStatus(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncServerStatus(ctx, s, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/sovereignty/campaigns": func(ctx context.Context, s *store.Store, body []byte) (int32, error) {
		dto, err := handlers.ParseSovereigntyCampaigns(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncSovereigntyCampaigns(ctx, s, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	"/sovereignty/systems": func(ctx context.Context, s *store.Store, body []byte) (int32, error) {
		dto, err := handlers.ParseSovereigntySystems(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncSovereigntySystems(ctx, s, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	// PHASE 20.5 (B30). Global adjusted/average prices per type. The
	// simplest of the thirteen to place and the one that had been
	// unreachable the longest: the route takes no parameters, needs no
	// scope, needs no token, and GET /api/v1/markets/prices has been
	// serving an empty collection from app.market_price since Phase 15
	// because nothing ever wrote to it.
	marketPricesPath: func(ctx context.Context, s *store.Store, body []byte) (int32, error) {
		dto, err := handlers.ParseMarketPrices(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncMarketPrices(ctx, s, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},

	// ── PHASE 20.7 (B48) ─────────────────────────────────────────────────
	// Two more parameterless, unauthenticated routes whose tables and
	// endpoints shipped without a writer. Both belong here for exactly the
	// reason /markets/prices does: no path parameter, no scope, no token, no
	// owner to elect.
	insurancePricesPath: func(ctx context.Context, s *store.Store, body []byte) (int32, error) {
		dto, err := handlers.ParseInsurancePrices(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncInsurancePrices(ctx, s, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	metaStatusPath: func(ctx context.Context, s *store.Store, body []byte) (int32, error) {
		dto, err := handlers.ParseEsiStatus(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncEsiStatus(ctx, s, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
}

const (
	marketPricesPath  = "/markets/prices"
	marketHistoryPath = "/markets/{region_id}/history"

	// PHASE 20.7 (B48). Capability #42's and #45's upstream routes.
	insurancePricesPath = "/insurance/prices"
	metaStatusPath      = "/meta/status"

	// maxMarketHistoryPairs bounds one pass of the market-history fan-out.
	//
	// The pair set comes from app.market_order (ListMarketHistoryPairs), so
	// it is bounded by what this installation actually trades rather than by
	// EVE — but "actually trades" for a large trading corporation is still
	// thousands of (region, type) pairs, and this route carries NO
	// x-rate-limit group in the catalogue, so Governor 1 does not throttle
	// it and Governor 2's installation-wide error budget is the only thing
	// standing behind it. A cap is therefore load-bearing, not defensive.
	//
	// 500 at the route's own cadence is the largest number that cannot,
	// by itself, out-pace the error budget's 100/minute ceiling on a bad
	// day. A pass that hits the cap syncs the first 500 pairs in
	// (region_id, type_id) order and says so in its sync_run row; it does
	// not silently truncate.
	maxMarketHistoryPairs = 500
)

// GlobalWorker executes "sync_route" jobs for entity_kind = "global"
// subscriptions — Phase 8's only global-scoped domain is sovereignty
// (campaigns, systems), unauthenticated and requiring no acting-character
// election at all (there is no owner to elect a director for).
type GlobalWorker struct {
	river.WorkerDefaults[planner.SyncJobArgs]

	Pool    store.Pool
	Gateway *esi.Client
	Policy  sync.PolicyConfig
}

func (w *GlobalWorker) Work(ctx context.Context, job *river.Job[planner.SyncJobArgs]) error {
	args := job.Args
	if args.EntityKind != sync.EntityGlobal {
		return fmt.Errorf("worker: global worker received non-global subscription %s (entity_kind=%q)", args.SubscriptionID, args.EntityKind)
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

	var handler globalHandler
	if route.UpstreamPath != marketHistoryPath {
		h, ok := globalDispatch[route.UpstreamPath]
		if !ok {
			return fmt.Errorf("worker: no global handler registered for route %s (%s)", route.UpstreamPath, route.OperationID)
		}
		handler = h
	}

	run, err := s.StartSyncRun(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: starting sync run for %s: %w", args.SubscriptionID, err)
	}

	var rowsAffected int32
	var outcome string
	var syncErr error
	if route.UpstreamPath == marketHistoryPath {
		rowsAffected, outcome, syncErr = w.doMarketHistoryFanout(ctx, s, sub, route)
	} else {
		rowsAffected, outcome, syncErr = w.doSync(ctx, s, sub, route, handler)
	}

	finishErr := s.FinishSyncRun(ctx, gen.FinishSyncRunParams{
		RunID: run.RunID, Status: statusOf(outcome), Outcome: &outcome,
		Error: errString(syncErr), RowsAffected: &rowsAffected,
	})
	if finishErr != nil {
		if syncErr != nil {
			return fmt.Errorf("%w (and finishing sync run: %v)", syncErr, finishErr)
		}
		return finishErr
	}
	return syncErr
}

func (w *GlobalWorker) doSync(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, handler globalHandler) (rowsAffected int32, outcome string, err error) {
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
		CacheMode:             derefStr(route.CacheMode),
		RateLimitGroup:        derefStr(route.RateLimitGroup),
		RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
		RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
		RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
		UserKey:               "hangar:global",
		// EntityID stays 0: a global route has no owner, so §5.8's
		// entity-scoped 403 breaker has nothing to key on. These routes are
		// unauthenticated and cannot 403 for an authorisation reason.
		Validators: validators,
	})
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
		n, syncErr := handler(ctx, s, resp.Body)
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
		return n, outcome, nil

	case http.StatusTooManyRequests:
		return 0, outcome, snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)

	default:
		return 0, outcome, fmt.Errorf("worker: unexpected status %d from %s", resp.StatusCode, route.UpstreamPath)
	}
}

// doMarketHistoryFanout walks every (region_id, type_id) pair this
// installation's own market-order sync has landed, fetching each pair's
// daily price history.
//
// ── WHY THIS IS A FAN-OUT AND NOT A DISPATCH ENTRY (PHASE 20.5, B30) ─────
// GET /markets/{region_id}/history needs a region in the PATH and a REQUIRED
// type_id in the QUERY. A subscription row has one entity_id and no second
// identifier column, so — exactly like a starbase's detail route or a mail
// body — the parameters are enumerated at work time from rows an earlier
// sync already committed. What is unusual here is that BOTH parameters are
// enumerated, which is why it does not use runDetailFanout's single-id item
// shape.
//
// It is a GLOBAL subscription rather than a per-region one because the
// route is unauthenticated reference data with no owner at all: there is no
// entity whose token or roles govern it, and inventing a region-as-entity
// would put region ids in a column every other row uses for a character or
// corporation id.
//
// ── ONE PAIR'S FAILURE IS NOT THE PASS'S FAILURE ─────────────────────────
// A 404 on a pair (the type has never traded in that region) is data, and
// skips. A refusal from either governor snoozes the whole subscription, as
// everywhere else. Anything else stops the pass with an error and whatever
// pairs already committed stay committed — each pair is its own upsert set,
// not a slice of one dataset, so there is no torn-set hazard the way there
// is for a paged collection.
func (w *GlobalWorker) doMarketHistoryFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute) (rowsAffected int32, outcome string, err error) {
	pairs, err := s.ListMarketHistoryPairs(ctx, maxMarketHistoryPairs)
	if err != nil {
		return 0, "", fmt.Errorf("worker: listing market-history (region, type) pairs: %w", err)
	}

	outcome = "200"
	for _, p := range pairs {
		resp, doErr := w.Gateway.Do(ctx, esi.Request{
			Method: route.Method, UpstreamPath: route.UpstreamPath,
			PathParams: map[string]string{"region_id": strconv.FormatInt(int64(p.RegionID), 10)},
			Query:      map[string][]string{"type_id": {strconv.FormatInt(int64(p.TypeID), 10)}},
			CacheMode:  derefStr(route.CacheMode),
			// This route carries no x-rate-limit group in the live spec, so
			// RateLimitGroup is empty and Governor 1 is bypassed for it —
			// stated rather than left to be noticed, because it is the
			// reason maxMarketHistoryPairs exists.
			RateLimitGroup:        derefStr(route.RateLimitGroup),
			RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
			RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
			RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
			UserKey:               "hangar:global",
		})
		if doErr != nil {
			if r, ok := classifyRefusal(doErr, w.Policy.TTLFloor, time.Now()); ok {
				return rowsAffected, r.reason, snoozeRefusal(ctx, s, sub.SubscriptionID, r)
			}
			return rowsAffected, normalize.Outcome(0, true), fmt.Errorf("worker: fetching market history for region %d type %d: %w", p.RegionID, p.TypeID, doErr)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			dto, perr := handlers.ParseMarketHistory(resp.Body)
			if perr != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), perr
			}
			res, serr := handlers.SyncMarketHistory(ctx, s, p.RegionID, p.TypeID, dto)
			if serr != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), serr
			}
			rowsAffected += res.RowsAffected
			outcome = normalize.Outcome(resp.StatusCode, false)
		case http.StatusNotFound:
			continue
		case http.StatusTooManyRequests:
			return rowsAffected, normalize.Outcome(resp.StatusCode, false),
				snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)
		default:
			return rowsAffected, normalize.Outcome(resp.StatusCode, false),
				fmt.Errorf("worker: market history for region %d type %d returned status %d", p.RegionID, p.TypeID, resp.StatusCode)
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
