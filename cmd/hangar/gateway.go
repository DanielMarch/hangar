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
	cacheStore := &cache.Store{L1: l1, L2: cache.NewPostgresL2(s.PostgresCacheL2(), logger)}

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
