package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync/planner"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func newScheduleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schedule",
		Short: "Run the leader-elected sync planner",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSchedule(cmd.Context())
		},
	}
}

// runSchedule boots a schedule-role process: the heartbeat plus Phase 6's
// leader-elected sync planner. Losing the planner's advisory lock to
// another replica is normal, not an error — see internal/sync/planner.
func runSchedule(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	logger.Info("hangar schedule: starting", "version", version, "commit", commit)

	shutdownTracing, err := telemetry.InitTracerProvider(ctx, "hangar", version, "schedule")
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	pool, err := pgxpool.New(ctx, cfg.DB.URL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleSchedule, version, logger)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Phase 20.1 (B36). `schedule` builds no ESI gateway, so it reports the
	// replica and divergence gauges but no ledger mode — a nil ModeSource
	// yields fewer series rather than a wrong one.
	stopMetrics := startMetricsListener(sigCtx, cfg.MetricsAddr, buildMetricsRegistry(store.New(pool), nil, logger), logger)
	defer stopMetrics()

	stopPlanner, err := startPlanner(sigCtx, cfg.DB.URL.Reveal(), pool, planner.Config{
		ClaimInterval:  cfg.Sync.PlannerInterval,
		ClaimBatchSize: cfg.Sync.ClaimBatchSize,
		ClaimLease:     cfg.Sync.ClaimLease,
	}, logger)
	if err != nil {
		return err
	}
	defer stopPlanner()

	hb.Run(sigCtx)
	return nil
}
