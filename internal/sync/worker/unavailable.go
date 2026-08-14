package worker

// ── PHASE 20.2: A REFUSAL IS NOT A FAILURE ───────────────────────────────
//
// internal/esi.Client.Do can decline to make a call for four reasons that
// are all the gateway working CORRECTLY:
//
//	Governor 1 has no headroom            (*ratelimit.RetryAtError)
//	Governor 2 has paused the install     (esi.ErrErrorBudgetPaused)
//	the route's 5XX breaker is open       (esi.ErrBreakerOpen)
//	the entity's 403 breaker is open      (esi.ErrEntityBreakerOpen)
//
// Before this phase every one of them came back to the worker as a plain
// error, which River records as a FAILED JOB and retries on its own
// exponential backoff. That is wrong in three separate ways. It fills
// river_job with failures for a healthy installation; it retries on a
// schedule that has nothing to do with when the budget will actually be
// available; and, worst, §5.5 is explicit that the caller "snoozes the
// subscription; it does not spin" — so retrying is the specified behaviour
// inverted.
//
// The correct response to all four is the same shape: snooze THIS
// subscription until the refusal can plausibly have cleared, record why on
// the sync_run row, and return success. Siblings are untouched, which is
// Principle 3 and is what Gate 1.5 measures.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync"
)

// Outcome strings written to app.sync_run.outcome for a refusal. The column
// is an open vocabulary (02_DATABASE_SCHEMA.md §4.3 #32) and these are
// deliberately NOT status codes: nothing was sent, so there is no status to
// report, and writing "error" would file a working governor alongside a
// genuine fault.
const (
	outcomeRateLimited       = "unavailable:rate_limited"
	outcomeErrorBudgetPaused = "unavailable:error_budget_paused"
	outcomeRouteBreakerOpen  = "unavailable:route_breaker_open"
	outcomeEntityBreakerOpen = "unavailable:entity_breaker_open"
	outcomeNoActingCharacter = "unavailable:no_eligible_acting_character"
)

// refusal is a classified gateway refusal: when to try again, and what to
// record.
type refusal struct {
	until  time.Time
	reason string
}

// classifyRefusal reports whether err is one of the four deliberate
// refusals above, and if so when the subscription should wake up.
//
// ttlFloor is HANGAR_ESI_TTL_FLOOR, used where the wait has no better
// estimate. The two breaker cases use breaker.Default*ProbeTTL rather than
// the floor, because the breaker itself will refuse again until its probe
// interval elapses and there is no point waking up before then — see that
// constant's comment for why the pair must not drift.
func classifyRefusal(err error, ttlFloor time.Duration, now time.Time) (refusal, bool) {
	if err == nil {
		return refusal{}, false
	}

	var retryAt *ratelimit.RetryAtError
	if errors.As(err, &retryAt) {
		until := retryAt.RetryAt
		if !until.After(now) {
			// A RetryAt already in the past would snooze for zero and spin.
			// The floor is the honest minimum.
			until = now.Add(ttlFloor)
		}
		return refusal{until: until, reason: outcomeRateLimited}, true
	}

	switch {
	case errors.Is(err, esi.ErrErrorBudgetPaused):
		return refusal{until: now.Add(ttlFloor), reason: outcomeErrorBudgetPaused}, true
	case errors.Is(err, esi.ErrEntityBreakerOpen):
		return refusal{until: now.Add(breaker.DefaultEntityProbeTTL), reason: outcomeEntityBreakerOpen}, true
	case errors.Is(err, esi.ErrBreakerOpen):
		return refusal{until: now.Add(breaker.DefaultRouteProbeTTL), reason: outcomeRouteBreakerOpen}, true
	case errors.Is(err, sync.ErrNoEligibleActingCharacter):
		// Not a gateway refusal — nothing was even attempted — but the
		// caller's handling is identical, and routing it through the same
		// classifier is what stops the two drifting into different
		// behaviours for the same operator-visible symptom ("this
		// corporation shows no data and nothing says why").
		return refusal{until: now.Add(breaker.DefaultEntityProbeTTL), reason: outcomeNoActingCharacter}, true
	}
	return refusal{}, false
}

// snoozeRefusal writes the refusal to app.sync_subscription.snoozed_until.
// ClaimDueSubscriptions already excludes snoozed rows IN ITS PREDICATE (see
// db/queries/sync_subscription.sql), so this is what actually stops the
// planner burning a claim slot on the subscription every five seconds until
// the wait is over.
func snoozeRefusal(ctx context.Context, s *store.Store, subscriptionID uuid.UUID, r refusal) error {
	until := r.until
	return s.SnoozeSyncSubscription(ctx, subscriptionID, &until)
}

// snoozeAfter429 honours §5.5's 429 rule: charge nothing (Governor 1
// already did — Cost429 is 0), snooze THIS subscription for exactly
// Retry-After when the response carried one, otherwise for ttl_floor, and
// leave every sibling alone.
//
// resp.SnoozeFor is computed by ratelimit.ClassifyResponse, which is the
// only place that rule is expressed; the fallback here exists solely for a
// Client constructed with no TTLFloor at all (a unit test), so that a
// misconfiguration cannot produce a snooze of zero and a spin.
func snoozeAfter429(ctx context.Context, s *store.Store, subscriptionID uuid.UUID, resp *esi.Response, ttlFloor time.Duration) error {
	d := resp.SnoozeFor
	if d <= 0 {
		d = ttlFloor
	}
	if d <= 0 {
		d = time.Minute
	}
	until := time.Now().Add(d)
	return s.SnoozeSyncSubscription(ctx, subscriptionID, &until)
}
