package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5"
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
// process collectors rather than refuse to serve metrics at all. counters
// may likewise be nil — only the process that owns an ESI client has any.
//
// revocations is nil in every process but `work`, and that is deliberate
// rather than incidental. provisioning_revocation_latency_seconds is
// observed where a revocation COMPLETES, which only ever happens in the
// process running the provision-urgent worker pool. Registering an
// always-empty histogram on `serve` would export `_count 0` from a process
// that structurally cannot ever increment it — a reassuring zero from a
// subsystem that is not there, which is precisely the reading 20.1's
// lesson forbids. `serve` produces revocation TRIGGERS and reports none of
// their latencies; `work` consumes them and reports all of them.
// deliveries and deadLetters are likewise nil in every process but `work`,
// and for exactly the same reason: the alert outbox PUMP runs only there.
// alert_delivery_total counts deliveries a pump SETTLED, so exporting it
// from `serve` would publish a permanent zero from a process that settles
// none — the reading 20.1's lesson forbids, and the reading B25 itself
// consisted of. `work` produces and delivers alerts and reports both;
// `serve` produces none and reports neither.
func buildMetricsRegistry(
	s *store.Store,
	gateway *ratelimit.Governor1,
	counters *telemetry.GatewayCounters,
	revocations *telemetry.RevocationLatency,
	deliveries *telemetry.AlertDeliveries,
	deadLetters telemetry.DeadLetterDepthSource,
	errorLimitMax int,
	logger *slog.Logger,
) *prometheus.Registry {
	reg := telemetry.NewRegistry()

	var mode telemetry.ModeSource
	if gateway != nil {
		mode = governorMode{gateway}
	}
	gatewayCollector := telemetry.NewGatewayCollector(
		s, mode, ledgerDivergence{s: s, g: gateway}, errorBudget{s: s, max: errorLimitMax}, telemetry.LiveThreshold, logger,
	)
	reg.MustRegister(gatewayCollector)
	if counters != nil {
		reg.MustRegister(counters)
	}
	if revocations != nil {
		reg.MustRegister(revocations)
	}
	if deliveries != nil {
		reg.MustRegister(deliveries)
	}
	if deadLetters != nil {
		// The scrape-error counter is SHARED, not duplicated: two
		// collectors exporting a metric family of the same name is a
		// duplicate-descriptor error rather than a merge, so the gateway
		// collector owns hangar_metric_scrape_errors_total and everything
		// else increments it. See GatewayCollector.ScrapeErrors.
		reg.MustRegister(telemetry.NewAlertCollector(deadLetters, gatewayCollector.ScrapeErrors(), logger))
	}
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
//
// ── PHASE 20.4.1: THE FAILURE NAMES THE OTHER PROCESS ────────────────────
// HANGAR_METRICS_ADDR defaults to 0.0.0.0:9090 for every role, which is
// right for the topology HANGAR SHIPS — one role per container, each with
// its own network namespace, so all three can hold 9090 and a scraper needs
// one port number. It is wrong for a single box running two roles as
// processes, where the second to start loses the bind.
//
// That is not a symmetric loss, which is why a bare "address in use" was
// not good enough. `work` is the only process that builds an ESI gateway
// and the only one that runs the alert dispatcher, so esi_ledger_mode,
// esi_ledger_divergence's solo source and the whole of Gate 3's metric set
// exist THERE and nowhere else — and whether `work` is the loser is decided
// by a startup race. 20.4 hit this and worked around it by hand.
//
// The default is deliberately NOT changed to a per-role port: that would
// fix the development topology by breaking the shipped one, giving the
// compose stack three ports to scrape instead of one. Naming the cause is
// the fix, and .env.example carries the same note where an operator
// configuring a single box will read it.
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
				"gate evidence and dashboards will have a gap for this process. "+
				"If another HANGAR role (serve/work/schedule) is running on this host it already holds "+
				"this address: HANGAR_METRICS_ADDR defaults to the same port for every role, which is "+
				"correct one-role-per-container and wrong on a single box. Give each role its own port. "+
				"This matters asymmetrically: esi_ledger_mode, esi_ledger_divergence's solo source and "+
				"every alert-delivery metric exist only in `work`",
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

func (m governorMode) Mode() (string, bool) {
	mode, observed := m.g.Mode()
	return string(mode), observed
}

// ledgerDivergence adapts the store's generated ListLedgerDivergence rows
// to telemetry.DivergenceRow, converting the two nullable readings from
// *int32 to *int64 and preserving their nil-ness — nil means "the server
// has not been heard from for this bucket", which must not become a zero.
//
// PHASE 20.4: it carries the STORED pair
// (local_remaining_at_reading, server_remaining) and no longer the live
// LocalRemaining the query still returns for the admin board. That row
// field is deliberately left unread here: the gauge's operands must both
// come from the reconciler's own instant, and reading the live one is the
// exact defect this phase fixed.
//
// ── PHASE 20.4.1: TWO SOURCES, BECAUSE THERE ARE TWO LEDGERS ─────────────
// app.esi_ledger_bucket is written by the CLUSTERED ledger only. At exactly
// one live replica the governor runs the in-process ledger, nothing writes
// that table, and both Gate 1 metrics emitted no samples at all — while
// test/load/gate1_esi.go's maximum over zero samples is 0, which reads as a
// pass. That is the N=1 half of a gate whose §1.4 requires N=1 AND N=3.
//
// So the governor is asked first. In solo mode it answers from memory; in
// clustered mode, or in a process with no gateway at all (`serve`,
// `schedule`), it declines and the store answers. There is deliberately no
// merging of the two: at any instant exactly one ledger is authoritative,
// and a union would report a bucket twice with two different histories.
type ledgerDivergence struct {
	s *store.Store
	g *ratelimit.Governor1 // nil in process roles that build no ESI gateway
}

func (d ledgerDivergence) LedgerDivergence(ctx context.Context) ([]telemetry.DivergenceRow, error) {
	if d.g != nil {
		if readings, ok := d.g.SoloReadings(); ok {
			return soloDivergenceRows(readings), nil
		}
	}
	rows, err := d.s.ListLedgerDivergence(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]telemetry.DivergenceRow, 0, len(rows))
	for _, row := range rows {
		converted := telemetry.DivergenceRow{
			Group: row.RateLimitGroup,
			// Both required for the freshness rule — see
			// telemetry.DivergenceRow.ObservedAt. Omitting them does not
			// produce a wrong number, it produces NO samples at all, which
			// is the safe direction and was still wrong for half an hour of
			// this phase's own verification.
			ObservedAt: row.ServerObservedAt,
			Window:     row.Window,
			MaxTokens:  int64(row.MaxTokens),
		}
		if row.ServerRemaining != nil {
			server := int64(*row.ServerRemaining)
			converted.ServerRemaining = &server
		}
		if row.LocalRemainingAtReading != nil {
			local := int64(*row.LocalRemainingAtReading)
			converted.LocalAtReading = &local
		}
		if row.LocalRemainingAfterReading != nil {
			after := int64(*row.LocalRemainingAfterReading)
			converted.LocalAfter = &after
		}
		out = append(out, converted)
	}
	return out, nil
}

// soloDivergenceRows converts the in-process ledger's snapshot into the
// collector's row shape. Every operand is present by construction — the
// solo ledger records a reading only from inside Reconcile, which has all
// three — so unlike the stored path there is no nil case to preserve here;
// a bucket that has never been reconciled is simply absent from the
// snapshot, which is the same "no reading is not a reading of zero" rule
// expressed by omission rather than by a nil pointer.
func soloDivergenceRows(readings []ratelimit.BucketReading) []telemetry.DivergenceRow {
	out := make([]telemetry.DivergenceRow, 0, len(readings))
	for _, r := range readings {
		server, atReading, after := int64(r.ServerRemaining), int64(r.LocalAtReading), int64(r.LocalAfter)
		observedAt := r.ObservedAt
		out = append(out, telemetry.DivergenceRow{
			Group:           r.Group,
			ServerRemaining: &server,
			LocalAtReading:  &atReading,
			LocalAfter:      &after,
			MaxTokens:       int64(r.MaxTokens),
			ObservedAt:      &observedAt,
			Window:          r.Window,
		})
	}
	return out
}

// deadLetterDepth adapts internal/alerting's dead-letter count to
// telemetry.DeadLetterDepthSource — the adapter exists so internal/telemetry
// does not import internal/alerting, the same inversion governorMode avoids
// for internal/esi/ratelimit.
//
// It reports ok=true with a depth of zero for an empty board, and that is
// correct rather than a violation of the nil-is-not-zero rule: an EMPTY
// dead-letter queue is a real, healthy, fully-known reading — the query
// ran and counted nothing. What must never become zero is a reading that
// could not be TAKEN, which is the error branch, and which produces no
// sample at all.
type deadLetterDepth struct{ s *store.Store }

func (d deadLetterDepth) DeadLetterDepth(ctx context.Context) (int64, bool, error) {
	n, err := alerting.DeadLetterCount(ctx, d.s)
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// errorBudget adapts app.esi_error_budget to telemetry.ErrorBudgetReader,
// doing the max-minus-count subtraction here because the maximum is
// configuration and internal/telemetry reads none.
//
// A missing row reports ok=false rather than a remaining of `max`: Governor
// 2 creates the row on Init, and Init runs in the process that builds the
// ESI gateway, so `serve` on a fresh installation legitimately has nothing
// to report. Reporting the full budget would be a reassuring number about a
// governor that is not running.
type errorBudget struct {
	s   *store.Store
	max int
}

func (e errorBudget) ErrorBudgetRemaining(ctx context.Context) (int64, bool, error) {
	row, err := e.s.GetErrorBudget(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	remaining := int64(e.max) - int64(row.ErrorCount)
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true, nil
}
