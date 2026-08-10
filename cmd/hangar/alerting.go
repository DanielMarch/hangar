package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// buildAlertDispatcher assembles Phase 14's outbox pump from configuration
// (SRS §4.4). It never fails: an installation with no channels configured
// is a valid installation, and the pump simply finds nothing to claim.
func buildAlertDispatcher(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) *alerting.Dispatcher {
	return &alerting.Dispatcher{
		Pool: pool,
		Policy: alerting.RetryPolicy{
			MaxAttempts:     cfg.Alerting.MaxAttempts,
			Base:            cfg.Alerting.RetryBase,
			Cap:             cfg.Alerting.RetryCap,
			DeadLetterAfter: cfg.Alerting.DeadLetterAfter,
		},
		ClaimSize: cfg.Alerting.ClaimSize,
		Log:       logger.With("component", "alerting.dispatcher"),
	}
}

// runAlertDispatcher pumps the outbox until ctx is cancelled.
//
// A tick failure is LOGGED AND SWALLOWED, never returned: this loop runs
// alongside the River worker pool, and a transient database hiccup while
// claiming deliveries must not take the whole worker process down —
// §4.4's "never block the queue" applies to the pump's own supervision as
// much as to any one delivery. A genuinely broken database will fail the
// heartbeat, which is the thing whose job it is to say so.
func runAlertDispatcher(ctx context.Context, dispatcher *alerting.Dispatcher, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := dispatcher.Tick(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				logger.Error("alerting: dispatch tick failed", "error", err)
				continue
			}
			if result.Claimed > 0 {
				logger.Info("alerting: dispatch tick",
					"claimed", result.Claimed, "groups", result.Groups,
					"sent", result.Sent, "retried", result.Retried, "dead_lettered", result.DeadLettered)
			}
		}
	}
}

// ensureDefaultAlertChannels provisions one app.alert_channel row per
// env-configured endpoint, so a fresh installation can deliver without an
// operator writing SQL first.
//
// Deliberately narrow: it creates a channel if one with that name does not
// already exist, and otherwise leaves the database alone. It does NOT
// update an existing row from the environment — an operator who has edited
// a channel's config through the admin surface must not have it silently
// reverted on the next worker restart by a stale env var. It also creates
// no ROUTING RULES: who receives what is an operator decision, and a
// default that quietly mails everything somewhere would be worse than
// delivering nothing.
func ensureDefaultAlertChannels(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) error {
	s := store.New(pool)

	type candidate struct {
		name   string
		kind   string
		config any
		enable bool
	}

	candidates := []candidate{
		{
			name: "default-slack", kind: channels.KindSlackWebhook,
			config: map[string]any{"url": cfg.Alerting.SlackWebhook.Reveal()},
			enable: cfg.Alerting.SlackWebhook.Reveal() != "",
		},
		{
			name: "default-discord", kind: channels.KindDiscordWebhook,
			config: map[string]any{"url": cfg.Alerting.DiscordWebhook.Reveal()},
			enable: cfg.Alerting.DiscordWebhook.Reveal() != "",
		},
		{
			name: "default-smtp", kind: channels.KindSMTP,
			config: map[string]any{
				"host": cfg.Alerting.SMTPHost, "port": cfg.Alerting.SMTPPort,
				"from": cfg.Alerting.SMTPFrom, "to": cfg.Alerting.SMTPTo,
				"smtp_username": cfg.Alerting.SMTPUsername,
				"smtp_password": cfg.Alerting.SMTPPassword.Reveal(),
				// HANGAR_SMTP_TLS: "starttls" negotiates the upgrade,
				// "tls" dials SMTPS directly (a different wire protocol,
				// not a stricter STARTTLS), "none" sends in the clear.
				"starttls":     cfg.Alerting.SMTPTLS == "starttls",
				"implicit_tls": cfg.Alerting.SMTPTLS == "tls",
				"require_tls":  cfg.Alerting.SMTPTLS != "none",
			},
			enable: cfg.Alerting.SMTPEnabled,
		},
	}

	for _, c := range candidates {
		if !c.enable {
			continue
		}
		if _, err := s.GetAlertChannelByName(ctx, c.name); err == nil {
			continue // already provisioned; the operator owns it now
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("alerting: looking up channel %q: %w", c.name, err)
		}

		encoded, err := json.Marshal(c.config)
		if err != nil {
			return fmt.Errorf("alerting: encoding config for channel %q: %w", c.name, err)
		}
		if _, err := s.CreateAlertChannel(ctx, c.kind, c.name, encoded); err != nil {
			return fmt.Errorf("alerting: creating channel %q: %w", c.name, err)
		}
		logger.Info("alerting: provisioned default channel from configuration", "name", c.name, "kind", c.kind)
	}
	return nil
}
