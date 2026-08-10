package worker

// ── THE char-notification 5-TOKEN RESERVE (Phase 14) ────────────────────
//
// GET /characters/{character_id}/notifications declares, in the live
// ingested spec, `x-rate-limit: {group: char-notification, max-tokens: 15,
// window-size: 15m}` and `x-cache-age: 600`. Two consequences, only one of
// which needs new code:
//
//   - The 600-second poll cadence §4.4 asks for falls out of the EXISTING
//     cache-age-driven scheduler (internal/sync.PlanNextDueAt, Phase 6/7)
//     with no new code at all: cache_age IS 600s for this route, and the
//     planner already schedules from it, with jitter. Nothing here needs
//     to special-case the interval.
//   - The 5-token reserve does need something, and this file is it.
//
// char-notification is by a wide margin the tightest bucket in the whole
// spec: 15 tokens per 15 minutes, where the next tightest character group
// is 30 and the typical one is 600. At Governor 1's cost model
// (internal/esi/ratelimit/parse.go: a reservation holds 5, a 2XX settles
// at 2) a mere three in-flight requests exhaust it. If the background
// poller is allowed to spend the bucket down to zero, an operator hitting
// "refresh now" — or a retry after a transient failure — finds no budget
// and is told to come back in fifteen minutes.
//
// ── WHY THE RESERVE LIVES HERE AND NOT IN internal/esi/ratelimit ────────
// ratelimit.AcquireRequest.MaxTokens is a PER-CALL, caller-supplied value:
// internal/esi/client.go's Do passes req.RateLimitMax straight through,
// and LedgerSolo.getBucket re-reads it on every acquire rather than fixing
// it when the bucket is created. The ceiling is therefore whatever the
// caller asks for, on that call, for that caller only.
//
// That makes the reserve a pure call-site policy, and implementing it here
// costs internal/esi/ratelimit — a foundational, heavily tested Phase 4
// component — exactly zero changes for what is one route's policy. The
// background poller asks for (real max - 5) and so can never consume the
// last 5 tokens; any OTHER caller against the same bucket (an interactive
// refresh from the UI, a manual retry) asks for the real 15 and can spend
// the headroom the poller structurally cannot touch.
//
// ── THE ONE PLACE THIS MUST NOT BE APPLIED ──────────────────────────────
// Ledger.Reconcile's maxTokens argument must always be the REAL value.
// Reconcile exists to correct the local ledger against ESI's own
// X-Ratelimit-Remaining — "the server always wins" (§5.5) — and feeding it
// a fictional smaller ceiling would desync the ledger from the truth it is
// there to import. ReconcileRateLimitMax below exists to make that
// distinction impossible to get wrong by accident, and is the value any
// future Reconcile call site must use.
//
// (As of this phase nothing in production calls Reconcile at all — it is
// implemented and unit-tested in internal/esi/ratelimit, but internal/esi.
// Client.Do never invokes it, so there is no live call site to get wrong
// yet. Stated rather than assumed, because "the rule is already enforced
// everywhere" and "there is nowhere it applies yet" look identical from
// the outside.)

// CharNotificationGroup is the x-rate-limit group name of
// GET /characters/{character_id}/notifications, verbatim from the spec.
const CharNotificationGroup = "char-notification"

// CharNotificationReserve is §4.4's "permanent 5-token reserve".
const CharNotificationReserve = 5

// BackgroundRateLimitMax returns the max-tokens a BACKGROUND sync call
// should acquire against for a route in the given rate-limit group: the
// route's real ceiling everywhere except char-notification, where it is
// the real ceiling minus the permanent reserve.
//
// routeMax comes from app.esi_route.rate_limit_max — the ingested spec's
// own value, never a constant here. If CCP raises char-notification to 30,
// the reserve stays 5 tokens and the poller gets 25, with no code change;
// hardcoding 10 would silently cap the poller forever.
//
// A routeMax at or below the reserve yields the real value unchanged
// rather than zero or a negative: a ceiling smaller than the reserve means
// the reserve cannot be honoured at all, and refusing every background
// call would be a worse failure than not reserving. (Not reachable with
// today's spec — 15 > 5 — but a spec change must degrade, not deadlock.)
func BackgroundRateLimitMax(group string, routeMax int32) int {
	max := int(routeMax)
	if group != CharNotificationGroup {
		return max
	}
	if max <= CharNotificationReserve {
		return max
	}
	return max - CharNotificationReserve
}

// ReconcileRateLimitMax returns the REAL ceiling, unreduced — the value
// every Ledger.Reconcile call must pass. See this file's header for why
// the reserve must never reach Reconcile.
func ReconcileRateLimitMax(routeMax int32) int { return int(routeMax) }
