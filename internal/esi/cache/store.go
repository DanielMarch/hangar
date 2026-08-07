package cache

import "context"

// Store orchestrates L1 and L2 behind the no-store contract: for a
// no-store scheduling mode, Get always misses and Set always no-ops — "no
// L1 write, no L2 write" (01_ARCHITECTURE.md §5.4) enforced in exactly one
// place, so a caller cannot accidentally implement only the first half.
// (ApplyConditionalHeaders in conditional.go is the third half — "and no
// conditional headers sent" — kept as its own function because it operates
// on an *http.Request, not the cache, but gated by the same IsNoStore.)
type Store struct {
	L1 *L1
	L2 L2 // nil is valid: Postgres-absent, Redis-absent test/dev configurations
}

// Get returns the cached entry for key under scheduling mode. A no-store
// mode always misses, regardless of what L1/L2 might otherwise hold — this
// matters because a route can flip from cached to no-store between two
// ingests (Phase 2), and a stale L1/L2 entry from before the flip must
// never be served once the mode says not to.
func (s *Store) Get(ctx context.Context, key string, mode string) (Entry, bool) {
	if IsNoStore(mode) {
		return Entry{}, false
	}
	if e, ok := s.L1.Get(key); ok {
		return e, true
	}
	if s.L2 == nil {
		return Entry{}, false
	}
	e, ok := s.L2.Get(ctx, key)
	if !ok {
		return Entry{}, false
	}
	s.L1.Set(key, e) // promote L2 hit into L1
	return e, true
}

// Set writes e to both tiers under scheduling mode, unless mode is
// no-store, in which case it writes to neither.
func (s *Store) Set(ctx context.Context, key string, e Entry, mode string) {
	if IsNoStore(mode) {
		return
	}
	s.L1.Set(key, e)
	if s.L2 != nil {
		s.L2.Set(ctx, key, e)
	}
}
