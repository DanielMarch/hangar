package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/esi/transport"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// routeBreakerProbeTTL bounds how long an open circuit stays open before
// allowing one probe request through (§ breaker half-open behaviour).
const routeBreakerProbeTTL = 60 * time.Second

// l1CacheMaxCostBytes bounds internal/esi/cache's in-process L1 tier.
const l1CacheMaxCostBytes = 128 * 1024 * 1024 // 128MiB

// buildGateway assembles Phase 7's internal/esi.Client from Phases 2-4's
// pieces — see internal/esi/client.go's package doc for why this assembly
// didn't exist before Phase 7 needed it.
func buildGateway(cfg *config.Config, pool *pgxpool.Pool, s *store.Store, logger *slog.Logger) (*esi.Client, error) {
	l1, err := cache.NewL1(l1CacheMaxCostBytes)
	if err != nil {
		return nil, fmt.Errorf("gateway: building L1 cache: %w", err)
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
		return nil, fmt.Errorf("gateway: initialising error budget: %w", err)
	}

	return &esi.Client{
		HTTPClient:   httpClient,
		BaseURL:      catalogue.EsiBaseURL,
		Cache:        cacheStore,
		RouteBreaker: breaker.NewRouteBreaker(routeBreakerProbeTTL, nil),
		Ledger:       governor1,
		ErrorBudget:  governor2,
		TTLFloor:     cfg.ESI.TTLFloor,
		Tenant:       "hangar",
	}, nil
}

// buildRefresher assembles the internal/sso.Refresher Phase 7's gateway
// needs for access tokens (§7.3).
func buildRefresher(cfg *config.Config, pool *pgxpool.Pool, keyring *crypto.Keyring) *sso.Refresher {
	return &sso.Refresher{
		Pool: pool,
		OAuth: sso.OAuthConfig{
			ClientID: cfg.SSO.ClientID, ClientSecret: cfg.SSO.ClientSecret.Reveal(),
			CallbackURL: cfg.SSO.CallbackURL, AuthorizeURL: cfg.SSO.AuthorizeURL, TokenURL: cfg.SSO.TokenURL,
		},
		Keyring: keyring,
	}
}
