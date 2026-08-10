package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hangar-project/hangar/internal/api"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync/planner"
	"github.com/hangar-project/hangar/internal/telemetry"
	webui "github.com/hangar-project/hangar/web"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server, embedded SPA, and in-process worker pool",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context())
		},
	}
}

// runServe boots the HTTP listener. Phase 0 ships only the shell: a liveness
// endpoint, a readiness endpoint that pings the database, the embedded SPA,
// and the replica heartbeat. Huma-based API routes, the River worker pool,
// and the sync planner are added by the phases that define them.
func runServe(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	logger.Info("hangar serve: starting", "version", version, "commit", commit)

	shutdownTracing, err := telemetry.InitTracerProvider(ctx, "hangar", version, "serve")
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	pool, err := pgxpool.New(ctx, cfg.DB.URL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleServe, version, logger)
	hbCtx, cancelHB := context.WithCancel(ctx)
	defer cancelHB()
	go hb.Run(hbCtx)

	// §2 "Single-process default": `serve` runs the sync planner itself so
	// a one-box installation never has to run `schedule` separately.
	// Losing the planner's advisory lock to a co-running `schedule`
	// replica is normal (internal/sync/planner), not an error.
	stopPlanner, err := startPlanner(hbCtx, cfg.DB.URL.Reveal(), pool, planner.Config{
		ClaimInterval:  cfg.Sync.PlannerInterval,
		ClaimBatchSize: cfg.Sync.ClaimBatchSize,
		ClaimLease:     cfg.Sync.ClaimLease,
	}, logger)
	if err != nil {
		return err
	}
	defer stopPlanner()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Phase 15: the /api/v1 surface, mounted on the same mux as the SPA and
	// healthchecks (Principle 6 — one API, no private endpoint). Deps.SSO
	// is nil here — the /auth/login and /auth/callback redirect handlers
	// degrade to 501 rather than panic when it is (see
	// internal/api/v1/auth.go's RegisterAuthRedirects); wiring the full EVE
	// SSO login flow (JWKS cache, keyring, OAuth config) into `serve` is a
	// follow-up to this phase, not required for the /api/v1 JSON surface
	// itself.
	s := store.New(pool)
	deps := api.Deps{Store: s}
	api.Version = version
	hapi := api.NewAPI(mux, deps)
	v1.RegisterAll(hapi, deps)
	v1.RegisterAuthRedirects(mux, s, nil)
	if err := registerMumbleAuthRoute(ctx, mux, s, cfg); err != nil {
		return err
	}

	dist, err := fs.Sub(webui.DistFS, "dist")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServerFS(dist))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(mux, s),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("hangar serve: listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		logger.Info("hangar serve: shutting down")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
