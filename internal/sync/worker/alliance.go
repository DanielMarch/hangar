package worker

// ── PHASE 20.8: THE FOURTH WORKER, AND WHY IT IS A WORKER ────────────────
//
// Capability #37 was recorded unreachable through 20.7 with an accurate
// description of the blocker: "internal/sync.EntityAlliance exists in the
// vocabulary, but there is no AllianceWorker and no elector for it —
// DispatchWorker routes character, corporation and global only. Making these
// two routes work means building that worker, which is a phase's own piece
// of work and not a map entry." This file is that piece of work.
//
// ── WHY NOT A GLOBAL FAN-OUT OVER app.alliance ───────────────────────────
// The tempting cheaper design is one global subscription per alliance route,
// fanning out over the rows of app.alliance the way the market-history and
// station fan-outs do over ids in HANGAR's own tables. It was rejected for
// two reasons, and the second is decisive.
//
//  1. Every one of these four routes takes exactly ONE path parameter, and
//     it is the alliance id — which is precisely what a subscription's
//     entity_id column holds. There is no second identifier to enumerate, so
//     there is nothing for a fan-out to be a fan-out OVER. These are plain
//     dispatch routes, structurally identical to the corporation ones.
//
//  2. TWO OF THEM NEED A SCOPE, AND A GLOBAL SUBSCRIPTION CANNOT CARRY ONE.
//     db/queries/sync_subscription.sql's ReconcileGlobalSubscriptions has no
//     scope gate — a global row has acting_character_id = NULL and there is
//     no token to gate on — and DisableUnscopedSubscriptions reads a NULL
//     acting character as "fully covered". So a scoped route placed in the
//     global set would be created ENABLED and 403 on every attempt forever,
//     spending Governor 2's installation-wide error budget on requests that
//     cannot succeed. That invariant is now machine-checked
//     (TestGlobalRoutesRequireNoScope); this worker is what makes it possible
//     to satisfy it.
//
// ── THE SET IS BOUNDED, WHICH IS WHY PER-ALLIANCE ROWS ARE AFFORDABLE ────
// app.alliance is not a directory of New Eden. Its only writer is
// UpsertAllianceStub and it has exactly two callers — the character sheet
// sync and the corporation sheet sync — each inserting the alliance of an
// entity HANGAR already tracks. So the table holds one row per alliance the
// installation has a relationship with, and four subscriptions per row is
// the same order of magnitude as the corporation set.
//
// ── NOTHING HERE HAS EVER RETURNED DATA ON THIS INSTALLATION ─────────────
// app.alliance holds 0 rows at the time of writing: HANGAR Corp is in no
// alliance, so ReconcileAllianceSubscriptions produces no rows and this
// worker is never dispatched. That is the correct behaviour for the state,
// not evidence that it works. What has been exercised is the SQL and the
// dispatch, against a seeded integration database
// (alliance_integration_test.go). Verifying it against real ESI requires an
// operator to put a tracked character into an alliance; it is not something
// this phase could simulate.

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

const (
	allianceSheetPath         = "/alliances/{alliance_id}"
	allianceContactsPath      = "/alliances/{alliance_id}/contacts"
	allianceContactLabelsPath = "/alliances/{alliance_id}/contacts/labels"
	allianceCorporationsPath  = "/alliances/{alliance_id}/corporations"
)

// allianceHandler is the alliance-domain twin of characterHandler and
// corporationHandler: raw response body in, rows-affected out, with the
// owner id supplied by the subscription.
type allianceHandler func(ctx context.Context, s *store.Store, allianceID int64, body []byte) (int32, error)

// allianceDispatch is the whole of capability #37's delivery. Four routes,
// four handlers, no fan-outs — the alliance id IS the only path parameter.
var allianceDispatch = map[string]allianceHandler{
	allianceSheetPath: func(ctx context.Context, s *store.Store, allianceID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseAllianceSheet(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncAllianceSheet(ctx, s, allianceID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	allianceContactsPath: func(ctx context.Context, s *store.Store, allianceID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseAllianceContacts(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncAllianceContacts(ctx, s, allianceID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	allianceContactLabelsPath: func(ctx context.Context, s *store.Store, allianceID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseAllianceContactLabels(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncAllianceContactLabels(ctx, s, allianceID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
	allianceCorporationsPath: func(ctx context.Context, s *store.Store, allianceID int64, body []byte) (int32, error) {
		dto, err := handlers.ParseAllianceCorporations(body)
		if err != nil {
			return 0, err
		}
		res, err := handlers.SyncAllianceCorporations(ctx, s, allianceID, dto)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected, nil
	},
}

// alliancePagePaginatedRoutes: the contacts route declares a `page` query
// parameter in the live spec, exactly as its character and corporation twins
// do. Walking it matters — a large alliance's standings list runs to
// thousands of entries, and syncing page 1 only would then PRUNE every
// contact on pages 2..n, because SyncAllianceContacts is a full-state sync.
// That is a strictly worse failure than not syncing at all, which is why the
// walker is wired from the first commit rather than recorded as a known gap
// the way the character wallet journal was for thirteen phases (B31).
var alliancePagePaginatedRoutes = map[string]bool{
	allianceContactsPath: true,
}

// AllianceWorker executes "sync_route" jobs for entity_kind = "alliance"
// subscriptions.
//
// Like CorporationWorker and unlike CharacterWorker it elects an acting
// character on every attempt, because an alliance has no token. The pool is
// different — every tracked character whose corporation is in the alliance —
// and there is no ROLE requirement, because ESI's alliance routes require
// only the scope and membership. internal/sync/election.go carries both
// branches.
type AllianceWorker struct {
	river.WorkerDefaults[planner.SyncJobArgs]

	Pool    store.Pool
	Gateway *esi.Client
	Tokens  *sso.Refresher
	Policy  sync.PolicyConfig
	Elector sync.ActingCharacterElector
}

// Work implements river.Worker[planner.SyncJobArgs].
func (w *AllianceWorker) Work(ctx context.Context, job *river.Job[planner.SyncJobArgs]) error {
	args := job.Args
	if args.EntityKind != sync.EntityAlliance {
		return fmt.Errorf("worker: alliance worker received non-alliance subscription %s (entity_kind=%q)", args.SubscriptionID, args.EntityKind)
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

	handler, ok := allianceDispatch[route.UpstreamPath]
	if !ok {
		return fmt.Errorf("worker: no alliance handler registered for route %s (%s)", route.UpstreamPath, route.OperationID)
	}

	// Elect fresh on every attempt, for the reason CorporationWorker does:
	// this Work call IS one attempt, and a 403 recorded now must steer the
	// NEXT attempt to a different candidate rather than being retried inline.
	//
	// The election runs even for the two PUBLIC routes (the sheet and the
	// member list). That is deliberate: an alliance-scoped subscription with
	// no eligible character means HANGAR has lost its foothold in the
	// alliance entirely, and reporting that uniformly across all four routes
	// is more useful than having two of them quietly keep working
	// unauthenticated while the two that matter go dark. The token is simply
	// carried on requests that do not need it, which ESI ignores.
	characterID, err := w.Elector.Elect(ctx, args.EntityKind, args.EntityID, sub.RouteID)
	if err != nil {
		if errors.Is(err, sync.ErrNoEligibleActingCharacter) {
			return w.recordUnavailable(ctx, s, args.SubscriptionID, err)
		}
		return fmt.Errorf("worker: electing acting character for alliance %d route %s: %w", args.EntityID, sub.RouteID, err)
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

	rowsAffected, outcome, syncErr := w.doSync(ctx, s, sub, route, args.EntityID, characterID, tok.Value, handler)

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

// recordUnavailable mirrors CorporationWorker's: one finished sync_run row
// saying why this attempt produced nothing, and a snooze matched to how fast
// the cause can change. For an alliance the cause is "no tracked character
// in this alliance holds esi-alliances.read_contacts.v1", which clears when
// a human re-authorises — slowly.
func (w *AllianceWorker) recordUnavailable(ctx context.Context, s *store.Store, subscriptionID uuid.UUID, cause error) error {
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

// doSync is CorporationWorker.doSync's shape with the corporation swapped
// for an alliance: the acting character's id keys §5.6's fair-share bucket
// and the 403 history, and the ALLIANCE keys §5.8's entity-scoped breaker —
// one member losing the scope must not break the route for other alliances.
func (w *AllianceWorker) doSync(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, allianceID, characterID int64, accessToken string, handler allianceHandler) (rowsAffected int32, outcome string, err error) {
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
		PathParams:            map[string]string{"alliance_id": strconv.FormatInt(allianceID, 10)},
		AccessToken:           accessToken,
		CacheMode:             derefStr(route.CacheMode),
		RateLimitGroup:        derefStr(route.RateLimitGroup),
		RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
		RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
		RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
		UserKey:               fmt.Sprintf("hangar:%d", characterID),
		EntityID:              allianceID,
		Validators:            validators,
	}

	var resp *esi.Response
	var doErr error
	if alliancePagePaginatedRoutes[route.UpstreamPath] {
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

	switch resp.StatusCode {
	case http.StatusNotModified:
		next, perr := sync.PlanNextDueAt(sync.DueTimeInput{
			Route: routeCfg, Policy: w.Policy, LastSuccess: derefTime(sub.LastSuccessAt),
			Consecutive304: int(sub.Consecutive304) + 1, OptInNoCache: sub.OptInNoCache, Now: time.Now(),
		})
		if perr != nil {
			return 0, outcome, perr
		}
		return 0, outcome, s.RecordSync304(ctx, sub.SubscriptionID, next)

	case http.StatusOK:
		n, syncErr := handler(ctx, s, allianceID, resp.Body)
		if syncErr != nil {
			return 0, outcome, syncErr
		}
		next, perr := sync.PlanNextDueAt(sync.DueTimeInput{
			Route: routeCfg, Policy: w.Policy, LastSuccess: time.Now(),
			Consecutive304: 0, OptInNoCache: sub.OptInNoCache, Now: time.Now(),
		})
		if perr != nil {
			return n, outcome, perr
		}
		if err := s.RecordSyncSuccess(ctx, gen.RecordSyncSuccessParams{
			SubscriptionID: sub.SubscriptionID, LastStatus: statusOf(outcome),
			Etag: nonEmpty(resp.ETag), LastModified: lastModPtr(resp), CursorAfter: sub.CursorAfter,
			NextDueAt: next, Consecutive304: 0,
		}); err != nil {
			return n, outcome, err
		}
		if err := s.ResetActingCharacter403(ctx, gen.ResetActingCharacter403Params{
			EntityKind: string(sync.EntityAlliance), EntityID: allianceID, RouteID: route.RouteID, CharacterID: characterID,
		}); err != nil {
			return n, outcome, err
		}
		return n, outcome, nil

	case http.StatusForbidden:
		// Per-candidate history AND the subscription's own counter, exactly as
		// the corporation worker does: the first steers the next election
		// away from this character, the second drives §5.8's breaker for the
		// alliance as a whole.
		if err := s.RecordSync403(ctx, sub.SubscriptionID); err != nil {
			return 0, outcome, err
		}
		return 0, outcome, s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
			EntityKind: string(sync.EntityAlliance), EntityID: allianceID, RouteID: route.RouteID, CharacterID: characterID,
		})

	case http.StatusTooManyRequests:
		return 0, outcome, snoozeAfter429(ctx, s, sub.SubscriptionID, resp, w.Policy.TTLFloor)

	default:
		return 0, outcome, fmt.Errorf("worker: unexpected status %d from %s", resp.StatusCode, route.UpstreamPath)
	}
}
