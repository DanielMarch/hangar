package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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

// runSchedule boots a schedule-role process. The sync planner itself (leader
// election, subscription scanning, route-catalogue-driven enqueue) is
// Phase 4+ domain logic; Phase 0 establishes the command and its heartbeat.
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

	logger.Info("hangar schedule: sync planner not implemented yet; heartbeating only")

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	hb.Run(sigCtx)
	return nil
}
