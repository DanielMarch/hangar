package telemetry

import "github.com/prometheus/client_golang/prometheus"

// ── GATE 2's METRIC (Phase 20.3) ─────────────────────────────────────────
//
// 04_RELEASE_GATES.md §2.2 defines the measurement exactly: p99 of
// `platform_call_completed_at − event_at` from `app.provisioning_audit`,
// where event_at is the ORIGINATING entitlement-reducing event and not job
// start or job claim, "because queue wait is what fails under load".
//
// ── WHY A HISTOGRAM AND NOT A SCRAPE-TIME GAUGE ─────────────────────────
// esi_ledger_divergence is a gauge because it describes a CURRENT state
// that app.esi_ledger_bucket already holds, so recomputing it at scrape
// time cannot drift from the truth. A revocation's latency is not a state;
// it is the duration of an event that has finished and will never recur.
// Reading "the latency of the most recent revocation" at scrape time would
// sample whatever happened to be last, which is not a p99 of anything. So
// this is observed once, where the event completes — the same reasoning
// that made esi_429_total a counter rather than a gauge.
//
// A histogram with no observations exports `_count 0`. Unlike 20.1's
// lesson about gauges, that is NOT a false reading: it says "zero
// revocations have completed in this process", which is exactly true and
// exactly distinguishable from "the subsystem is broken" (that shows up as
// triggers firing with no completions, and on the exposure board). The
// nil-is-not-zero rule applies to readings of a continuous quantity, not
// to counts of events.
//
// ── WHY THE OPERANDS ARE THE ONES THE GATE NAMES ────────────────────────
// 20.2's lesson: a subtraction whose operands describe different moments is
// not a measurement. Both operands here come off the SAME
// app.provisioning_audit row and describe the same revocation, and the
// subtraction is performed by the UPDATE that writes the second of them
// (db/queries/provisioning.sql's CompleteProvisioningAudit) rather than
// being reassembled in Go from a stored timestamp and a local clock.

// RevocationLatency is the Prometheus collector behind Gate 2's SLO.
// One instance per process, registered once, handed to the provisioning
// workers.
type RevocationLatency struct {
	seconds *prometheus.HistogramVec
}

// NewRevocationLatency builds the histogram.
//
// ── LABEL CARDINALITY, THE 20.1 LESSON ──────────────────────────────────
// `outcome` is internal/provisioning's own closed set — success,
// partial_failure, failed, skipped_unlinked — four values, fixed in Go
// source. There is deliberately NO user, character or platform label:
// Gate 2 is a 5000-identity run across 3 platforms, and a per-identity
// label would put 15,000 histograms into Prometheus during the very run
// the metric exists to measure. Platform was considered and rejected too —
// platform_id is a uuid, so the series name would be unreadable, and §2.1's
// pass condition is stated over the installation, not per platform.
//
// ── WHY FAILURES ARE LABELLED RATHER THAN DROPPED ───────────────────────
// A `failed` revocation did not remove anything: the group is still live on
// the remote platform and the exposure continues. Recording its
// call duration as a revocation latency would UNDERSTATE exposure — a
// two-second failure would look like a fast revocation. Dropping the sample
// instead would hide the failure from the metric entirely. Labelling keeps
// both readable: Gate 2's p99 query filters to outcome="success", and a
// dashboard can still see that failures are happening and how long they
// take to fail.
// ── WHY knownOutcomes IS A PARAMETER, AND WHY IT MATTERS ────────────────
// A HistogramVec with no observations exports NO SERIES AT ALL — not even a
// zero. Found by scraping a live installation during Phase 20.3: `/metrics`
// on a healthy, idle `work` process said nothing whatsoever about
// revocations, which is indistinguishable from a process where the
// revocation path is not wired. That is 20.1's "a missing reading is never
// a zero" lesson running the OTHER way round: for a count of events, zero
// is a true and useful reading and its ABSENCE is the misleading one.
//
// Pre-initialising every label value fixes it. The values come from the
// caller rather than being listed here because they are
// internal/provisioning's vocabulary (its exported KnownOutcomes), and
// internal/telemetry is a leaf that nearly everything imports — it must not
// grow a dependency on the provisioning stack to name four strings.
func NewRevocationLatency(knownOutcomes ...string) *RevocationLatency {
	r := &RevocationLatency{
		seconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "provisioning_revocation_latency_seconds",
			Help: "Seconds from the originating entitlement-reducing event (app.provisioning_audit.event_at) to the platform call completing. 04_RELEASE_GATES.md §2.2's measurement, including queue wait. Gate 2 requires p99 < 60s over outcome=\"success\".",
			// Bucketed around the 60s SLO rather than on Prometheus's
			// default (which tops out at 10s and would put every
			// interesting value in +Inf): the boundaries either side of 60
			// are what a p99-under-60 assertion actually reads.
			Buckets: []float64{0.5, 1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 300, 600},
		}, []string{"outcome"}),
	}
	for _, outcome := range knownOutcomes {
		r.seconds.WithLabelValues(outcome)
	}
	return r
}

// ObserveRevocation records one completed revocation. seconds comes from
// CompleteProvisioningAudit's own RETURNING clause — never from a local
// clock (see this file's header).
//
// A negative or absent latency is DROPPED rather than clamped to zero. It
// can only mean the two timestamps disagree about which came first, which
// is a clock-skew or double-completion signal; recording it as an
// instantaneous revocation would be the most flattering possible lie about
// the number this metric exists to police.
func (r *RevocationLatency) ObserveRevocation(outcome string, seconds float64) {
	if seconds < 0 {
		return
	}
	r.seconds.WithLabelValues(labelOrUnset(outcome)).Observe(seconds)
}

// Describe implements prometheus.Collector.
func (r *RevocationLatency) Describe(ch chan<- *prometheus.Desc) { r.seconds.Describe(ch) }

// Collect implements prometheus.Collector.
func (r *RevocationLatency) Collect(ch chan<- prometheus.Metric) { r.seconds.Collect(ch) }
