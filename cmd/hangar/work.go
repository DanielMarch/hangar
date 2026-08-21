package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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

	// Phase 20.4: Gate 3's metrics.
	//
	// PHASE 23 (N-9): they used to be built HERE, with a comment saying the
	// pump "runs here and nowhere else, so this is the only process in
	// which alert_delivery_total can move". That was true and was the
	// defect — the process a default installation actually runs is `serve`,
	// so Gate 3's numbers were only ever visible from a process nobody
	// runs. They are built by buildAlertingRole now, which both roles call,
	// and both roles register what it returns.
	alerts, err := buildAlertingRole(ctx, cfg, pool, s, logger)
	if err != nil {
		return err
	}

	// Phase 20.1 (B36). `work` is the process that actually calls ESI, so
	// it is the one whose esi_ledger_mode reading answers Gate 1.8. It has
	// no other HTTP listener, which is why the metrics endpoint is its own.
	stopMetrics := startMetricsListener(ctx, cfg.MetricsAddr,
		buildMetricsRegistry(s, governor1, counters, revocationLatency, alerts.Deliveries, alerts.DeadLetters,
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
	// PHASE 23 (N-9): §4.4's producers and pump, from the one assembly
	// `serve` also starts. See cmd/hangar/alerting.go.
	alerts.Start(sigCtx)
	go runWebhookDispatcher(sigCtx, webhooks, cfg.Alerting.DispatchInterval, logger)
	hb.Run(sigCtx)
	return nil
}
