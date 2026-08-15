package telemetry

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// alerting.go holds Gate 3's two metrics, declared in Phase 20.4 — the
// phase that gave §4.4's delivery pipeline a producer — and not before,
// per 04_RELEASE_GATES.md's rule 7: a metric is declared by the change that
// makes it move.
//
// Declaring them earlier would have been worse than useless. Until 20.4
// nothing wrote app.alert_event, so alert_delivery_total would have read a
// flat zero and alert_dead_letter_depth a flat zero, on every installation,
// forever — a pair of reassuring numbers from a subsystem that could not
// fire. That is B25's whole shape, and 04_RELEASE_GATES.md §3.1 warns about
// its consequence directly: "zero alerts dropped" is true of an empty run.
//
// ── THE FOUR LESSONS, APPLIED HERE ───────────────────────────────────────
//
//  1. NO LABEL WHOSE CARDINALITY SCALES WITH CHARACTERS OR ALERTS. There is
//     deliberately no alert_type label. It looks harmless — the catalogue
//     is 54 types — but Principle 14 means the vocabulary is OPEN: an
//     unrecognised CCP notification type is registered at runtime and
//     delivered, so the label's real domain is "every string CCP has ever
//     put in a `type` field", which is 254 values in the live spec today
//     and unbounded tomorrow. Gate 3 is a four-hour run across all eight
//     domains; the metric measuring it must not grow a series per type
//     CCP invents. Channel KIND (three values, fixed by a CHECK
//     constraint) and outcome (three, fixed here) are what remain.
//
//  2. A MISSING GAUGE READING IS NEVER A ZERO — 20.1's lesson.
//     alert_dead_letter_depth is a gauge over a database count, and a
//     scrape that cannot read the table reports no sample and increments
//     hangar_metric_scrape_errors_total, rather than reporting a
//     dead-letter depth of zero for a queue nobody managed to look at.
//
//  3. A SUBTRACTION WHOSE OPERANDS DESCRIBE DIFFERENT MOMENTS IS NOT A
//     MEASUREMENT — 20.2's and 20.4's own lesson, from esi_ledger_divergence.
//     Neither metric here is a difference. The depth gauge is a single
//     COUNT(*) evaluated in the database, not `enqueued − delivered`
//     reassembled in Go from two counters that were read at different
//     instants; the two would drift apart under exactly the concurrency
//     that makes the number interesting.
//
//  4. A COUNTERVEC WITH NO OBSERVATIONS EXPORTS NO SERIES AT ALL — 20.3's
//     lesson, found by curling a live installation. An unwired subsystem
//     and a quiet one are then indistinguishable. Every (kind, outcome)
//     pair is therefore pre-initialised at zero by NewAlertDeliveries, from
//     the caller's own vocabulary, exactly as NewRevocationLatency now
//     takes provisioning.KnownOutcomes().

// Alert delivery outcomes. Three, matching what alerting.Dispatcher can
// actually do to a delivery it claimed — and matching Gate 3.1's own
// accounting terms, so the metric and the gate use one vocabulary.
//
// There is no "coalesced_into" outcome, and its absence is deliberate
// rather than an omission. A coalesced sibling IS delivered — inside the
// roll-up — and alerting.Dispatcher marks every delivery in a sent group
// 'sent' for exactly that reason. Gate 3.1 counts coalescing by comparing
// events to messages, which is a database question; inventing a fourth
// outcome here would make the same delivery countable twice.
const (
	AlertDelivered    = "sent"
	AlertRetried      = "retried"
	AlertDeadLettered = "dead_lettered"
)

// AlertOutcomes is the closed outcome set, for pre-initialisation and for
// tests that assert the series exist.
func AlertOutcomes() []string {
	return []string{AlertDelivered, AlertRetried, AlertDeadLettered}
}

// AlertDeliveries is alert_delivery_total: one counter per (channel kind,
// outcome), incremented by the dispatcher where a delivery is settled.
type AlertDeliveries struct {
	total *prometheus.CounterVec
}

// NewAlertDeliveries builds the counter and pre-initialises every
// (kind, outcome) pair at zero.
//
// knownKinds comes from the caller — internal/alerting/channels.KnownKinds()
// — for the same reason NewRevocationLatency's outcomes do: internal/
// telemetry is a leaf that nearly everything imports, and it must not grow
// a dependency on the alerting stack to name three strings.
//
// The pre-initialisation includes channels.KindUnknown's stand-in, because
// a delivery can fail BEFORE its channel row is readable (the row was
// deleted between enqueue and claim), and a settled delivery with no
// knowable kind must still be counted somewhere rather than dropped.
func NewAlertDeliveries(knownKinds ...string) *AlertDeliveries {
	a := &AlertDeliveries{
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alert_delivery_total",
			Help: "Alert deliveries settled by the outbox pump, by channel kind and outcome. 04_RELEASE_GATES.md §3.1: an alert is DROPPED only if it was generated and neither delivered nor dead-lettered — dead-lettering is a visible outcome, not a loss, which is why it is a label value here rather than a separate metric.",
		}, []string{"kind", "outcome"}),
	}
	kinds := append([]string{"unset"}, knownKinds...)
	for _, kind := range kinds {
		for _, outcome := range AlertOutcomes() {
			a.total.WithLabelValues(kind, outcome)
		}
	}
	return a
}

// ObserveAlertDelivery records one settled delivery.
func (a *AlertDeliveries) ObserveAlertDelivery(kind, outcome string) {
	a.total.WithLabelValues(labelOrUnset(kind), labelOrUnset(outcome)).Inc()
}

// Describe implements prometheus.Collector.
func (a *AlertDeliveries) Describe(ch chan<- *prometheus.Desc) { a.total.Describe(ch) }

// Collect implements prometheus.Collector.
func (a *AlertDeliveries) Collect(ch chan<- prometheus.Metric) { a.total.Collect(ch) }

// DeadLetterDepthSource reports how many alert deliveries are currently on
// the dead-letter board. Satisfied by a closure over
// internal/alerting.DeadLetterCount; an interface so this package does not
// import the alerting stack.
//
// ok is false when the count could not be read. That is NOT a depth of
// zero — 20.1's lesson — and produces no sample.
type DeadLetterDepthSource interface {
	DeadLetterDepth(ctx context.Context) (depth int64, ok bool, err error)
}

// AlertCollector reports alert_dead_letter_depth at scrape time.
//
// A gauge over authoritative state that already exists, read where it
// lives, rather than a counter HANGAR increments and decrements in Go: a
// requeue from the admin board (POST .../dead-letter/{id}/requeue) takes a
// delivery back OFF the board, and a Go-side counter would have to be told
// about that by every path that could ever move a row. The database always
// knows. This is the same reasoning that makes esi_ledger_divergence and
// esi_ledger_mode scrape-time reads and esi_429_total a counter.
type AlertCollector struct {
	deadLetters  DeadLetterDepthSource
	depth        *prometheus.Desc
	scrapeErrors *prometheus.CounterVec
	log          *slog.Logger
}

// NewAlertCollector builds the collector. A nil source disables the gauge
// entirely — which is the right behaviour for a process that does not run
// the pump, and is not the same as reporting zero from one that does.
func NewAlertCollector(deadLetters DeadLetterDepthSource, scrapeErrors *prometheus.CounterVec, log *slog.Logger) *AlertCollector {
	return &AlertCollector{
		deadLetters: deadLetters,
		depth: prometheus.NewDesc(
			"alert_dead_letter_depth",
			"Alert deliveries currently on the dead-letter board: generated, attempted, permanently not delivered, and visible to an administrator who can requeue them. 04_RELEASE_GATES.md §3.1 counts these as a delivered outcome rather than a drop. No sample is emitted when the count cannot be read — an unreadable board is not an empty one.",
			nil, nil,
		),
		scrapeErrors: scrapeErrors,
		log:          log,
	}
}

// Describe implements prometheus.Collector.
func (c *AlertCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.depth }

// Collect implements prometheus.Collector.
func (c *AlertCollector) Collect(ch chan<- prometheus.Metric) {
	if c.deadLetters == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	depth, ok, err := c.deadLetters.DeadLetterDepth(ctx)
	switch {
	case err != nil:
		if c.scrapeErrors != nil {
			c.scrapeErrors.WithLabelValues("alert_dead_letter").Inc()
		}
		if c.log != nil {
			c.log.WarnContext(ctx, "telemetry: scraping the alert dead-letter depth", "error", err)
		}
	case !ok:
		// Nothing to say. Deliberately silent rather than zero.
	default:
		ch <- prometheus.MustNewConstMetric(c.depth, prometheus.GaugeValue, float64(depth))
	}
}
