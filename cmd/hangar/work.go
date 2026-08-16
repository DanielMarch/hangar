package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func newWorkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "work",
		Short: "Run the River background job worker pool",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWork(cmd.Context())
		},
	}
}

// runWork boots a worker-role process: the heartbeat plus Phase 6/7's
// River worker pool. Phase 6 defined and enqueues the "sync_route" kind
// (internal/sync/planner.KindSyncRoute); Phase 7 is the first phase to
// register a Worker for it — internal/sync/worker.CharacterWorker, which
// dispatches character-scoped subscriptions to internal/sync/handlers.
// Corp/alliance dispatch (entity_kind != "character") is Phase 8/9's; a
// job for one of those kinds fails loudly (CharacterWorker.Work returns an
// error for it) rather than being silently dropped.
func runWork(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	logger.Info("hangar work: starting", "version", version, "commit", commit)

	shutdownTracing, err := telemetry.InitTracerProvider(ctx, "hangar", version, "work")
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	pool, err := pgxpool.New(ctx, cfg.DB.URL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	s := store.New(pool)

	keyring, err := crypto.NewKeyring(cfg.Crypto)
	if err != nil {
		return err
	}
	gateway, governor1, counters, err := buildGateway(cfg, pool, s, logger)
	if err != nil {
		return err
	}
	refresher := buildRefresher(cfg, pool, keyring)

	// Phase 20.3: Gate 2's metric is observed where the revocation
	// completes, so the histogram belongs to the process that runs the
	// urgent worker and to no other. Built before the registry so it can be
	// registered on it.
	revocationLatency := telemetry.NewRevocationLatency(provisioning.KnownOutcomes()...)

	// Phase 20.4: Gate 3's metrics, and for the same reason — the outbox
	// PUMP runs here and nowhere else, so this is the only process in which
	// alert_delivery_total can move or alert_dead_letter_depth means
	// anything. Registering them on `serve` would export a settled-delivery
	// counter from a process that settles none.
	alertDeliveries := telemetry.NewAlertDeliveries(channels.KnownKinds()...)

	// Phase 20.1 (B36). `work` is the process that actually calls ESI, so
	// it is the one whose esi_ledger_mode reading answers Gate 1.8. It has
	// no other HTTP listener, which is why the metrics endpoint is its own.
	stopMetrics := startMetricsListener(ctx, cfg.MetricsAddr,
		buildMetricsRegistry(s, governor1, counters, revocationLatency, alertDeliveries, deadLetterDepth{s},
			cfg.ESI.ErrorLimitMax, logger), logger)
	defer stopMetrics()

	// PHASE 22 (B-6): the pool is assembled in cmd/hangar/workers.go, which
	// `serve` also calls. It used to be built here and only here, which is
	// exactly why a default installation — one `serve`, no `work` — enqueued
	// sync and provisioning jobs that nothing ever consumed. That file has
	// the full account.
	riverClient, err := buildWorkerPool(ctx, cfg, pool, s, gateway, refresher, revocationLatency, logger)
	if err != nil {
		return err
	}

	// §9.2's revocation triggers this process can observe. Shared with
	// `serve` since Phase 20.3 — see cmd/hangar/revocation.go for what was
	// wrong with wiring them here only.
	urgent := wireRevocationTriggers(riverClient, pool)

	// Token invalidation and owner-hash-change are §9.2's other two named
	// triggers — internal/sso.Refresher already exposes exactly these two
	// hooks (Phase 5), previously unset. See
	// internal/provisioning/urgent.go's HandleCharacterChange doc
	// comment for why this necessarily opens its own transaction rather
	// than the SSO token write's.
	//
	// PHASE 20.3: both now go through internal/sso.Lifecycle rather than
	// straight to Urgent. That gives §7.2/§7.3's invalidation and its
	// revocation notification ONE definition instead of two — see
	// internal/sso/lifecycle.go's header for the split between it and
	// refresh.go's in-transaction invalidation, and why the store call on
	// this path is a deliberate idempotent re-assertion rather than an
	// oversight.
	lifecycle := buildTokenLifecycle(s, urgent, pool, logger)
	refresher.OnInvalidGrant = func(ctx context.Context, characterID int64) {
		if err := lifecycle.InvalidateForInvalidGrant(ctx, characterID); err != nil {
			logger.Error("sso: invalidating tokens after invalid_grant failed", "character_id", characterID, "error", err)
		}
	}
	refresher.OnOwnerHashChanged = func(ctx context.Context, characterID int64) {
		if err := lifecycle.InvalidateForOwnerHashChange(ctx, characterID); err != nil {
			logger.Error("sso: invalidating tokens after owner hash change failed", "character_id", characterID, "error", err)
		}
	}

	if err := riverClient.Start(ctx); err != nil {
		return err
	}
	defer func() { _ = riverClient.Stop(context.Background()) }()

	// Phase 14: the alert outbox pump. It runs as a plain ticker alongside
	// the River pool rather than as a River job, because a delivery pass is
	// a short, idempotent sweep of a table — giving it a job row per tick
	// would put more rows through River than it delivers alerts. Channels
	// come from app.alert_channel; an installation with none configured
	// runs this loop finding nothing, which is the default and is not an
	// error (Principle 7's optional-dependency shape).
	if err := ensureDefaultAlertChannels(ctx, cfg, pool, logger); err != nil {
		return err
	}
	dispatcher := buildAlertDispatcher(cfg, pool, alertDeliveries, logger)

	// Phase 20.4 (B25): the pump finally has producers. The notification
	// hook fires from the sync handlers this process runs; the threshold
	// evaluator is its own ticker. See cmd/hangar/alerting.go.
	emitter := buildAlertEmitter(cfg, pool)
	wireAlertGeneration(emitter, logger)
	thresholds := buildThresholdEvaluator(cfg, pool, emitter, logger)

	// Phase 19: §4.9's webhook pump, alongside the alert pump and for the
	// same reasons — a short idempotent sweep of a table, not worth a River
	// job row per tick. Without this the outbox is write-only: rbac's
	// mutations write app.outbox_event faithfully and nothing ever fans
	// them out. See cmd/hangar/webhooks.go.
	webhooks := buildWebhookDispatcher(pool, keyring, logger)

	reportSchemaIntegrity(ctx, pool, logger)

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleWork, version, logger)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runAlertDispatcher(sigCtx, dispatcher, cfg.Alerting.DispatchInterval, logger)
	go runThresholdEvaluator(sigCtx, thresholds, cfg.Alerting.ThresholdInterval, logger)
	go runWebhookDispatcher(sigCtx, webhooks, cfg.Alerting.DispatchInterval, logger)
	hb.Run(sigCtx)
	return nil
}
