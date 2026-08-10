package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hangar-project/hangar/internal/crypto"
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
	gateway, err := buildGateway(cfg, pool, s, logger)
	if err != nil {
		return err
	}
	refresher := buildRefresher(cfg, pool, keyring)

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

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{planner.QueueSync: {MaxWorkers: 20}},
		Workers: workers,
	})
	if err != nil {
		return err
	}
	if err := riverClient.Start(ctx); err != nil {
		return err
	}
	defer func() { _ = riverClient.Stop(context.Background()) }()

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleWork, version, logger)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	hb.Run(sigCtx)
	return nil
}
