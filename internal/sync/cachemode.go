// Package sync implements Phase 6's sync engine: the subscription model,
// due-time computation, and cache-mode policy that the leader-elected
// planner (internal/sync/planner) and Phase 7+'s route-handler workers
// share (01_ARCHITECTURE.md §6).
package sync

// CacheMode is app.esi_route.cache_mode's resolved value — never the raw,
// possibly-absent header string. ParseCacheMode is the only place that
// string gets interpreted.
type CacheMode string

const (
	// CacheModeTTLBased polls at max(x-cache-age, ttl_floor). This is the
	// default: an absent header AND any value this build doesn't
	// recognise both resolve here (§6.2 — "the fallback for any
	// unrecognised future value").
	CacheModeTTLBased CacheMode = "ttl-based"
	// CacheModeEventBased treats x-cache-age as a hint, not a contract:
	// poll at max(x-cache-age, ttl_floor), rely on ETag revalidation, and
	// apply 1.5^n backoff on consecutive 304s.
	CacheModeEventBased CacheMode = "event-based"
	// CacheModeNoCache is never written to L1/L2, carries no conditional
	// headers, and is scheduled at ttl_floor only — and only for
	// subscriptions that explicitly opt in (opt_in_no_cache).
	CacheModeNoCache CacheMode = "no-cache"
)

// ParseCacheMode interprets app.esi_route.cache_mode (the raw
// x-server-cache-mode/x-cache-mode value ingested verbatim by Phase 2's
// catalogue — internal/esi/catalogue/ingest.go's Route.CacheMode is stored
// as-is, never normalised, per Principle 14). Per §6.2, absent is the
// majority case and defaults to ttl-based; any value this build doesn't
// recognise — a future CCP addition — falls back to the same default
// rather than erroring, so a spec change degrades gracefully instead of
// halting the planner.
//
// "not-cached" is accepted as a synonym for the no-cache mode this
// package's docs call "no-cache": the live ESI spec actually emits
// "not-cached" (confirmed against
// internal/esi/catalogue/embedded/openapi.snapshot.json), never the literal
// string "no-cache" — 01_ARCHITECTURE.md §6.2's table names the concept,
// not the wire spelling. internal/esi/cache.IsNoStore and
// internal/esi/catalogue.SchedulingMode already carry this same fix for
// the same reason; treating only "no-cache" here would silently schedule
// every real no-cache route as ttl-based (caching and conditionally
// requesting a route the spec says must never be cached).
func ParseCacheMode(raw string) CacheMode {
	switch CacheMode(raw) {
	case CacheModeEventBased:
		return CacheModeEventBased
	case CacheModeNoCache, "not-cached":
		return CacheModeNoCache
	default:
		// "" (absent), CacheModeTTLBased itself, and anything unrecognised.
		return CacheModeTTLBased
	}
}
