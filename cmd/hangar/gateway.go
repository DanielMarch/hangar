package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/esi/transport"
	"github.com/hangar-project/hangar/internal/i18n"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// l1CacheMaxCostBytes bounds internal/esi/cache's in-process L1 tier.
const l1CacheMaxCostBytes = 128 * 1024 * 1024 // 128MiB

// buildGateway assembles Phase 7's internal/esi.Client from Phases 2-4's
// pieces — see internal/esi/client.go's package doc for why this assembly
// didn't exist before Phase 7 needed it.
//
// PHASE 20.1: also returns the Governor 1 instance. `esi.Client` holds it
// only as the `ratelimit.Ledger` interface, and the esi_ledger_mode metric
// needs the concrete type's Mode(). Returning it is better than a type
// assertion at the call site: the assertion would compile, then silently
// stop producing the metric the day the ledger is constructed differently.
//
// PHASE 20.2: and the Gate 1 counters, for the same reason — the client
// holds them only as the narrow esi.Observer interface, and the registry
// needs the concrete collector.
func buildGateway(cfg *config.Config, pool *pgxpool.Pool, s *store.Store, logger *slog.Logger) (*esi.Client, *ratelimit.Governor1, *telemetry.GatewayCounters, error) {
	l1, err := cache.NewL1(l1CacheMaxCostBytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gateway: building L1 cache: %w", err)
	}
	l2, err := buildL2(cfg, s, logger)
	if err != nil {
		return nil, nil, nil, err
	}
	cacheStore := &cache.Store{L1: l1, L2: l2}

	pinSource := func() (string, error) {
		pin, err := catalogue.GetPin(context.Background(), s)
		if err != nil {
			return "", fmt.Errorf("gateway: resolving compatibility pin: %w", err)
		}
		return pin.Format("2006-01-02"), nil
	}

	httpClient := esi.NewHTTPClient(transport.Options{
		Version:    version,
		ContactURL: cfg.PublicURL,
		Pin:        pinSource,
		Retry:      transport.DefaultRetryConfig,
	}, cfg.ESI.RequestTimeout)

	governor1 := ratelimit.NewGovernor1(ratelimit.NewLedgerClustered(pool), s, nil, logger)

	governor2 := ratelimit.NewGovernor2(s, cfg.ESI.ErrorLimitWindow, cfg.ESI.ErrorLimitMax,
		cfg.ESI.ErrorLimitPauseAt, cfg.ESI.ErrorLimitResumeAt, nil, logger,
		func(ctx context.Context, name string, attrs map[string]any) {
			logger.ErrorContext(ctx, "hangar: "+name, "attrs", attrs)
		})
	if err := governor2.Init(context.Background()); err != nil {
		return nil, nil, nil, fmt.Errorf("gateway: initialising error budget: %w", err)
	}

	// PHASE 20.2 (B23). The ESI Accept-Language is INSTALLATION-WIDE, not
	// per acting user. Background sync has no acting user to take a
	// preference from, and a per-user value would fragment the ESI cache
	// (the resolved language is part of the key, §5.3) up to ninefold for
	// data that is identical in every locale except a handful of localised
	// name fields. Validate() has already rejected an unknown locale, so a
	// resolution failure here is not reachable through the config path —
	// but it is still fatal rather than silently falling back to "en",
	// because a gateway quietly speaking the wrong language is exactly the
	// class of thing this phase exists to stop.
	language, err := i18n.ResolveESILanguage(cfg.Locale)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gateway: resolving ESI language: %w", err)
	}

	counters := telemetry.NewGatewayCounters()

	return &esi.Client{
		HTTPClient:    httpClient,
		BaseURL:       catalogue.EsiBaseURL,
		Cache:         cacheStore,
		RouteBreaker:  breaker.NewRouteBreaker(breaker.DefaultRouteProbeTTL, nil),
		EntityBreaker: breaker.NewEntityBreaker(breaker.DefaultEntityProbeTTL, nil),
		Ledger:        governor1,
		ErrorBudget:   governor2,
		Metrics:       counters,
		TTLFloor:      cfg.ESI.TTLFloor,
		Language:      language,
		Tenant:        "hangar",
		// PHASE 20.3. The same pinSource the transport already uses for the
		// X-Compatibility-Date HEADER now also keys the CACHE (§5.3's
		// formula names compatibility_date and esi.Client.cacheKey never
		// populated it). One source for both, deliberately: a cache keyed
		// on a different pin from the one the request was made under is
		// worse than no pin in the key at all.
		CompatibilityPin: pinSource,
	}, governor1, counters, nil
}

// buildRefresher assembles the internal/sso.Refresher Phase 7's gateway
// needs for access tokens (§7.3).
func buildRefresher(cfg *config.Config, pool *pgxpool.Pool, keyring *crypto.Keyring) *sso.Refresher {
	return &sso.Refresher{
		Pool:    pool,
		OAuth:   ssoOAuthConfig(cfg), // Phase 15.1: shared with buildSSOFlow (sso.go) so the two can't drift
		Keyring: keyring,
	}
}

// ── PHASE 20.5, DEFECT B34: THE OPTIONAL REDIS L2 TIER ───────────────────
//
// docker-compose.yml has shipped a `cache` profile since Phase 5 and
// internal/esi/cache.NewRedisL2 has been implemented and tested since Phase
// 3. Nothing ever constructed it: buildGateway hard-wired the Postgres L2.
// An operator who ran `docker compose --profile cache up` got a Redis
// container that HANGAR never spoke to, and no signal that it was inert —
// Principle 7 says Redis must be OPTIONAL, and it does not say the option
// should do nothing.
//
// REPLACES, NOT FRONTS. 01_ARCHITECTURE.md §5.4's DECISION is explicit:
// "when HANGAR_REDIS_URL is set, Redis replaces the Postgres L2 table".
// Store holds one L2 for that reason, and this returns one.
//
// ── WHAT A REDIS OUTAGE MID-REQUEST DOES: IT MISSES ──────────────────────
// Not a failure, and NOT a fall-through to Postgres. Both alternatives were
// considered and both are wrong:
//
//   - A FAILURE would make an optional dependency load-bearing, which is
//     Principle 7 inverted. §5.4 already settles it: "a Redis error is
//     logged and treated as a miss."
//
//   - A FALL-THROUGH to Postgres sounds like the generous option and is the
//     incorrect one. When Redis is the L2, nothing has been WRITTEN to
//     app.esi_cache_entry, so the fall-through finds either nothing or rows
//     left over from before Redis was enabled — serving a body the
//     configured cache tier does not contain, aged by however long ago the
//     tier was switched. It would also make the enabled and disabled suites
//     disagree about request counts, which is precisely the property this
//     defect's gate condition demands they share.
//
// A miss costs one conditional revalidation against ESI, which returns 304
// with the validators the SUBSCRIPTION holds (app.sync_subscription.etag) —
// not the cache's. That is why correctness never depended on this tier: the
// authoritative validator state has always been in Postgres.
func buildL2(cfg *config.Config, s *store.Store, logger *slog.Logger) (cache.L2, error) {
	url := cfg.Redis.URL.Reveal()
	if url == "" {
		return cache.NewPostgresL2(s.PostgresCacheL2(), logger), nil
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		// A MISCONFIGURED Redis is fatal at boot even though a BROKEN one is
		// not at request time, and the two are different questions. "This
		// URL is not a Redis URL" is an operator mistake that will never fix
		// itself, and starting anyway would silently demote the tier they
		// just asked for — the exact class of thing this phase exists to
		// stop. An unreachable-but-well-formed URL still boots, because that
		// is a Redis that may come back.
		return nil, fmt.Errorf("gateway: HANGAR_REDIS_URL is not a valid Redis URL: %w", err)
	}
	logger.Info("hangar: esi L2 cache tier is redis",
		"addr", opts.Addr, "db", opts.DB, "prefix", cfg.Redis.Prefix,
		"note", "replaces the Postgres L2 table; a Redis error degrades to a cache miss, never to a request failure")
	return cache.NewRedisL2(redis.NewClient(opts), redisKeyPrefix(cfg.Redis.Prefix), logger), nil
}

// redisKeyPrefix namespaces HANGAR's ESI cache keys inside a Redis an
// operator may well be sharing with something else. The ":esi:" segment is
// added here rather than being part of the configured value so that
// HANGAR_REDIS_PREFIX stays "the name of this installation", which is what
// .env.example describes it as, and so a future second Redis consumer
// cannot collide with this one by inheriting the same bare prefix.
func redisKeyPrefix(prefix string) string {
	if prefix == "" {
		prefix = "hangar"
	}
	return prefix + ":esi:"
}
