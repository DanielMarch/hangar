package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/events"
)

// ── PHASE 19 CLOSE-OUT DEFECT, SAME CLASS AS B20 ─────────────────────────
//
// internal/events delivered the whole §4.9 pipeline — the transactional
// outbox, the fan-out, the signed HTTP delivery, the retry policy and the
// endpoint breaker — and NOTHING outside an integration test ever called
// events.Dispatcher. No `serve` path, no `work` path, no River job.
//
// The consequence is not "webhooks are slow", it is that they never happen:
// app.outbox_event accumulates rows forever, app.webhook_delivery is never
// written, and an installation that has configured an endpoint receives
// nothing at all while every test passes. The outbox half is the half that
// LOOKS like it is working, because rbac's mutations really do write their
// rows.
//
// This is exactly defect B20 one subsystem over — Phase 2's catalogue.Boot
// had no caller either, for the same reason: the phase that builds a
// component and the phase that runs it are usually the same phase, and when
// the wiring is one line it is the line that gets forgotten. Recorded here
// rather than fixed silently, because the pattern is worth recognising: if
// a package's only callers are in _test.go files, it does not run.

// buildWebhookDispatcher assembles §4.9's dispatcher.
//
// The keyring is a PARAMETER rather than something built here: every
// delivery is signed and an endpoint's HMAC secret is envelope-encrypted at
// rest, so key material is mandatory — but both callers already hold a
// keyring for other reasons, and constructing a second one would report a
// bad HANGAR_MASTER_KEY twice with two different messages.
func buildWebhookDispatcher(pool *pgxpool.Pool, keyring *crypto.Keyring, logger *slog.Logger) *events.Dispatcher {
	return &events.Dispatcher{
		Pool:    pool,
		Keyring: keyring,
		Log:     logger.With("component", "events.dispatcher"),
	}
}

// DefaultWebhookDispatchInterval is how often the pump sweeps.
//
// Matches internal/alerting's default. Deliberately not a new configuration
// knob: §4.9 states no cadence requirement, and an operator who needs to
// tune this needs to tune the alert pump the same way, so one setting for
// both is the change to make — not two half-settings introduced a phase
// apart.
const DefaultWebhookDispatchInterval = 15 * time.Second

// runWebhookDispatcher pumps the outbox until ctx is cancelled.
//
// A tick failure is logged and swallowed for the same reason the alert pump
// swallows its own: this loop runs alongside the River worker pool, and a
// transient database hiccup while claiming must not take the worker process
// down. A genuinely broken database fails the heartbeat, which is the thing
// whose job it is to say so.
func runWebhookDispatcher(ctx context.Context, dispatcher *events.Dispatcher, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = DefaultWebhookDispatchInterval
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
				logger.Error("events: webhook dispatch tick failed", "error", err)
				continue
			}
			if result.EventsFannedOut > 0 || result.Claimed > 0 {
				logger.Info("events: webhook dispatch tick",
					"events_fanned_out", result.EventsFannedOut,
					"deliveries_queued", result.DeliveriesQueued,
					"claimed", result.Claimed, "sent", result.Sent,
					"retried", result.Retried, "dead_lettered", result.DeadLettered,
					"endpoints_disabled", result.EndpointsDisabled)
			}
		}
	}
}
