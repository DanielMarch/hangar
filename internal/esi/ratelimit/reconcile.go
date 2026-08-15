package ratelimit

// reconcileAction is the pure decision core of §5.5's "the server always
// wins" rule, shared by the solo and clustered Reconcile implementations so
// the convergence math is defined and tested exactly once
// (TestServerHeadersAlwaysWin exercises this function directly through both
// backends).
//
//   - serverRemaining < localAvailable: the server has seen more
//     consumption than we have. InjectCost is the synthetic entry's cost —
//     enough, added to what we already hold, to make our view agree.
//   - serverRemaining > localAvailable: we are holding more consumption
//     than the server has. EvictTarget is the localAvailable value to
//     evict oldest entries up to (never past maxTokens, which
//     serverRemaining is already clamped against by the caller).
//   - Equal: no action.
func reconcileAction(maxTokens, localAvailable, serverRemaining int) (injectCost int, evictTarget int, needsEvict bool) {
	serverRemaining = ConvergenceTarget(maxTokens, serverRemaining)
	switch {
	case serverRemaining < localAvailable:
		return localAvailable - serverRemaining, 0, false
	case serverRemaining > localAvailable:
		return 0, serverRemaining, true
	default:
		return 0, 0, false
	}
}

// ConvergenceTarget is the availability §5.5's reconciliation actually aims
// at: the server's reading, clamped to the bucket's own ceiling, because
// "local converges upward, NEVER above max_tokens" (04_RELEASE_GATES.md
// §1.3's adversarial table).
//
// PHASE 20.4.1: exported because the reconciler is no longer its only user.
// esi_ledger_divergence is now the residual AFTER convergence, so whatever
// reads that residual — internal/telemetry's collector, the admin
// rate-limit board — must subtract against the same clamped figure the
// reconciler aimed at. Measuring against a raw server reading above the
// ceiling would report a divergence for obeying §5.5, i.e. would fail the
// gate for passing its own adversarial condition. One definition, three
// callers, so the clamp cannot drift between the correction and the
// measurement of it.
func ConvergenceTarget(maxTokens, serverRemaining int) int {
	if serverRemaining > maxTokens {
		return maxTokens
	}
	return serverRemaining
}
