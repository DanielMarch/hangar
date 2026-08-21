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
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── PHASE 23 (N-9): ONE ASSEMBLY, BOTH ROLES ─────────────────────────────
//
// alertingRole is §4.4's whole pipeline — the two producers and the pump —
// assembled once and started by BOTH `serve` and `work`. It is the shape
// cmd/hangar/workers.go took for B-6, for the same reason and against the
// same defect.
//
// ── THE DEFECT THIS CLOSES ───────────────────────────────────────────────
//
// wireAlertGeneration, runThresholdEvaluator, runAlertDispatcher and
// ensureDefaultAlertChannels were called from cmd/hangar/work.go and
// nowhere else, and docker-compose.yml's only `hangar` service is
// `command: ["serve"]`. So a stock installation synchronised, provisioned,
// served and swept — and produced no alert event and delivered no message.
// §4.4 was entirely absent from it, and `serve` passed nil for Gate 3's two
// metrics with a comment pointing at this item.
//
// 01_ARCHITECTURE.md §2 is as unambiguous here as it was about the workers:
//
//	[DECISION] Single-process default. Gate 5 forbids operational
//	ceremony. `serve` does everything; `work`/`schedule` exist for
//	administrators who have outgrown one box.
//
// ── WHY ONE ASSEMBLY AND NOT FOUR CALLS COPIED INTO serve.go ─────────────
//
// Because four calls copied into serve.go is how this happens a fourth
// time. It is now the THIRD seam wired in one process only to have been a
// defect — B-25's alert producers, B-6's River workers, and this — and in
// each case the fix was cheap and the discovery cost a phase. A single
// assembly means a producer added for one role cannot be missing from the
// other. TestBothProcessRolesStartTheAlertingRole is the structural guard,
// alongside workers_test.go's.
//
// ── WHY IT COULD NOT BE DONE BEFORE N-10 ─────────────────────────────────
//
// Starting a second pump while ClaimPendingAlertDeliveries was a bare
// SELECT would have turned "alerts are never delivered on a default
// installation" into "alerts are delivered TWICE on a scaled-out one",
// which is worse. The claim is a lease as of this phase; see
// db/queries/alert.sql.
//
// ── TWO PUMPS ON ONE OUTBOX IS NOW A SUPPORTED TOPOLOGY ──────────────────
//
// An operator co-running `hangar work` — or two `work` replicas, River's
// normal scale-out — now has two or three dispatchers claiming the same
// app.alert_delivery table. That is exactly what the lease is for, and
// TestTwoDispatchersDoNotDoubleSend is the proof.
type alertingRole struct {
	// Emitter is the shared producer. Exported because `serve` also hands
	// it to the API layer (api.Deps.Alerts) for the one administrative
	// action §4.4 declares a default-enabled domain event for — advancing
	// the ESI compatibility pin. ONE emitter per process: it holds no
	// per-event state, and two would be two things to configure.
	Emitter *alerting.Emitter
	// Deliveries and DeadLetters are Gate 3's two metrics. Both roles run
	// the pump now, so both roles register them — `serve` passed nil for
	// both until this phase, which is why Gate 3's numbers were only ever
	// visible from a process a default installation does not run.
	Deliveries  *telemetry.AlertDeliveries
	DeadLetters telemetry.DeadLetterDepthSource

	dispatcher        *alerting.Dispatcher
	thresholds        *alerting.Evaluator
	dispatchInterval  time.Duration
	thresholdInterval time.Duration
	logger            *slog.Logger
}

// buildAlertingRole assembles the pipeline and performs the two pieces of
// wiring that must happen before anything is started: provisioning the
// env-configured default channels, and installing the CCP-notification
// hook.
//
// It returns an error only for a genuine failure to provision a configured
// channel. An installation with NO channels configured is a valid
// installation and returns a working role that finds nothing to deliver —
// Principle 7's optional-dependency shape.
func buildAlertingRole(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, s *store.Store, logger *slog.Logger) (*alertingRole, error) {
	if err := ensureDefaultAlertChannels(ctx, cfg, pool, logger); err != nil {
		return nil, err
	}

	deliveries := telemetry.NewAlertDeliveries(channels.KnownKinds()...)
	emitter := buildAlertEmitter(cfg, pool)
	wireAlertGeneration(emitter, logger)

	return &alertingRole{
		Emitter:           emitter,
		Deliveries:        deliveries,
		DeadLetters:       deadLetterDepth{s},
		dispatcher:        buildAlertDispatcher(cfg, pool, deliveries, logger),
		thresholds:        buildThresholdEvaluator(cfg, pool, emitter, logger),
		dispatchInterval:  cfg.Alerting.DispatchInterval,
		thresholdInterval: cfg.Alerting.ThresholdInterval,
		logger:            logger,
	}, nil
}

// Start runs the pump and the threshold evaluator until ctx is cancelled.
// Both are plain tickers rather than River jobs, because each pass is a
// short idempotent sweep of a table — giving one a job row per tick would
// put more rows through River than it delivers alerts.
//
// It returns immediately; the two loops run in their own goroutines and
// stop with ctx.
func (r *alertingRole) Start(ctx context.Context) {
	go runAlertDispatcher(ctx, r.dispatcher, r.dispatchInterval, r.logger)
	go runThresholdEvaluator(ctx, r.thresholds, r.thresholdInterval, r.logger)
}

// buildAlertDispatcher assembles Phase 14's outbox pump from configuration
// (SRS §4.4). It never fails: an installation with no channels configured
// is a valid installation, and the pump simply finds nothing to claim.
func buildAlertDispatcher(cfg *config.Config, pool *pgxpool.Pool, deliveries *telemetry.AlertDeliveries, logger *slog.Logger) *alerting.Dispatcher {
	return &alerting.Dispatcher{
		Pool: pool,
		Policy: alerting.RetryPolicy{
			MaxAttempts:     cfg.Alerting.MaxAttempts,
			Base:            cfg.Alerting.RetryBase,
			Cap:             cfg.Alerting.RetryCap,
			DeadLetterAfter: cfg.Alerting.DeadLetterAfter,
			Lease:           cfg.Alerting.Lease,
		},
		ClaimSize: cfg.Alerting.ClaimSize,
		Observer:  deliveries,
		Log:       logger.With("component", "alerting.dispatcher"),
	}
}

// ── PHASE 20.4, DEFECT B25: THE PIPELINE GETS A PRODUCER ─────────────────
//
// Everything below this line is new in 20.4, and the reason it had to exist
// is worth stating once, here, where the wiring is.
//
// alerting.Dispatcher — the outbox PUMP — has been wired since Phase 14
// (runAlertDispatcher, above). It claims, groups, renders, sends, retries
// and dead-letters, over three channel drivers, with an admin-visible
// dead-letter board and a requeue endpoint. It has been running every
// fifteen seconds on every installation and delivering nothing, because
// alerting.Emitter — the only thing that writes app.alert_event — had no
// caller anywhere in cmd/hangar. §4.4's whole delivery half was built,
// tested, documented, and structurally incapable of ever firing.
//
// That is why Gate 3.1's accounting identity
// `generated == delivered + coalesced_into + dead_lettered +
// suppressed_by_dedupe` had no left-hand side, and why a four-hour run
// would have "dropped zero alerts" — truthfully, and meaninglessly.
//
// The two producers are wired separately because they are different
// shapes. A notification IS an event and hangs off the sync write that
// records it; a threshold is a question about state, and only a pass that
// re-asks it can notice the answer changing. See
// internal/alerting/threshold.go.

// buildAlertEmitter constructs the Emitter both producers share. One
// Emitter is safe for concurrent use — it holds no per-event state — so
// the notification hook and the threshold evaluator use the same instance.
func buildAlertEmitter(cfg *config.Config, pool *pgxpool.Pool) *alerting.Emitter {
	return &alerting.Emitter{Pool: pool, Window: cfg.Alerting.CoalesceWindow}
}

// wireAlertGeneration installs the CCP-notification producer:
// internal/sync/handlers.NotificationObservedHook, fired once per
// notification a sync pass actually wrote.
//
// ── WHY THIS IS A PACKAGE-LEVEL HOOK AND NOT AN IMPORT ───────────────────
// internal/sync/handlers must not import internal/alerting (Phases 6-9
// must not depend on Phase 14), and the handler signature is fixed by
// internal/sync/worker's package-level dispatch table, which has no
// constructor to pass a dependency to. See internal/sync/handlers/hooks.go.
//
// ── AND WHY IT IS CALLED FROM ONE PLACE, LIKE wireRevocationTriggers ─────
// A hook nobody sets is silent, and Phase 20.3 found exactly that defect in
// the RBAC seam: `serve` mounted the whole RBAC mutation surface with
// PermissionsChangedHook nil, so every revocation performed through the API
// enqueued nothing, for two phases, with every test green. This function is
// the single wiring point for the alerting seam so the same mistake needs
// somebody to delete a line rather than merely to forget one.
func wireAlertGeneration(emitter *alerting.Emitter, logger *slog.Logger) {
	handlers.NotificationObservedHook = func(ctx context.Context, n handlers.ObservedNotification) error {
		result, err := emitter.IngestNotification(ctx, alerting.Notification{
			Type:           n.Type,
			NotificationID: n.NotificationID,
			Payload:        n.Payload,
			OccurredAt:     n.OccurredAt,
		})
		if err != nil {
			return err
		}
		// An unrecognised CCP type is a NORMAL path (Principle 14) and not
		// an error — it is registered, boarded, and rendered generically —
		// but it is worth a log line the first time, because the
		// unknown-types board is a thing an operator has to know to look
		// at. Gate 3.2 is the condition this serves.
		if result.OnUnknownBoard {
			logger.InfoContext(ctx, "alerting: unrecognised CCP notification type recorded on the unknown-types board",
				"alert_type", result.AlertType, "character_id", n.CharacterID, "notification_id", n.NotificationID)
		}
		return nil
	}
}

// buildThresholdEvaluator assembles §4.4's threshold category. Every margin
// comes from configuration, and a zero selects internal/alerting's own
// default rather than disabling the threshold — see ThresholdPolicy.
func buildThresholdEvaluator(cfg *config.Config, pool *pgxpool.Pool, emitter *alerting.Emitter, logger *slog.Logger) *alerting.Evaluator {
	return &alerting.Evaluator{
		Pool:    pool,
		Emitter: emitter,
		Policy: alerting.ThresholdPolicy{
			StructureFuelWithin:    cfg.Alerting.StructureFuelWithin,
			StarbaseFuelBelow:      cfg.Alerting.StarbaseFuelBelow,
			StarbaseFuelBand:       cfg.Alerting.StarbaseFuelBand,
			MemberInactiveFor:      cfg.Alerting.MemberInactiveFor,
			ContractExpiringWithin: cfg.Alerting.ContractExpiringWithin,
		},
		Log: logger.With("component", "alerting.thresholds"),
	}
}

// runThresholdEvaluator re-asks each threshold's question on a ticker until
// ctx is cancelled. An interval of zero disables it, which is a valid
// configuration and is logged as one rather than silently doing nothing —
// a threshold subsystem that is off and a threshold subsystem that is
// broken must not look the same from the outside.
//
// A pass failure is LOGGED AND SWALLOWED, for the same reason the dispatch
// tick's is: this loop runs alongside the River worker pool, and a
// transient database hiccup evaluating fuel levels must not take the
// worker process down. Evaluate itself already isolates the four
// thresholds from each other.
func runThresholdEvaluator(ctx context.Context, evaluator *alerting.Evaluator, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		logger.Info("alerting: threshold evaluator disabled (HANGAR_ALERT_THRESHOLD_INTERVAL is empty or zero) — " +
			"§4.4's four threshold alerts will never fire on this installation")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := evaluator.Evaluate(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				logger.Error("alerting: threshold evaluation pass failed", "error", err)
			}
			if result.Subjects > 0 {
				logger.Info("alerting: threshold evaluation pass",
					"subjects", result.Subjects, "emitted", result.Emitted,
					"deduplicated", result.Deduplicated, "unrouted_types", result.Unrouted,
					"by_type", result.ByType)
			}
		}
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
