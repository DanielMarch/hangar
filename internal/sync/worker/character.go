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

	handler, ok := characterDispatch[route.UpstreamPath]
	if !ok {
		return fmt.Errorf("worker: no character handler registered for route %s (%s)", route.UpstreamPath, route.OperationID)
	}

	tok, err := w.Tokens.EnsureAccessToken(ctx, args.EntityID)
	if err != nil {
		return fmt.Errorf("worker: obtaining access token for character %d: %w", args.EntityID, err)
	}

	run, err := s.StartSyncRun(ctx, args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("worker: starting sync run for %s: %w", args.SubscriptionID, err)
	}

	rowsAffected, outcome, syncErr := w.doSync(ctx, s, sub, route, args.EntityID, tok.Value, handler)

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
		PathParams:      map[string]string{"character_id": strconv.FormatInt(characterID, 10)},
		AccessToken:     accessToken,
		CacheMode:       derefStr(route.CacheMode),
		RateLimitGroup:  derefStr(route.RateLimitGroup),
		RateLimitMax:    int(derefInt32(route.RateLimitMax)),
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
