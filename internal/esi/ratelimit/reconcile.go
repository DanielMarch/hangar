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
	if serverRemaining > maxTokens {
		serverRemaining = maxTokens // never exceed max_tokens, per §5.5
	}
	switch {
	case serverRemaining < localAvailable:
		return localAvailable - serverRemaining, 0, false
	case serverRemaining > localAvailable:
		return 0, serverRemaining, true
	default:
		return 0, 0, false
	}
}
