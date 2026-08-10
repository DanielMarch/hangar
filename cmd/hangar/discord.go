package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/discord"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// registerDiscordDriver wires Phase 12's Discord driver into drivers when
// HANGAR_DISCORD_ENABLED=true (internal/config.Validate already refused
// to boot with an invalid Discord config if enabled — see validate.go).
// Discord is strictly optional (Principle 7), so a disabled or
// not-yet-linked installation must boot cleanly with no driver registered
// at all, never an error.
//
// One HANGAR installation currently configures exactly one Discord guild
// (HANGAR_DISCORD_GUILD_ID is singular), but the driver is registered
// against whichever app.platform row(s) actually have kind='discord' —
// there can be zero (an administrator hasn't created the platform record
// via the future admin API, Phase 15, yet) or one. Zero is a warning, not
// a fatal error: the process still needs to serve every OTHER queue.
func registerDiscordDriver(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, drivers *provisioning.Drivers) error {
	if !cfg.Discord.Enabled {
		return nil
	}

	s := store.New(pool)
	platforms, err := s.ListPlatforms(ctx)
	if err != nil {
		return fmt.Errorf("discord: listing platforms: %w", err)
	}

	var discordPlatformIDs []string
	for _, p := range platforms {
		if p.Kind == "discord" {
			discordPlatformIDs = append(discordPlatformIDs, p.PlatformID.String())
		}
	}
	if len(discordPlatformIDs) == 0 {
		slog.Default().Warn("discord: HANGAR_DISCORD_ENABLED=true but no app.platform row with kind='discord' exists yet — no driver registered")
		return nil
	}

	discordCfg := discord.Config{
		Enabled:             cfg.Discord.Enabled,
		BotToken:            cfg.Discord.BotToken.Reveal(),
		GuildID:             cfg.Discord.GuildID,
		APIVersion:          cfg.Discord.APIVersion,
		Allowlist:           cfg.Discord.Allowlist,
		GlobalRate:          cfg.Discord.GlobalRate,
		InvalidBudgetMax:    cfg.Discord.InvalidBudgetMax,
		InvalidWarnPercent:  cfg.Discord.InvalidWarnPercent,
		InvalidPausePercent: cfg.Discord.InvalidPausePercent,
	}

	budget := discord.NewInvalidBudget(s, discord.InvalidBudgetWindow, discordCfg.InvalidBudgetMax, discordCfg.InvalidWarnPercent, discordCfg.InvalidPausePercent, discord.SystemClock, slog.Default())
	if err := budget.Init(ctx); err != nil {
		return fmt.Errorf("discord: initialising invalid-request budget: %w", err)
	}

	driver, err := discord.NewDriver(discordCfg, budget, discord.SystemClock)
	if err != nil {
		return fmt.Errorf("discord: constructing driver: %w", err)
	}

	for _, platformID := range discordPlatformIDs {
		drivers.Register(platformID, driver)
	}
	return nil
}
