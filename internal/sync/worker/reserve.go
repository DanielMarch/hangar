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
// ── WHERE THE RESERVE IS APPLIED, AND WHERE IT MUST NOT BE ──────────────
//
// PHASE 20.3 CORRECTION. This header used to argue that the reserve could
// live entirely at the call site, costing internal/esi/ratelimit zero
// changes, because "AcquireRequest.MaxTokens is a PER-CALL, caller-supplied
// value ... the ceiling is therefore whatever the caller asks for, on that
// call, for that caller only". The first half was true; the conclusion was
// not. BOTH ledgers implement admission by comparing consumption against
// the ceiling they have STORED — acquireLedgerEntrySQL's test reads
// `locked.max_tokens`, LedgerSolo's reads `b.maxTokens` — and both write
// AcquireRequest.MaxTokens into that stored ceiling first. The reduced
// number was therefore not per-call at all: it was persisted into
// app.esi_ledger_bucket.max_tokens, a row every caller shares.
//
// That was measurable on a healthy installation. esi_ledger_divergence
// computes local_remaining as max_tokens - consumed and compares it against
// the server's X-Ratelimit-Remaining, which ESI reports against 15. With 10
// stored, char-notification read a PERMANENT divergence of exactly 5
// against Gate 1.3's tolerance of 1 — every other group reading 0 — with
// nothing wrong with the installation. It also meant an interactive caller
// asking for the real 15 flipped the stored ceiling back, and the next
// background call flipped it to 10 again: two callers, one row, a real
// write per flip.
//
// The split is now explicit in the ledger contract itself:
//
//   - AcquireRequest.MaxTokens          — the REAL ceiling. Stored. What
//                                         Reconcile and the divergence
//                                         metric measure against.
//   - AcquireRequest.AdmissionMaxTokens — the ceiling THIS call may admit
//                                         against. Never stored, never
//                                         outlives the call.
//
// So the reserve really is a call-site policy now, and BackgroundRateLimitMax
// still expresses it — it just feeds the admission field instead of the
// stored one. The reserve holds cluster-wide for the same reason it always
// did: every background caller computes the same reduced ceiling, and an
// interactive caller passing no reduction can spend the headroom the poller
// structurally cannot touch.
//
// Reconcile is unaffected and was always correct: it takes the real ceiling
// (Request.RateLimitMax), because "the server always wins" (§5.5) and
// feeding it a fictional smaller ceiling would desync the ledger from the
// truth it is there to import.

// CharNotificationGroup is the x-rate-limit group name of
// GET /characters/{character_id}/notifications, verbatim from the spec.
const CharNotificationGroup = "char-notification"

// CharNotificationReserve is §4.4's "permanent 5-token reserve".
const CharNotificationReserve = 5

// BackgroundRateLimitMax returns the ADMISSION ceiling a BACKGROUND sync
// call should acquire against for a route in the given rate-limit group:
// zero — meaning "no reduction, use the route's real ceiling" — everywhere
// except char-notification, where it is the real ceiling minus the
// permanent reserve.
//
// routeMax comes from app.esi_route.rate_limit_max — the ingested spec's
// own value, never a constant here. If CCP raises char-notification to 30,
// the reserve stays 5 tokens and the poller gets 25, with no code change;
// hardcoding 10 would silently cap the poller forever.
//
// A routeMax at or below the reserve yields zero (no reduction) rather than
// zero-or-negative headroom: a ceiling smaller than the reserve means the
// reserve cannot be honoured at all, and refusing every background call
// would be a worse failure than not reserving. (Not reachable with today's
// spec — 15 > 5 — but a spec change must degrade, not deadlock.)
func BackgroundRateLimitMax(group string, routeMax int32) int {
	if group != CharNotificationGroup {
		return 0
	}
	max := int(routeMax)
	if max <= CharNotificationReserve {
		return 0
	}
	return max - CharNotificationReserve
}

// RouteRateLimitMax returns the route's REAL ceiling — the value
// esi.Request.RateLimitMax must always carry, on every call site, because
// it is what the ledger persists as the bucket's max_tokens and what
// Reconcile and esi_ledger_divergence both measure against. It exists so
// that a call site reads as a pair (real ceiling, admission ceiling) and
// the two can never be transposed by accident.
//
// It was ReconcileRateLimitMax until Phase 20.3, when the value stopped
// being reconciliation-specific: the reserve moved off the stored ceiling,
// so the real ceiling is now simply what every caller passes.
func RouteRateLimitMax(routeMax int32) int { return int(routeMax) }
