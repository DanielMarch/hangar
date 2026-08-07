package scopes

// NeedsReauthorization reports whether granted lacks any scope in
// required. Route handlers (a later phase) call this before an ESI
// request that needs a scope the stored token might not carry, and if
// true, direct the character back through the SSO flow requesting
// granted ∪ required — additively, never replacing what was already
// granted, since EVE SSO scopes are cumulative per authorization.
func NeedsReauthorization(granted Set, required []string) bool {
	return len(granted.Missing(required)) > 0
}

// MergeScopes returns granted ∪ additional, deduplicated, for building the
// scope list of a re-authorization request. Order is not meaningful —
// scopes are an opaque set, never a sequence a grammar could be inferred
// from.
func MergeScopes(granted []string, additional []string) []string {
	set := NewSet(granted)
	for _, s := range additional {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}
