package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// NewRegistry returns a Prometheus registry seeded with the standard process
// and Go runtime collectors.
//
// ── WHY THIS FUNCTION HAD NO CALLER UNTIL PHASE 20.1 (defect B36) ─────────
// Phase 0 established the registry and left "domain-specific collectors ...
// registered by the phases that introduce them". No phase introduced any,
// nothing ever called NewRegistry, and no `/metrics` endpoint was served —
// so every metric named in 04_RELEASE_GATES.md's instrumentation table
// existed only inside doc comments. The gate document said the metrics were
// owed by Phases 4/11/14 and forbade adding them in Phase 20; SRS §0 said
// Phase 20 owned them outright. Both could not hold; see the audit in
// docs/PRODUCTION_CALLER_AUDIT.md and the resolution recorded as rules 6
// and 7 of 04_RELEASE_GATES.md §0.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	return reg
}

// ── WHAT THIS FILE DELIBERATELY DOES NOT DECLARE ─────────────────────────
//
// Release-gate rule 7: *a metric is declared only by the change that makes
// it move.* A metric that exists and reads zero is indistinguishable from a
// healthy system — `alert_delivery_total == 0` reads as "a quiet
// installation", not as "the emitter has no caller" — so declaring the full
// gate metric set here would hide precisely the defects Phases 20.2–20.4
// exist to close.
//
// Declared here, because their subsystems are live and these values move
// today:
//
//	esi_replica_live_count       the heartbeat loop runs in every process role
//	esi_ledger_mode              Governor1.Mode() is live via buildGateway
//	esi_ledger_divergence        computed from app.esi_ledger_bucket/_entry
//
// Deliberately NOT declared here, with the phase that owns each:
//
//	esi_429_total, esi_420_total, esi_error_limit_remaining,
//	esi_429_headerless_total                            → 20.2, with B29
//	provisioning_revocation_latency_seconds             → 20.3, with B26/B27
//	alert_delivery_total, alert_dead_letter_depth       → 20.4, with B25
//
// Adding one of those here before its wiring lands would be the same defect
// in a new costume.

// LedgerDivergenceSource is the read side of Governor 1's ledger, as the
// metric collector needs it. internal/store satisfies it; it is an
// interface rather than a *store.Store so internal/telemetry does not
// import internal/store (which would be an import cycle through the
// store's own telemetry use) and so the collector is testable without a
// database.
type LedgerDivergenceSource interface {
	CountLiveReplicas(ctx context.Context, liveThreshold time.Duration) (int64, error)
}

// ModeSource reports Governor 1's currently active ledger mode. Satisfied
// by *ratelimit.Governor1; an interface so telemetry does not depend on
// internal/esi/ratelimit.
type ModeSource interface {
	Mode() string
}

// DivergenceRow is one Governor 1 bucket's local-versus-server reading.
// Mirrors the shape store.ListLedgerDivergence returns, narrowed to what
// the metric needs.
//
// ── WHY THERE IS NO user_key LABEL ───────────────────────────────────────
// A Governor 1 bucket is keyed by (rate_limit_group, user_key), and
// user_key is "applicationID:characterID" (§5.5). Labelling the metric by
// it would emit one time series per character per group — and Gate 1 is a
// **5000-character** run, so the metric intended to prove Gate 1 passes
// would itself put tens of thousands of series into Prometheus during the
// very run it is measuring. Gate 1.3's bar is stated "per group", so the
// collector aggregates to the per-group MAXIMUM, which is both what the
// gate asks for and bounded by the number of rate-limit groups.
type DivergenceRow struct {
	Group          string
	LocalRemaining int64
	// ServerRemaining is nil until the server has been heard from for this
	// bucket. Nil is NOT zero: zero divergence is a healthy reading, no
	// reading is not a reading, and collapsing them would hide a bucket
	// whose headers have stopped arriving behind a wall of reassuring
	// zeroes. A nil row contributes no sample at all.
	ServerRemaining *int64
}

// DivergenceLister lists the current per-bucket readings.
type DivergenceLister interface {
	LedgerDivergence(ctx context.Context) ([]DivergenceRow, error)
}

// gatewayCollector reports the Gate 1 metrics whose subsystems are live, by
// reading current state at SCRAPE time rather than by counting events.
//
// Scrape-time collection is the right shape for these three specifically:
// each is a gauge over authoritative state that already exists (a row
// count, a mode field, a pair of ledger columns), so a counter incremented
// at the call site would be a second, drifting copy of a number the
// database already holds. It is the wrong shape for a rate — which is why
// esi_429_total and friends are counters owned by 20.2, incremented where
// the response is classified, not derived here.
type gatewayCollector struct {
	replicas      LedgerDivergenceSource
	mode          ModeSource
	divergence    DivergenceLister
	liveThreshold time.Duration
	log           *slog.Logger

	replicaCount *prometheus.Desc
	ledgerMode   *prometheus.Desc
	ledgerDiv    *prometheus.Desc
	scrapeErrors *prometheus.CounterVec
}

// NewGatewayCollector builds the Phase 20.1 collector. Any of its sources
// may be nil — a process role that does not build the ESI gateway (there
// is currently none, but `migrate` and `openapi` are shaped that way)
// simply reports fewer series rather than panicking on scrape.
func NewGatewayCollector(
	replicas LedgerDivergenceSource,
	mode ModeSource,
	divergence DivergenceLister,
	liveThreshold time.Duration,
	log *slog.Logger,
) prometheus.Collector {
	if log == nil {
		log = slog.Default()
	}
	return &gatewayCollector{
		replicas: replicas, mode: mode, divergence: divergence,
		liveThreshold: liveThreshold, log: log,
		replicaCount: prometheus.NewDesc(
			"esi_replica_live_count",
			"Replicas heartbeating within the liveness threshold. Exactly one selects solo ledger mode; two or more select clustered.",
			nil, nil),
		ledgerMode: prometheus.NewDesc(
			"esi_ledger_mode",
			"Governor 1 ledger mode as an enum gauge: 1 for the active mode, 0 for the others.",
			[]string{"mode"}, nil),
		ledgerDiv: prometheus.NewDesc(
			"esi_ledger_divergence",
			"Maximum absolute difference, across the buckets of a rate-limit group, between the local ledger's remaining count and the server's X-Ratelimit-Remaining. Gate 1.3 requires max <= 1 per group. Groups the server has not reported on emit no sample.",
			[]string{"group"}, nil),
		scrapeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hangar_metric_scrape_errors_total",
			Help: "Scrapes that could not read a source. A non-zero value means the gauges below are stale, which is not the same as healthy.",
		}, []string{"source"}),
	}
}

func (c *gatewayCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.replicaCount
	ch <- c.ledgerMode
	ch <- c.ledgerDiv
	c.scrapeErrors.Describe(ch)
}

// Collect reads every source under a bounded context. A source that fails
// increments hangar_metric_scrape_errors_total and emits NO sample for
// itself — rather than emitting a zero, which a dashboard cannot tell from
// a genuinely idle installation. This is the same reasoning as
// ServerRemaining being nullable.
func (c *gatewayCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	if c.replicas != nil {
		// The query takes a NEGATIVE interval added to now(), matching
		// db/queries/esi_replica.sql's live_threshold argument.
		n, err := c.replicas.CountLiveReplicas(ctx, -c.liveThreshold)
		if err != nil {
			c.scrapeErrors.WithLabelValues("replicas").Inc()
			c.log.WarnContext(ctx, "telemetry: scraping live replica count", "error", err)
		} else {
			ch <- prometheus.MustNewConstMetric(c.replicaCount, prometheus.GaugeValue, float64(n))
		}
	}

	if c.mode != nil {
		// Emitted as an enum gauge (one series per possible mode, exactly
		// one of them 1) rather than as a string label on a constant, so
		// Gate 1.8's "mode was clustered for the whole N=3 run and solo for
		// the whole N=1 run, with no unexpected flapping" is answerable
		// with a range query instead of by eyeballing label churn.
		active := c.mode.Mode()
		for _, m := range []string{"solo", "clustered"} {
			value := 0.0
			if m == active {
				value = 1.0
			}
			ch <- prometheus.MustNewConstMetric(c.ledgerMode, prometheus.GaugeValue, value, m)
		}
	}

	if c.divergence != nil {
		rows, err := c.divergence.LedgerDivergence(ctx)
		if err != nil {
			c.scrapeErrors.WithLabelValues("divergence").Inc()
			c.log.WarnContext(ctx, "telemetry: scraping ledger divergence", "error", err)
		} else {
			// Per-group maximum, not per-bucket: see DivergenceRow's note on
			// why a user_key label would be a cardinality bomb during the
			// very run it exists to measure.
			maxByGroup := make(map[string]int64, len(rows))
			for _, row := range rows {
				if row.ServerRemaining == nil {
					continue
				}
				d := row.LocalRemaining - *row.ServerRemaining
				if d < 0 {
					d = -d
				}
				if current, seen := maxByGroup[row.Group]; !seen || d > current {
					maxByGroup[row.Group] = d
				}
			}
			for group, d := range maxByGroup {
				ch <- prometheus.MustNewConstMetric(c.ledgerDiv, prometheus.GaugeValue, float64(d), group)
			}
		}
	}

	c.scrapeErrors.Collect(ch)
}

// scrapeTimeout bounds every source read in one Collect. A scrape that
// hangs on the database would otherwise hold Prometheus's own connection
// open until its client timeout, and a monitoring system that stalls
// because the thing it monitors is unwell is worse than useless.
const scrapeTimeout = 5 * time.Second
