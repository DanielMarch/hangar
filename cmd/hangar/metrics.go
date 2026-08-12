package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ── PHASE 20.1, DEFECT B36 ───────────────────────────────────────────────
// telemetry.NewRegistry had no caller and no `/metrics` endpoint was ever
// served, so every metric named in 04_RELEASE_GATES.md's instrumentation
// table existed only in doc comments and Gates 1-3 had no measurement
// surface at all. This file is the caller.
//
// It deliberately registers ONLY the collector for subsystems that are
// live today (release-gate rule 7 — a metric is declared by the change
// that makes it move). Phases 20.2-20.4 register theirs alongside the
// wiring that makes them non-zero.

// buildMetricsRegistry assembles the process-wide Prometheus registry.
//
// gateway may be nil: `migrate` and `openapi` never construct an ESI
// gateway, and a process without one should still export its Go and
// process collectors rather than refuse to serve metrics at all.
func buildMetricsRegistry(s *store.Store, gateway *ratelimit.Governor1, logger *slog.Logger) *prometheus.Registry {
	reg := telemetry.NewRegistry()

	var mode telemetry.ModeSource
	if gateway != nil {
		mode = governorMode{gateway}
	}
	reg.MustRegister(telemetry.NewGatewayCollector(
		s, mode, ledgerDivergence{s}, telemetry.LiveThreshold, logger,
	))
	return reg
}

// metricsHandler serves the registry in the Prometheus text exposition
// format.
//
// It is mounted UNAUTHENTICATED, deliberately and narrowly: the endpoint
// exposes operational counters and no user, character or token data, and
// every scraper in the deployment topologies SRS §9 documents (the compose
// stack, the Helm chart's ServiceMonitor) reaches it over the internal
// network. Making it require a HANGAR session would mean either giving
// Prometheus a user account or inventing a second credential path — and
// Phase 18's defect B21 is the record of what a second credential path
// costs. Operators terminating the port publicly should not; the Helm
// chart's default Service does not expose it.
func metricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		// A scrape that cannot read one source still reports every source
		// it CAN read, with hangar_metric_scrape_errors_total incremented.
		// Failing the whole scrape would turn one unhealthy subsystem into
		// total blindness, which is the opposite of what monitoring is for.
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      reg,
	})
}

// startMetricsListener serves /metrics on cfg.MetricsAddr until ctx is
// cancelled, and returns a shutdown func. An empty address disables it.
//
// A bind failure is LOGGED, NOT FATAL. The reasoning is the same as the
// alert dispatcher's swallowed tick errors: an installation whose metrics
// port is already taken should still serve its API, because losing
// observability is bad and losing the application is worse. The log line
// says so explicitly rather than leaving a silent gap — a monitoring
// endpoint that quietly is not there is exactly the failure mode B36 was.
func startMetricsListener(ctx context.Context, addr string, reg *prometheus.Registry, logger *slog.Logger) func() {
	if addr == "" {
		logger.Info("hangar: metrics listener disabled (HANGAR_METRICS_ADDR is empty)")
		return func() {}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metricsHandler(reg))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		logger.Info("hangar: metrics listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("hangar: metrics listener stopped — /metrics is NOT being served; "+
				"gate evidence and dashboards will have a gap for this process",
				"addr", addr, "error", err)
		}
	}()

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
}

// governorMode adapts *ratelimit.Governor1 to telemetry.ModeSource. The
// adapter exists so internal/telemetry does not import
// internal/esi/ratelimit — telemetry is imported by nearly everything, and
// a dependency from it into the ESI stack would be an inversion.
type governorMode struct{ g *ratelimit.Governor1 }

func (m governorMode) Mode() string { return string(m.g.Mode()) }

// ledgerDivergence adapts the store's generated ListLedgerDivergence rows
// to telemetry.DivergenceRow, converting the nullable server reading from
// *int32 to *int64 and preserving its nil-ness — nil means "the server has
// not been heard from for this bucket", which must not become a zero.
type ledgerDivergence struct{ s *store.Store }

func (d ledgerDivergence) LedgerDivergence(ctx context.Context) ([]telemetry.DivergenceRow, error) {
	rows, err := d.s.ListLedgerDivergence(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]telemetry.DivergenceRow, 0, len(rows))
	for _, row := range rows {
		converted := telemetry.DivergenceRow{
			Group:          row.RateLimitGroup,
			LocalRemaining: row.LocalRemaining,
		}
		if row.ServerRemaining != nil {
			server := int64(*row.ServerRemaining)
			converted.ServerRemaining = &server
		}
		out = append(out, converted)
	}
	return out, nil
}
