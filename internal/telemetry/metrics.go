package telemetry

import (
	"context"
	"log/slog"
	"strconv"
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
// PHASE 20.2 adds the four Gate 1 metrics that B29's wiring makes move, and
// not one line earlier:
//
//	esi_error_limit_remaining    a gauge over app.esi_error_budget, below
//	esi_429_total{has_headers}   \
//	esi_429_headerless_total{group}  > counters, in gatewayCounters below,
//	esi_420_total                /   incremented where the response is classified
//
// PHASE 20.3 adds Gate 2's metric, alongside the wiring that gives its
// trigger matrix producers (B27's token-lifecycle events, B32's entitlement
// rule writes, and the RBAC hook in the process that actually mutates RBAC):
//
//	provisioning_revocation_latency_seconds{outcome}  a histogram, below
//
// Deliberately NOT declared here, with the phase that owns each:
//
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
// ── PHASE 20.4: BOTH OPERANDS ARE NOW STORED, AND THAT IS THE FIX ────────
// This struct used to carry a LIVE local count (summed from
// app.esi_ledger_entry at scrape time) beside a STORED server reading, and
// subtracted them. It therefore reported how much had been consumed since
// the last reconcile — throughput, not accuracy. Measured on the live
// installation under per-bucket concurrency: char-social 55, char-detail
// 51, corp-industry 50, char-industry 46, against a tolerance of 1, each
// persisting 3-6 s while the ledger was in perfect health.
//
// The live field is GONE rather than merely unused. Leaving it here would
// leave the wrong subtraction one keystroke away, and this is the third
// time this defect class has been fixed — 20.2 for readings older than a
// window, 20.2 again for reservation contamination, and now for a
// sub-second skew that no freshness rule can resolve. Making the mistake
// unrepresentable is the only remedy that does not depend on the next
// reader having read this comment.
type DivergenceRow struct {
	Group string

	// LocalAtReading and ServerRemaining are the pair the reconciler wrote
	// together, under the bucket's own lock, in one statement
	// (app.esi_ledger_bucket.local_remaining_at_reading and
	// .server_remaining — see RecordServerLedgerReading). They describe one
	// instant, which is what makes their difference a measurement.
	//
	// Either being nil means the server has not been heard from for this
	// bucket. Nil is NOT zero: zero divergence is a healthy reading, no
	// reading is not a reading, and collapsing them would hide a bucket
	// whose headers have stopped arriving behind a wall of reassuring
	// zeroes. A nil row contributes no sample at all.
	LocalAtReading  *int64
	ServerRemaining *int64

	// ObservedAt is when that pair was written, and Window is the bucket's
	// floating window.
	//
	// ── PHASE 20.2: AN EXPIRED READING IS NOT A READING EITHER ───────────
	// The freshness rule survives 20.4 unchanged, and it is not redundant
	// with the paired columns. A pair written at the same instant still
	// describes an instant that can recede into the past: an idle bucket's
	// entries age out of the floating window by design, so what the ledger
	// holds NOW has drifted arbitrarily far from what it held then, and a
	// dashboard reporting the old difference as current would be asserting
	// something nobody has checked.
	//
	// Measured on the development installation immediately after B29's
	// wiring went live: `corp-detail` read 173 against a Gate 1.3 tolerance
	// of 1, from a reading 69 seconds old on a bucket that had simply not
	// been polled since. Nothing was wrong with the ledger — the two numbers
	// described different moments.
	//
	// A reading older than one window is therefore dropped rather than
	// reported, on exactly the same principle as the nil case above. Gate
	// 1's own run is unaffected, because under sustained load every bucket
	// is reconciled on every response. The admin dashboard still shows the
	// stale reading WITH its age, because "nothing has been heard from this
	// bucket in a while" is exactly what an operator needs to see and
	// exactly what this gauge cannot express.
	ObservedAt *time.Time
	Window     time.Duration
}

// DivergenceLister lists the current per-bucket readings.
type DivergenceLister interface {
	LedgerDivergence(ctx context.Context) ([]DivergenceRow, error)
}

// ErrorBudgetReader reports Governor 2's remaining installation-wide error
// budget for esi_error_limit_remaining (Gate 1.4).
//
// `ok` is false when app.esi_error_budget has no row yet — Governor 2's
// Init creates it, and Init runs in the process that builds the ESI
// gateway, so a freshly migrated installation genuinely has no reading.
// That is NOT a remaining budget of zero, which would read as "the
// installation is about to be paused" for a system that has not made a
// single request. No reading, no sample.
//
// The subtraction (max − error_count) is done by the caller, in cmd/hangar,
// because the maximum is configuration (HANGAR_ESI_ERROR_LIMIT_MAX) and
// this package deliberately reads none.
type ErrorBudgetReader interface {
	ErrorBudgetRemaining(ctx context.Context) (remaining int64, ok bool, err error)
}

// GatewayCollector reports the Gate 1 metrics whose subsystems are live, by
// reading current state at SCRAPE time rather than by counting events.
//
// Scrape-time collection is the right shape for these three specifically:
// each is a gauge over authoritative state that already exists (a row
// count, a mode field, a pair of ledger columns), so a counter incremented
// at the call site would be a second, drifting copy of a number the
// database already holds. It is the wrong shape for a rate — which is why
// esi_429_total and friends are counters owned by 20.2, incremented where
// the response is classified, not derived here.
type GatewayCollector struct {
	replicas      LedgerDivergenceSource
	mode          ModeSource
	divergence    DivergenceLister
	errorBudget   ErrorBudgetReader
	liveThreshold time.Duration
	log           *slog.Logger

	replicaCount   *prometheus.Desc
	ledgerMode     *prometheus.Desc
	ledgerDiv      *prometheus.Desc
	errorRemaining *prometheus.Desc
	scrapeErrors   *prometheus.CounterVec
}

// ScrapeErrors exposes hangar_metric_scrape_errors_total so that OTHER
// scrape-time collectors in the same registry can report their own failures
// against the one metric an operator watches, rather than each inventing a
// name.
//
// This collector owns it: it Describes and Collects it, and every other
// user only increments. Two collectors both exporting a metric family of
// the same name is a duplicate-descriptor registration error, not a merge —
// which is why the counter is shared by reference rather than by each
// collector building its own. Phase 20.4's AlertCollector is the first
// other user.
func (c *GatewayCollector) ScrapeErrors() *prometheus.CounterVec { return c.scrapeErrors }

// NewGatewayCollector builds the Phase 20.1 collector. Any of its sources
// may be nil — a process role that does not build the ESI gateway (there
// is currently none, but `migrate` and `openapi` are shaped that way)
// simply reports fewer series rather than panicking on scrape.
func NewGatewayCollector(
	replicas LedgerDivergenceSource,
	mode ModeSource,
	divergence DivergenceLister,
	errorBudget ErrorBudgetReader,
	liveThreshold time.Duration,
	log *slog.Logger,
) *GatewayCollector {
	if log == nil {
		log = slog.Default()
	}
	return &GatewayCollector{
		replicas: replicas, mode: mode, divergence: divergence, errorBudget: errorBudget,
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
		errorRemaining: prometheus.NewDesc(
			"esi_error_limit_remaining",
			"Governor 2's remaining installation-wide error budget in the current fixed window (max minus error_count). Gate 1.4 watches this cross the proactive-pause threshold before any 420 arrives. No sample is emitted until the budget row exists.",
			nil, nil),
		scrapeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hangar_metric_scrape_errors_total",
			Help: "Scrapes that could not read a source. A non-zero value means the gauges below are stale, which is not the same as healthy.",
		}, []string{"source"}),
	}
}

func (c *GatewayCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.replicaCount
	ch <- c.ledgerMode
	ch <- c.ledgerDiv
	ch <- c.errorRemaining
	c.scrapeErrors.Describe(ch)
}

// Collect reads every source under a bounded context. A source that fails
// increments hangar_metric_scrape_errors_total and emits NO sample for
// itself — rather than emitting a zero, which a dashboard cannot tell from
// a genuinely idle installation. This is the same reasoning as
// ServerRemaining being nullable.
func (c *GatewayCollector) Collect(ch chan<- prometheus.Metric) {
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
			now := time.Now()
			for _, row := range rows {
				if row.ServerRemaining == nil || row.LocalAtReading == nil || !row.readingIsCurrent(now) {
					continue
				}
				d := *row.LocalAtReading - *row.ServerRemaining
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

	if c.errorBudget != nil {
		remaining, ok, err := c.errorBudget.ErrorBudgetRemaining(ctx)
		switch {
		case err != nil:
			c.scrapeErrors.WithLabelValues("error_budget").Inc()
			c.log.WarnContext(ctx, "telemetry: scraping error budget", "error", err)
		case ok:
			ch <- prometheus.MustNewConstMetric(c.errorRemaining, prometheus.GaugeValue, float64(remaining))
		}
	}

	c.scrapeErrors.Collect(ch)
}

// ── Gate 1's counters (Phase 20.2, defect B29) ───────────────────────────
//
// Counters, not scrape-time gauges, because each is a RATE over events that
// leave no durable trace: a 429 is answered, snoozed and forgotten, so
// there is no table to read it back from at scrape time the way
// esi_ledger_divergence has app.esi_ledger_bucket. That asymmetry is
// spelled out on GatewayCollector above and is why these live in a separate
// collector rather than being bolted onto it.

// GatewayCounters is the Prometheus collector behind internal/esi.Observer.
// One instance per process, registered once, passed to the ESI client.
type GatewayCounters struct {
	total429      *prometheus.CounterVec
	headerless429 *prometheus.CounterVec
	total420      prometheus.Counter
}

// NewGatewayCounters builds the counter set.
//
// ── LABEL CARDINALITY, THE 20.1 LESSON ───────────────────────────────────
// `group` is app.esi_route.rate_limit_group: a closed set of a few dozen
// names from the ingested spec, so it is bounded by the catalogue and not
// by the installation's size. There is deliberately NO user_key/character
// label anywhere here — Gate 1 is a 5000-character run, and a per-character
// label would put tens of thousands of series into Prometheus during the
// very run these metrics exist to measure. See DivergenceRow's note.
func NewGatewayCounters() *GatewayCounters {
	return &GatewayCounters{
		total429: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "esi_429_total",
			Help: "429 responses received from ESI, split by whether the response carried X-Ratelimit-* headers at all.",
		}, []string{"has_headers"}),
		headerless429: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "esi_429_headerless_total",
			Help: "429 responses that carried no rate-limit headers (CCP's in-monolith limiters), per rate-limit group. 01_ARCHITECTURE.md §5.5 requires these be charged nothing and snoozed, never read as remaining=0.",
		}, []string{"group"}),
		total420: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "esi_420_total",
			Help: "420 'error limited' responses from ESI. Gate 1.2's pass condition is that this stays at zero: Governor 2's proactive pause is supposed to fire first.",
		}),
	}
}

// Observe429 implements internal/esi.Observer.
func (c *GatewayCounters) Observe429(group string, hasHeaders bool) {
	c.total429.WithLabelValues(strconv.FormatBool(hasHeaders)).Inc()
	if !hasHeaders {
		c.headerless429.WithLabelValues(labelOrUnset(group)).Inc()
	}
}

// Observe420 implements internal/esi.Observer.
func (c *GatewayCounters) Observe420() { c.total420.Inc() }

// Describe implements prometheus.Collector.
func (c *GatewayCounters) Describe(ch chan<- *prometheus.Desc) {
	c.total429.Describe(ch)
	c.headerless429.Describe(ch)
	c.total420.Describe(ch)
}

// Collect implements prometheus.Collector.
func (c *GatewayCounters) Collect(ch chan<- prometheus.Metric) {
	c.total429.Collect(ch)
	c.headerless429.Collect(ch)
	c.total420.Collect(ch)
}

// labelOrUnset keeps an empty rate-limit group (a route the spec declares
// no x-rate-limit for) from producing a label value of "", which reads on a
// dashboard as a missing label rather than as a real, nameable category.
func labelOrUnset(group string) string {
	if group == "" {
		return "unset"
	}
	return group
}

// readingIsCurrent reports whether the stored pair is recent enough to
// still describe the bucket — see DivergenceRow.ObservedAt. Since 20.4 the
// two operands describe the same instant by construction, so this no longer
// guards the subtraction itself; it guards the claim that the answer is
// CURRENT. A row with no window recorded is treated as current, because the
// alternative is dropping every sample on a schema this code cannot
// interpret.
func (r DivergenceRow) readingIsCurrent(now time.Time) bool {
	if r.ObservedAt == nil {
		return false
	}
	if r.Window <= 0 {
		return true
	}
	return now.Sub(*r.ObservedAt) <= r.Window
}

// scrapeTimeout bounds every source read in one Collect. A scrape that
// hangs on the database would otherwise hold Prometheus's own connection
// open until its client timeout, and a monitoring system that stalls
// because the thing it monitors is unwell is worse than useless.
const scrapeTimeout = 5 * time.Second
