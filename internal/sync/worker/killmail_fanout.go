package worker

// ── PHASE 20.7 (B48): THE TWO-STAGE FAN-OUT ──────────────────────────────
//
// Killmails are the one capability in B48 whose shape no existing mechanism
// in this package fits, and the schema is what makes it so.
//
// Every other detail fan-out in HANGAR is: sync the parent LIST into a
// table, then enumerate the detail calls from the stored rows. That is what
// fanoutRoutes() and runDetailFanout are built around, and it is why a
// detail route gets a subscription of its own — its own cadence, etag,
// sync_run history and place on the admin board.
//
// A killmail cannot work that way. The recent-list route returns ONLY
// {killmail_id, killmail_hash}, while app.killmail requires killmail_time,
// solar_system_id, victim_ship_type_id and victim_damage_taken NOT NULL —
// and killmail_time is the PARTITION KEY, so a stub row has no partition to
// land in. There is no such thing as a killmail row that exists before its
// detail has been fetched, so there is nothing for a detail subscription to
// enumerate, so a detail subscription would do nothing on every pass.
//
// The two stages therefore run inside ONE subscription, on the recent route.
// That is a deliberate exception to this package's usual rule and is
// recorded as one in worker/unmapped.go, where the detail route is
// classified ReasonFetchedByParent rather than left looking unnoticed.
//
// ── WHAT IT KEYS ON ──────────────────────────────────────────────────────
// Stage 2's path is /killmails/{killmail_id}/{killmail_hash}: the id is the
// engine's ordinary int64 idParam, and the HASH — a string, and the reason
// this looked awkward — rides in extraPathParams, the same slot starbase
// detail uses for its system_id. No string-id machinery is needed for it at
// all. (The project-detail fan-out next door DOES need idText, because a
// project's uuid is the id itself.)
//
// The detail route is PUBLIC: no scope, no token. Only the two recent-list
// routes are scoped, and those scopes were absent from this installation's
// 47-scope grant when this was written — see docs/SSO_APPLICATION.md.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/internal/sync/normalize"
)

// killmailDetailPath is stage 2. It is unauthenticated and takes no owner.
const killmailDetailPath = "/killmails/{killmail_id}/{killmail_hash}"

// killmailFanout is one owner's two-stage killmail sync.
type killmailFanout struct {
	sub   gen.AppSyncSubscription
	route gen.AppEsiRoute // the RECENT list route — stage 1

	ownerKind  string
	ownerParam string
	ownerID    int64

	actingCharacterID int64
	entityID          int64
	accessToken       string
}

// runKillmailFanout fetches the recent list, discards the killmails already
// stored, and fetches and syncs the detail of the rest.
//
// ── WHY ALREADY-STORED KILLMAILS ARE SKIPPED ─────────────────────────────
// A killmail is immutable: CCP never revises one. So a killmail HANGAR
// already holds never needs fetching again, and in steady state the recent
// list returns almost entirely ids that are already here and the pass makes
// ZERO detail calls. Without that filter this sync would re-fetch every
// killmail in the recent window on every pass, forever, for data that cannot
// have changed — which for an active corporation is hundreds of requests an
// hour against Governor 2's budget to learn nothing.
func runKillmailFanout(ctx context.Context, gw *esi.Client, policy sync.PolicyConfig, s *store.Store, kf killmailFanout) (rowsAffected int32, outcome string, err error) {
	// ── STAGE 1: the recent list ─────────────────────────────────────────
	// Page-paginated (X-Pages), so it goes through the shared walker rather
	// than a single request; a corporation with more than one page of recent
	// kills would otherwise sync only the first.
	var validators *cache.Validators
	if kf.sub.Etag != nil {
		validators = &cache.Validators{ETag: *kf.sub.Etag}
	}

	resp, doErr := fetchAllPages(ctx, gw, esi.Request{
		Method: kf.route.Method, UpstreamPath: kf.route.UpstreamPath,
		PathParams:            map[string]string{kf.ownerParam: strconv.FormatInt(kf.ownerID, 10)},
		AccessToken:           kf.accessToken,
		CacheMode:             derefStr(kf.route.CacheMode),
		RateLimitGroup:        derefStr(kf.route.RateLimitGroup),
		RateLimitMax:          RouteRateLimitMax(derefInt32(kf.route.RateLimitMax)),
		RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(kf.route.RateLimitGroup), derefInt32(kf.route.RateLimitMax)),
		RateLimitWindow:       sync.IntervalToDuration(kf.route.RateLimitWindow),
		UserKey:               fmt.Sprintf("hangar:%d", kf.actingCharacterID),
		EntityID:              kf.entityID,
		Validators:            validators,
	})
	if doErr != nil {
		if r, ok := classifyRefusal(doErr, policy.TTLFloor, time.Now()); ok {
			return 0, r.reason, snoozeRefusal(ctx, s, kf.sub.SubscriptionID, r)
		}
		return 0, normalize.Outcome(0, true), doErr
	}

	routeCfg := sync.RouteCacheConfig{
		CacheMode: derefStr(kf.route.CacheMode), CacheAge: sync.IntervalToDuration(kf.route.CacheAge),
		BlockedByPin: kf.route.BlockedByPin,
	}
	outcome = normalize.Outcome(resp.StatusCode, false)

	switch resp.StatusCode {
	case http.StatusNotModified:
		next, perr := sync.PlanNextDueAt(sync.DueTimeInput{
			Route: routeCfg, Policy: policy, LastSuccess: derefTime(kf.sub.LastSuccessAt),
			Consecutive304: int(kf.sub.Consecutive304) + 1, OptInNoCache: kf.sub.OptInNoCache, Now: time.Now(),
		})
		if perr != nil {
			return 0, outcome, perr
		}
		return 0, outcome, s.RecordSync304(ctx, kf.sub.SubscriptionID, next)
	case http.StatusOK:
		// fall through
	case http.StatusForbidden:
		return 0, outcome, s.RecordSync403(ctx, kf.sub.SubscriptionID)
	case http.StatusTooManyRequests:
		return 0, outcome, snoozeAfter429(ctx, s, kf.sub.SubscriptionID, resp, policy.TTLFloor)
	default:
		return 0, outcome, fmt.Errorf("worker: killmail list for %s %d returned status %d", kf.ownerKind, kf.ownerID, resp.StatusCode)
	}

	refs, perr := handlers.ParseKillmailRefs(resp.Body)
	if perr != nil {
		return 0, outcome, perr
	}

	known, kerr := s.ListKnownKillmailIDs(ctx, kf.ownerKind, kf.ownerID)
	if kerr != nil {
		return 0, outcome, fmt.Errorf("worker: listing known killmails for %s %d: %w", kf.ownerKind, kf.ownerID, kerr)
	}
	have := make(map[int64]bool, len(known))
	for _, id := range known {
		have[id] = true
	}

	// ── STAGE 2: the detail of each killmail not already stored ──────────
	// The detail route is a DIFFERENT catalogue row from the one this
	// subscription names, so it is read from app.esi_route rather than
	// assumed: its own cache mode, rate-limit group and pin state govern the
	// call, exactly as Principle 5 requires of every request HANGAR makes.
	detailRoute, rerr := s.GetEsiRouteByMethodAndPath(ctx, http.MethodGet, killmailDetailPath)
	if rerr != nil {
		return 0, outcome, fmt.Errorf("worker: reading the killmail detail route from the catalogue: %w", rerr)
	}
	if detailRoute.BlockedByPin {
		// The list succeeded and the detail route is blocked by the
		// compatibility pin. Recording the list's success would claim a
		// complete pass; this says what actually happened instead.
		return 0, outcome, fmt.Errorf("worker: killmail detail route is blocked by the compatibility pin; %d killmails could not be fetched", len(refs))
	}

	var items []fanoutDetailItem
	for _, r := range refs {
		if have[r.KillmailID] {
			continue
		}
		items = append(items, fanoutDetailItem{
			id:              r.KillmailID,
			extraPathParams: map[string]string{"killmail_hash": r.KillmailHash},
		})
	}

	// The detail route is public and takes no owner, so no access token is
	// passed and ownerParam names a placeholder the path does not contain —
	// esi.Client.buildURL ignores path params with no matching placeholder,
	// which is what lets one engine serve both owned and unowned details.
	return runDetailFanout(ctx, gw, policy, s, detailFanout{
		sub: kf.sub, route: detailRoute,
		ownerParam: kf.ownerParam, ownerID: kf.ownerID, idParam: "killmail_id",
		actingCharacterID: kf.actingCharacterID, entityID: kf.entityID,
		items: items,
		syncOne: func(ctx context.Context, s *store.Store, item fanoutDetailItem, body []byte) (int32, error) {
			dto, derr := handlers.ParseKillmailDetail(body)
			if derr != nil {
				return 0, derr
			}
			res, serr := handlers.SyncKillmail(ctx, s, kf.ownerKind, kf.ownerID, item.extraPathParams["killmail_hash"], dto)
			if serr != nil {
				return 0, serr
			}
			return res.RowsAffected, nil
		},
		etag: nonEmpty(resp.ETag),
	})
}

// doKillmailFanout is CorporationWorker's entry point into the two-stage
// sync. The corporation form records the elector's per-candidate 403 history
// on a refusal, exactly as its other fan-outs do.
func (w *CorporationWorker) doKillmailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, corporationID, characterID int64, accessToken string) (int32, string, error) {
	return runKillmailFanout(ctx, w.Gateway, w.Policy, s, killmailFanout{
		sub: sub, route: route,
		ownerKind: "corporation", ownerParam: "corporation_id", ownerID: corporationID,
		actingCharacterID: characterID, entityID: corporationID, accessToken: accessToken,
	})
}

// doKillmailFanout is CharacterWorker's entry point. A character is its own
// acting character and its own 403-breaker entity, so both ids are the
// character.
func (w *CharacterWorker) doKillmailFanout(ctx context.Context, s *store.Store, sub gen.AppSyncSubscription, route gen.AppEsiRoute, characterID int64, accessToken string) (int32, string, error) {
	return runKillmailFanout(ctx, w.Gateway, w.Policy, s, killmailFanout{
		sub: sub, route: route,
		ownerKind: "character", ownerParam: "character_id", ownerID: characterID,
		actingCharacterID: characterID, entityID: characterID, accessToken: accessToken,
	})
}
