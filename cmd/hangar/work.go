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

func newWorkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "work",
		Short: "Run the River background job worker pool",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWork(cmd.Context())
		},
	}
}

// runWork boots a worker-role process. No River workers are registered yet —
// job kinds land with the phases that define them (sync jobs from Phase 4,
// alert delivery from Phase 14, ...). Phase 0's job is the heartbeat and the
// shape of the command, not the job registry.
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

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleWork, version, logger)

	logger.Info("hangar work: no job kinds registered yet; heartbeating only until a later phase adds a River client here")

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	hb.Run(sigCtx)
	return nil
}
