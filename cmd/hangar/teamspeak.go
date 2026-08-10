package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/teamspeak"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// registerTeamSpeakDriver mirrors registerDiscordDriver's shape exactly
// (discord.go's doc comment covers the "zero platform rows is a warning,
// not fatal" reasoning — identical here).
func registerTeamSpeakDriver(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, drivers *provisioning.Drivers) error {
	if !cfg.TeamSpeak.Enabled {
		return nil
	}

	s := store.New(pool)
	platforms, err := s.ListPlatforms(ctx)
	if err != nil {
		return fmt.Errorf("teamspeak: listing platforms: %w", err)
	}

	var teamspeakPlatformIDs []string
	for _, p := range platforms {
		if p.Kind == "teamspeak" {
			teamspeakPlatformIDs = append(teamspeakPlatformIDs, p.PlatformID.String())
		}
	}
	if len(teamspeakPlatformIDs) == 0 {
		slog.Default().Warn("teamspeak: HANGAR_TEAMSPEAK_ENABLED=true but no app.platform row with kind='teamspeak' exists yet — no driver registered")
		return nil
	}

	client := teamspeak.NewClient(cfg.TeamSpeak.WebQueryURL, cfg.TeamSpeak.APIKey.Reveal(), cfg.TeamSpeak.VirtualServerID, nil)
	driver := teamspeak.NewDriver(client)
	for _, platformID := range teamspeakPlatformIDs {
		drivers.Register(platformID, driver)
	}
	return nil
}
