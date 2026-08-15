package worker

// ── PHASE 20.5, DEFECT B30: THE FAN-OUT ROUTES COULD NOT BE SCHEDULED ─────
//
// Every "detail" route in HANGAR — a mail body, a starbase's fuel bay, a
// calendar event's text, a project's contributors — is fetched by a fan-out:
// one subscription for the TEMPLATED upstream path, which enumerates the
// second path parameter from rows the parent list sync already landed.
// CorporationWorker.Work and CharacterWorker.Work have carried a `case` for
// each of these since Phase 8.1/9, and each `case` fires only when a
// subscription NAMES that route.
//
// No such subscription could exist. worker/syncset.go's SubscribableRoutes
// is derived from the three dispatch tables, the fan-out paths are in none
// of them, and internal/sync/subscribe reconciles against exactly that list.
// Measured on the live installation at commit 5ebbc56: 70 enabled
// subscriptions, not one of them a fan-out path. So `case starbaseDetailPath`
// — the source §4.4 names for corporation.starbase.fuel_low — had never once
// been reached on any installation, and neither had mail bodies, contract
// items, contract bids, the per-division corporation wallet journal, or the
// project contributors this phase also had to repair the DTO of.
//
// syncset.go's own comment asserted the opposite: "those routes are fetched
// by the parent route's own sync, which knows the id list because it just
// read it". That describes a design nothing implemented. The fix chooses the
// design the CODE has: fan-out paths become SUBSCRIBABLE (fanoutRoutes(),
// syncset.go), because a subscription is what gives a fan-out its own
// cadence, its own etag/snooze bookkeeping, its own sync_run history and its
// own place on the admin board. Folding a detail fetch into the parent's
// handler would have hidden all four inside a route that reports a single
// outcome for two different upstream calls.
//
// ── ONE ENGINE, TWO OWNERS ───────────────────────────────────────────────
// runDetailFanout is worker/corporation.go's fanoutDetail generalised over
// the owner. The corporation form keeps its acting-character 403 history
// (§6.3's elector reads it); the character form has no elector and records
// only the subscription's own 403. That difference is the whole reason this
// is a struct of callbacks rather than one function with a bool.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/normalize"
)

// fanoutDetailItem is one candidate detail call: the id substituted into the
// route's own {id-param} placeholder, plus any extra path/query values that
// specific detail route needs beyond the owner and the id itself (e.g.
// starbase detail's system_id query parameter).
type fanoutDetailItem struct {
	id              int64
	extraPathParams map[string]string
	extraQuery      map[string]string
}

// detailFanout is one fan-out's whole configuration. Everything the engine
// needs is here rather than on a worker receiver, so CharacterWorker and
// CorporationWorker share one implementation without either importing the
// other's bookkeeping.
type detailFanout struct {
	sub   gen.AppSyncSubscription
	route gen.AppEsiRoute

	// ownerParam/ownerID substitute the route's owner placeholder —
	// "character_id" or "corporation_id". idParam names the SECOND dynamic
	// parameter, the one a subscription row cannot carry.
	ownerParam string
	ownerID    int64
	idParam    string

	// actingCharacterID keys §5.6's per-user fair-share bucket. For a
	// character fan-out it is the character itself; for a corporation
	// fan-out it is the elected acting character.
	actingCharacterID int64
	// entityID keys §5.8's entity-scoped 403 breaker: the corporation for a
	// corporation route, the character for a character route.
	entityID    int64
	accessToken string

	items   []fanoutDetailItem
	syncOne func(ctx context.Context, s *store.Store, id int64, body []byte) (int32, error)

	// on403 runs extra bookkeeping beyond RecordSync403 (the corporation
	// elector's per-candidate history). nil is valid and means "nothing
	// beyond the subscription's own counter".
	on403 func(ctx context.Context, s *store.Store) error
	// onSuccess runs after the whole set completed without a refusal —
	// the corporation elector's ResetActingCharacter403. nil is valid.
	onSuccess func(ctx context.Context, s *store.Store) error
}

// runDetailFanout makes one detail call per item and syncs each body.
//
// A 403 on the FIRST item aborts the remaining calls immediately (per-role
// 403s are almost always homogeneous across every item of the same
// owner/route) rather than burning the whole item set before discovering the
// token cannot do this at all; a 404 on any one item is data (the resource
// vanished between the list and detail calls), not a failure, and skips that
// item.
//
// An EMPTY item set is a complete, successful pass, not a no-op to be
// skipped: the parent list is genuinely empty (this corporation has no
// starbases; this character has no calendar events), and recording that as a
// success is what stops the subscription from being re-claimed every 5s
// forever.
func runDetailFanout(ctx context.Context, gw *esi.Client, policy sync.PolicyConfig, s *store.Store, f detailFanout) (rowsAffected int32, outcome string, err error) {
	outcome = "200"
	for _, item := range f.items {
		pathParams := map[string]string{
			f.ownerParam: strconv.FormatInt(f.ownerID, 10),
			f.idParam:    strconv.FormatInt(item.id, 10),
		}
		for k, v := range item.extraPathParams {
			pathParams[k] = v
		}
		var query map[string][]string
		if len(item.extraQuery) > 0 {
			query = make(map[string][]string, len(item.extraQuery))
			for k, v := range item.extraQuery {
				query[k] = []string{v}
			}
		}

		resp, doErr := gw.Do(ctx, esi.Request{
			Method: f.route.Method, UpstreamPath: f.route.UpstreamPath,
			PathParams: pathParams, Query: query, AccessToken: f.accessToken,
			CacheMode:             derefStr(f.route.CacheMode),
			RateLimitGroup:        derefStr(f.route.RateLimitGroup),
			RateLimitMax:          RouteRateLimitMax(derefInt32(f.route.RateLimitMax)),
			RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(f.route.RateLimitGroup), derefInt32(f.route.RateLimitMax)),
			RateLimitWindow:       sync.IntervalToDuration(f.route.RateLimitWindow),
			UserKey:               fmt.Sprintf("hangar:%d", f.actingCharacterID),
			EntityID:              f.entityID,
		})
		if doErr != nil {
			if r, ok := classifyRefusal(doErr, policy.TTLFloor, time.Now()); ok {
				return rowsAffected, r.reason, snoozeRefusal(ctx, s, f.sub.SubscriptionID, r)
			}
			return rowsAffected, normalize.Outcome(0, true), fmt.Errorf("worker: fetching detail for id %d of %s: %w", item.id, f.route.UpstreamPath, doErr)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			n, syncErr := f.syncOne(ctx, s, item.id, resp.Body)
			if syncErr != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), syncErr
			}
			rowsAffected += n
			outcome = normalize.Outcome(resp.StatusCode, false)
		case http.StatusNotFound:
			continue
		case http.StatusForbidden:
			if err := s.RecordSync403(ctx, f.sub.SubscriptionID); err != nil {
				return rowsAffected, normalize.Outcome(resp.StatusCode, false), err
			}
			if f.on403 != nil {
				if err := f.on403(ctx, s); err != nil {
					return rowsAffected, normalize.Outcome(resp.StatusCode, false), err
				}
			}
			return rowsAffected, normalize.Outcome(resp.StatusCode, false), nil
		case http.StatusTooManyRequests:
			return rowsAffected, normalize.Outcome(resp.StatusCode, false),
				snoozeAfter429(ctx, s, f.sub.SubscriptionID, resp, policy.TTLFloor)
		default:
			return rowsAffected, normalize.Outcome(resp.StatusCode, false), fmt.Errorf("worker: detail id %d of %s returned status %d", item.id, f.route.UpstreamPath, resp.StatusCode)
		}
	}

	next, err := sync.PlanNextDueAt(sync.DueTimeInput{
		Route:  sync.RouteCacheConfig{CacheMode: derefStr(f.route.CacheMode), CacheAge: sync.IntervalToDuration(f.route.CacheAge), BlockedByPin: f.route.BlockedByPin},
		Policy: policy, LastSuccess: time.Now(), Consecutive304: 0, OptInNoCache: f.sub.OptInNoCache, Now: time.Now(),
	})
	if err != nil {
		return rowsAffected, outcome, err
	}
	if err := s.RecordSyncSuccess(ctx, gen.RecordSyncSuccessParams{
		SubscriptionID: f.sub.SubscriptionID, LastStatus: statusOf(outcome), CursorAfter: f.sub.CursorAfter,
		NextDueAt: next, Consecutive304: 0,
	}); err != nil {
		return rowsAffected, outcome, err
	}
	if f.onSuccess != nil {
		if err := f.onSuccess(ctx, s); err != nil {
			return rowsAffected, outcome, err
		}
	}
	return rowsAffected, outcome, nil
}
