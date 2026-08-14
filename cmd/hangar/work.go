package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/planner"
	"github.com/hangar-project/hangar/internal/sync/worker"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
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

	// Phase 20.1 (B36). `work` is the process that actually calls ESI, so
	// it is the one whose esi_ledger_mode reading answers Gate 1.8. It has
	// no other HTTP listener, which is why the metrics endpoint is its own.
	stopMetrics := startMetricsListener(ctx, cfg.MetricsAddr, buildMetricsRegistry(s, governor1, counters, cfg.ESI.ErrorLimitMax, logger), logger)
	defer stopMetrics()

	syncPolicy := sync.PolicyConfig{TTLFloor: cfg.ESI.TTLFloor, BackoffCap: cfg.Sync.BackoffCap}

	workers := river.NewWorkers()
	// River allows exactly one Worker per job Kind — Phase 7's
	// CharacterWorker, Phase 8's CorporationWorker and GlobalWorker are all
	// registered together behind worker.DispatchWorker, which routes each
	// "sync_route" job to the matching entity_kind's worker. Each of the
	// three still has its own directly-callable Work method for tests.
	river.AddWorker(workers, &worker.DispatchWorker{
		Character: &worker.CharacterWorker{
			Pool: pool, Gateway: gateway, Tokens: refresher, Policy: syncPolicy,
		},
		Corporation: &worker.CorporationWorker{
			Pool: pool, Gateway: gateway, Tokens: refresher, Policy: syncPolicy,
			Elector: sync.DBElector{Store: s},
		},
		Global: &worker.GlobalWorker{
			Pool: pool, Gateway: gateway, Policy: syncPolicy,
		},
	})

	// Phase 11: access provisioning's own queues. provision-urgent gets a
	// dedicated 32-worker pool per 01_ARCHITECTURE.md §9.2's budget table
	// so a nightly provision-bulk reconcile can never starve a revocation
	// — the two queues sharing a river.Client is fine (River schedules
	// per-queue independently); it is sharing a WORKER POOL that's
	// prohibited, and QueueConfig below gives each its own.
	drivers := provisioning.NewDrivers()
	if err := registerDiscordDriver(ctx, cfg, pool, drivers); err != nil {
		return err
	}
	if err := registerTeamSpeakDriver(ctx, cfg, pool, drivers); err != nil {
		return err
	}
	if err := registerMumbleDriver(ctx, cfg, pool, drivers, logger); err != nil {
		return err
	}
	river.AddWorker(workers, &provisioning.UrgentWorker{Pool: pool, Drivers: drivers})
	river.AddWorker(workers, &provisioning.BulkWorker{Pool: pool, Drivers: drivers})

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			planner.QueueSync:        {MaxWorkers: 20},
			provisioning.QueueUrgent: {MaxWorkers: 32},
			provisioning.QueueBulk:   {MaxWorkers: 8}, // matches .env.example's HANGAR_WORKER_QUEUES documented provision-bulk:8
		},
		Workers: workers,
	})
	if err != nil {
		return err
	}

	// Wires internal/rbac's PermissionsChangedHook (internal/rbac/hook.go)
	// to Phase 11's revocation path — an RBAC-triggered permission change
	// (direct role grant/revoke, squad_role change, squad membership
	// change, role deletion) now recomputes and, if it reduced any
	// platform's entitlements, enqueues a provision-urgent job in the SAME
	// transaction as the RBAC mutation. This is the wiring point, not
	// internal/rbac itself, so internal/rbac compiles and tests with zero
	// knowledge that internal/provisioning exists (roadmap: "a cleaner
	// seam... avoids a new Phase 11 dependency inside a Phase 10
	// package").
	urgent := &provisioning.Urgent{River: riverClient}
	rbac.PermissionsChangedHook = func(ctx context.Context, s *store.Store, userID uuid.UUID) error {
		return urgent.HandleUserChangeTx(ctx, s, userID, time.Now(), "rbac_change")
	}

	// Token invalidation and owner-hash-change are §9.2's other two named
	// triggers this process can observe directly — internal/sso.Refresher
	// already exposes exactly these two hooks (Phase 5), previously unset.
	// See internal/provisioning/urgent.go's HandleCharacterTokenChange doc
	// comment for why this necessarily opens its own transaction rather
	// than the SSO token write's.
	refresher.OnInvalidGrant = func(ctx context.Context, characterID int64) {
		if err := urgent.HandleCharacterTokenChange(ctx, pool, characterID, "token_invalidated"); err != nil {
			logger.Error("provisioning: urgent revocation after token invalidation failed", "character_id", characterID, "error", err)
		}
	}
	refresher.OnOwnerHashChanged = func(ctx context.Context, characterID int64) {
		if err := urgent.HandleCharacterTokenChange(ctx, pool, characterID, "owner_hash_changed"); err != nil {
			logger.Error("provisioning: urgent revocation after owner hash change failed", "character_id", characterID, "error", err)
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
	dispatcher := buildAlertDispatcher(cfg, pool, logger)

	// Phase 19: §4.9's webhook pump, alongside the alert pump and for the
	// same reasons — a short idempotent sweep of a table, not worth a River
	// job row per tick. Without this the outbox is write-only: rbac's
	// mutations write app.outbox_event faithfully and nothing ever fans
	// them out. See cmd/hangar/webhooks.go.
	webhooks := buildWebhookDispatcher(pool, keyring, logger)

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleWork, version, logger)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runAlertDispatcher(sigCtx, dispatcher, cfg.Alerting.DispatchInterval, logger)
	go runWebhookDispatcher(sigCtx, webhooks, cfg.Alerting.DispatchInterval, logger)
	hb.Run(sigCtx)
	return nil
}
