package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// registerMumbleDriver mirrors registerDiscordDriver's shape. When
// HANGAR_MUMBLE_EXTERNAL_AUTHENTICATOR=true, it also starts the
// long-lived authenticator stream goroutine (runMumbleAuthenticator)
// against the FIRST matching platform row — Mumble's external-
// authenticator mode is inherently one-server-at-a-time per gRPC
// connection, matching HANGAR_MUMBLE_GRPC_ADDR being singular.
func registerMumbleDriver(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, drivers *provisioning.Drivers, logger *slog.Logger) error {
	if !cfg.Mumble.Enabled {
		return nil
	}

	s := store.New(pool)
	platforms, err := s.ListPlatforms(ctx)
	if err != nil {
		return fmt.Errorf("mumble: listing platforms: %w", err)
	}

	var mumblePlatformIDs []uuid.UUID
	for _, p := range platforms {
		if p.Kind == "mumble" {
			mumblePlatformIDs = append(mumblePlatformIDs, p.PlatformID)
		}
	}
	if len(mumblePlatformIDs) == 0 {
		logger.Warn("mumble: HANGAR_MUMBLE_ENABLED=true but no app.platform row with kind='mumble' exists yet — no driver registered")
		return nil
	}

	mumbleCfg := mumble.Config{
		Enabled:               cfg.Mumble.Enabled,
		GRPCAddr:              cfg.Mumble.GRPCAddr,
		ServerID:              cfg.Mumble.ServerID,
		TLSCAPath:             cfg.Mumble.TLSCAPath,
		ExternalAuthenticator: cfg.Mumble.ExternalAuthenticator,
		FailClosed:            cfg.Mumble.FailClosed,
		AuthSharedSecret:      cfg.Mumble.AuthSharedSecret.Reveal(),
	}

	client, err := mumble.NewClient(mumbleCfg)
	if err != nil {
		return fmt.Errorf("mumble: constructing client: %w", err)
	}

	driver := mumble.NewDriver(client, 0) // root channel resolved lazily
	for _, platformID := range mumblePlatformIDs {
		drivers.Register(platformID.String(), driver)
	}

	if cfg.Mumble.ExternalAuthenticator {
		authenticator := &mumble.Authenticator{
			Client:     client,
			Decider:    v1.NewMumbleDecider(s, mumblePlatformIDs[0]),
			FailClosed: cfg.Mumble.FailClosed,
			Log:        logger,
		}
		go runMumbleAuthenticator(ctx, authenticator, logger)
	}
	return nil
}

// runMumbleAuthenticator keeps Authenticator.Run alive across
// disconnects — Run itself does not retry (its own doc comment), so this
// is the reconnect loop: a fresh Run call on every non-context-cancelled
// error, with a fixed backoff so a persistently unreachable Murmur
// doesn't spin this goroutine into a hot loop.
func runMumbleAuthenticator(ctx context.Context, a *mumble.Authenticator, logger *slog.Logger) {
	const reconnectDelay = 10 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := a.Run(ctx); err != nil {
			logger.Error("mumble: authenticator stream ended, reconnecting", "error", err, "retry_in", reconnectDelay)
		}
		select {
		case <-time.After(reconnectDelay):
		case <-ctx.Done():
			return
		}
	}
}
