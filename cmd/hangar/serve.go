package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/hangar-project/hangar/internal/api"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/api/v2shim"
	"github.com/hangar-project/hangar/internal/crypto"
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

	// PHASE 20.11. Verified before the heartbeat starts, because a missing
	// app.esi_replica is exactly the drift this catches and the heartbeat is
	// its first victim. See cmd/hangar/schema_status.go.
	reportSchemaIntegrity(ctx, pool, logger)

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

	// §2 "Single-process default", same reasoning as the planner: the
	// stock docker-compose runs ONE hangar service, `serve`, so anything
	// that only runs under `work` does not run on a default installation
	// at all. §4.9's outbox is written by internal/rbac's mutations
	// whether or not anything drains it, so without this the outbox grows
	// forever and no subscriber is ever called — write-only, and silently
	// so. See cmd/hangar/webhooks.go for how this was missed.
	// One keyring per process: the SSO flow below needs the same key
	// material, and building it twice would mean a bad HANGAR_MASTER_KEY
	// reported twice with two different messages.
	keyring, err := crypto.NewKeyring(cfg.Crypto)
	if err != nil {
		return err
	}
	go runWebhookDispatcher(hbCtx, buildWebhookDispatcher(pool, keyring, logger), cfg.Alerting.DispatchInterval, logger)

	// §2 "Single-process default", same reasoning as the planner above: a
	// one-box installation must not have to run a second command before
	// anything works. Without this, app.esi_route stays empty — and since
	// app.sync_subscription foreign-keys into it, NOTHING in the ESI sync
	// layer can run, not even be configured.
	//
	// Deliberately: in the background (a startup HTTP call to ESI must not
	// delay the listener), non-fatal (an ESI outage must not stop the API
	// serving cached data — catalogue.Boot already falls back to the
	// embedded snapshot before giving up), and idempotent, so every replica
	// doing this on every restart is safe. It never advances the pin.
	//
	// PHASE 20.1 (defect B41): opt-out-able. catalogue.Boot is the only
	// writer of app.setting's `esi.d_max` and rewrites it on every startup,
	// so any environment that has deliberately seeded a catalogue and a
	// D_max ceiling has that seed silently replaced moments after boot —
	// and whether it survives depends on how fast ESI answers, which makes
	// it a race rather than a consistent failure. See ESIConfig's comment.
	if cfg.ESI.StartupCatalogueIngest {
		go func() {
			ingestCtx, cancel := context.WithTimeout(hbCtx, ingestTimeout)
			defer cancel()
			if _, err := ingestCatalogue(ingestCtx, pool, logger); err != nil {
				if hbCtx.Err() == nil {
					logger.ErrorContext(ingestCtx,
						"hangar: esi catalogue ingest failed at startup — the route catalogue may be empty or stale; "+
							"run 'hangar admin ingest-catalogue' once ESI is reachable",
						"error", err)
				}
			}
		}()
	} else {
		logger.Info("hangar: startup catalogue ingest disabled " +
			"(HANGAR_ESI_STARTUP_CATALOGUE_INGEST=false) — app.esi_route and esi.d_max are " +
			"whatever is already in the database; run 'hangar admin ingest-catalogue' to refresh them")
	}

	// Phase 20.1 (B36). On its OWN listener, not on the mux below: the mux
	// is bound to the published port, and /metrics is unauthenticated.
	// `serve` builds no ESI gateway (only `work` does), so it contributes
	// the replica and divergence gauges and no ledger mode.
	stopMetrics := startMetricsListener(hbCtx, cfg.MetricsAddr,
		buildMetricsRegistry(store.New(pool), nil, nil, nil, nil, nil, cfg.ESI.ErrorLimitMax, logger), logger)
	defer stopMetrics()

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
	// healthchecks (Principle 6 — one API, no private endpoint).
	//
	// PHASE 15.1: the EVE SSO login flow is now really assembled
	// (cmd/hangar/sso.go) rather than passed as a nil *sso.Flow — Phase 15
	// left /auth/login and /auth/callback answering 501, which Phase 16's
	// SPA login screen cannot build against.
	s := store.New(pool)

	// PHASE 20.5 (B22). Whether reference data exists is stated once, at
	// boot, rather than left to be inferred from a fitting export full of
	// numeric ids. Deliberately not blocking and not fatal: an installation
	// with no SDE is a supported state, it is just one nobody should have to
	// discover.
	reportSDEState(ctx, pool, s, logger)

	// PHASE 20.3. §9.2's revocation triggers, in the process that performs
	// most of the mutations that fire them. Before this, `serve` mounted
	// the entire RBAC mutation surface with rbac.PermissionsChangedHook
	// still nil — see cmd/hangar/revocation.go for the full account of what
	// that meant for Gate 2's trigger matrix.
	//
	// A River client this process never Start()s: insert-only, no worker
	// pool, no producers. cmd/hangar/work.go remains the only process that
	// consumes provision-urgent.
	insertOnlyRiver, err := newInsertOnlyRiverClient(pool)
	if err != nil {
		return err
	}
	urgent := wireRevocationTriggers(insertOnlyRiver, pool)
	lifecycle := buildTokenLifecycle(s, urgent, pool, logger)

	flow, err := buildSSOFlow(ctx, cfg, pool, s, keyring, lifecycle, urgent, logger)
	if err != nil {
		return err
	}

	// Phase 20.1.1 (defect B42): create the subscriptions that give the
	// planner something to claim. Runs at boot and every reconcileInterval,
	// on every replica — see runSubscriptionReconciler for why it is not
	// behind the leader lock. Without this the planner claims due work,
	// finds none, and does so forever.
	go runSubscriptionReconciler(hbCtx, s, logger)

	// Phase 20.4 (B25): `serve` is the process that performs the one
	// administrative action §4.4 declares a default-enabled DOMAIN EVENT
	// for — advancing the ESI compatibility pin. It produces alert events
	// into the shared outbox; `work` still owns the pump that delivers
	// them, and therefore owns Gate 3's delivery metrics.
	deps := api.Deps{Store: s, Pool: pool, SSO: flow, Urgent: urgent, Alerts: buildAlertEmitter(cfg, pool), Keyring: keyring}
	api.Version = version
	hapi := api.NewAPI(mux, deps)
	v1.RegisterAll(hapi, deps)
	v1.RegisterAuthRedirects(mux, s, flow)

	// Phase 19: the read-only /api/v2 sunset shim (SRS §10). Mounted on the
	// same mux, behind the same api.Handler middleware chain, so it shares
	// one credential path and one authorisation check with /api/v1 rather
	// than growing a second one.
	v2shim.Register(mux, v2shim.Deps{Store: s})
	if err := registerMumbleAuthRoute(ctx, mux, s, cfg); err != nil {
		return err
	}

	dist, err := fs.Sub(webui.DistFS, "dist")
	if err != nil {
		return err
	}
	mux.Handle("/", spaHandler(dist))

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

// spaHandler serves the embedded SPA build with a client-side-routing
// fallback: TanStack Router (Phase 16/17) does its own path matching in
// the browser via the History API, so any GET that doesn't correspond to a
// real file under dist/ — /login, /characters/123/wallet, a hard refresh
// or a bookmark on any nested route — must still be answered with
// index.html so the client-side router gets a chance to render it, rather
// than the bare 404 http.FileServerFS gives a path it doesn't recognise.
//
// PHASE 17 DEFECT CLOSURE: Phase 16 registered only a plain
// http.FileServerFS on "/", which is correct for "/" and for real asset
// paths (/assets/*.js) but 404s every other SPA route on direct
// navigation. That was invisible in Phase 16's own testing (its one new
// route, /login, was only ever reached by client-side navigation from
// "/", never a hard reload or a bookmark) and became a real break the
// moment Phase 17 added deep-linkable, bookmark-and-refresh-worthy routes
// (/characters/{id}/wallet and friends).
//
// A request whose path has a file extension and doesn't exist still gets
// a real 404 (a missing /assets/*.js is a build/deploy bug worth seeing
// as one, not a misleading 200 of HTML); everything else falls back to
// index.html — EXCEPT under reservedAPIPrefixes, which must 404 like any
// other unknown API/health/auth route rather than silently answering 200
// with an HTML body. Go's net/http.ServeMux only prefers a more specific
// pattern than "/" when one is actually REGISTERED for the request path;
// an unregistered sub-path of /api/v1/... (a typo, a route removed
// without updating a client, a probe) has no such match and falls through
// to "/" same as any other unknown path — spaHandler must not turn that
// into a false-positive 200.
var reservedAPIPrefixes = []string{"api/", "auth/", "healthz", "readyz"}

func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if reqPath == "" || reqPath == "." {
			fileServer.ServeHTTP(w, r)
			return
		}
		for _, prefix := range reservedAPIPrefixes {
			if reqPath == prefix || strings.HasPrefix(reqPath, prefix) {
				http.NotFound(w, r)
				return
			}
		}
		if _, err := fs.Stat(dist, reqPath); err != nil {
			if path.Ext(reqPath) != "" {
				http.NotFound(w, r)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
