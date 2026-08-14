package worker

import (
	"context"
	"fmt"
	"net/http"
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
}

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

	handler, ok := globalDispatch[route.UpstreamPath]
	if !ok {
		return fmt.Errorf("worker: no global handler registered for route %s (%s)", route.UpstreamPath, route.OperationID)
	}

	run, err := s.StartSyncRun(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: starting sync run for %s: %w", args.SubscriptionID, err)
	}

	rowsAffected, outcome, syncErr := w.doSync(ctx, s, sub, route, handler)

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
