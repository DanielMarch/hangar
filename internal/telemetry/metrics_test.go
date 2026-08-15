package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type stubReplicas struct {
	n   int64
	err error
	// gotThreshold records what the collector passed, so the sign of the
	// interval is asserted rather than assumed.
	gotThreshold time.Duration
}

func (s *stubReplicas) CountLiveReplicas(_ context.Context, threshold time.Duration) (int64, error) {
	s.gotThreshold = threshold
	return s.n, s.err
}

type stubMode struct{ mode string }

func (s stubMode) Mode() string { return s.mode }

type stubDivergence struct {
	rows []DivergenceRow
	err  error
}

func (s stubDivergence) LedgerDivergence(context.Context) ([]DivergenceRow, error) {
	return s.rows, s.err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func int64p(v int64) *int64 { return &v }

// freshReading stamps a divergence row as observed just now, within its
// window. Phase 20.2 made a stale reading emit no sample (see
// DivergenceRow.ObservedAt), so every row asserting on a VALUE has to say
// when it was observed — a test that forgot would silently assert on an
// empty gauge.
func freshReading(rows []DivergenceRow) []DivergenceRow {
	now := time.Now()
	for i := range rows {
		rows[i].ObservedAt = &now
		rows[i].Window = 15 * time.Minute
	}
	return rows
}

// TestLedgerDivergenceIsPerGroupMaximum pins the aggregation that keeps
// Gate 1's own metric from becoming a cardinality bomb during Gate 1's own
// run: buckets are keyed (group, user_key) and a 5000-character run has
// 5000 user keys, so the collector must emit one series per GROUP carrying
// that group's maximum — which is also exactly what Gate 1.3 measures.
func TestLedgerDivergenceIsPerGroupMaximum(t *testing.T) {
	collector := NewGatewayCollector(nil, nil, stubDivergence{rows: freshReading([]DivergenceRow{
		{Group: "market", LocalAtReading: int64p(100), ServerRemaining: int64p(99)},  // 1
		{Group: "market", LocalAtReading: int64p(100), ServerRemaining: int64p(93)},  // 7  <- max
		{Group: "market", LocalAtReading: int64p(100), ServerRemaining: int64p(100)}, // 0
		{Group: "corp", LocalAtReading: int64p(50), ServerRemaining: int64p(52)},     // 2 (absolute)
	})}, nil, LiveThreshold, quietLogger())

	expected := `
# HELP esi_ledger_divergence Maximum absolute difference, across the buckets of a rate-limit group, between the local ledger's remaining count and the server's X-Ratelimit-Remaining. Gate 1.3 requires max <= 1 per group. Groups the server has not reported on emit no sample.
# TYPE esi_ledger_divergence gauge
esi_ledger_divergence{group="corp"} 2
esi_ledger_divergence{group="market"} 7
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "esi_ledger_divergence"); err != nil {
		t.Error(err)
	}
}

// TestBucketWithNoServerReadingEmitsNoSample is the nullability rule the
// admin endpoint already follows, enforced here too: no reading is not a
// reading. Emitting zero for a bucket the server has never reported on
// would hide a group whose rate-limit headers have stopped arriving behind
// a reassuring zero — which is the healthiest-looking value on the gauge.
func TestBucketWithNoServerReadingEmitsNoSample(t *testing.T) {
	collector := NewGatewayCollector(nil, nil, stubDivergence{rows: []DivergenceRow{
		{Group: "silent", LocalAtReading: int64p(100), ServerRemaining: nil},
	}}, nil, LiveThreshold, quietLogger())

	if n := testutil.CollectAndCount(collector, "esi_ledger_divergence"); n != 0 {
		t.Errorf("a bucket with no server reading produced %d samples, want 0 — a null reading must not become a zero", n)
	}
}

// TestBucketWithNoLocalReadingEmitsNoSample is the OTHER half of the same
// rule, and it is new in Phase 20.4 because the local half only became a
// stored, nullable value in this phase (migration 00042).
//
// It has two real sources, not just a theoretical one: a bucket carried
// across the migration keeps its server_remaining and has no paired local
// reading until the next reconcile, and `solo` mode reconciles in process
// and writes neither column. In both cases the honest answer is "no
// reading", and it must not be reported as a divergence equal to the whole
// server reading — which is what subtracting a zeroed nil would produce,
// and which would look exactly like a catastrophically broken ledger.
func TestBucketWithNoLocalReadingEmitsNoSample(t *testing.T) {
	now := time.Now()
	collector := NewGatewayCollector(nil, nil, stubDivergence{rows: []DivergenceRow{
		{Group: "premigration", ServerRemaining: int64p(97), LocalAtReading: nil, ObservedAt: &now, Window: time.Minute},
	}}, nil, LiveThreshold, quietLogger())

	if n := testutil.CollectAndCount(collector, "esi_ledger_divergence"); n != 0 {
		t.Errorf("a bucket with a server reading but no paired local reading produced %d samples, want 0 — "+
			"half a pair is not a measurement", n)
	}
}

// TestModeIsAnEnumGauge pins the shape Gate 1.8 needs. The condition is
// "esi_ledger_mode was clustered for the whole N=3 run and solo for the
// whole N=1 run; no unexpected flapping", which is a range query over a
// numeric series — not something answerable by watching a string label
// appear and disappear.
func TestModeIsAnEnumGauge(t *testing.T) {
	collector := NewGatewayCollector(nil, stubMode{mode: "clustered"}, nil, nil, LiveThreshold, quietLogger())

	expected := `
# HELP esi_ledger_mode Governor 1 ledger mode as an enum gauge: 1 for the active mode, 0 for the others.
# TYPE esi_ledger_mode gauge
esi_ledger_mode{mode="clustered"} 1
esi_ledger_mode{mode="solo"} 0
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "esi_ledger_mode"); err != nil {
		t.Error(err)
	}
}

// TestReplicaThresholdIsNegative guards the one easily-inverted detail:
// db/queries/esi_replica.sql adds live_threshold to now(), so it must
// arrive NEGATIVE. A positive interval would count every replica that has
// ever heartbeated as live, permanently selecting clustered mode.
func TestReplicaThresholdIsNegative(t *testing.T) {
	replicas := &stubReplicas{n: 3}
	collector := NewGatewayCollector(replicas, nil, nil, nil, LiveThreshold, quietLogger())

	if n := testutil.CollectAndCount(collector, "esi_replica_live_count"); n != 1 {
		t.Fatalf("expected exactly one replica-count sample, got %d", n)
	}
	if replicas.gotThreshold != -LiveThreshold {
		t.Errorf("CountLiveReplicas got threshold %v, want %v — the query adds it to now(), so it must be negative",
			replicas.gotThreshold, -LiveThreshold)
	}
}

// TestFailingSourceEmitsNoSampleAndCountsTheError is the same principle as
// the null reading, one level up. A source that cannot be read must not
// contribute a zero that a dashboard will read as healthy; it contributes
// nothing, and says so on a separate counter.
func TestFailingSourceEmitsNoSampleAndCountsTheError(t *testing.T) {
	collector := NewGatewayCollector(
		&stubReplicas{err: errors.New("database unreachable")},
		nil,
		stubDivergence{err: errors.New("database unreachable")},
		nil,
		LiveThreshold, quietLogger(),
	)

	if n := testutil.CollectAndCount(collector, "esi_replica_live_count"); n != 0 {
		t.Errorf("a failing source produced %d samples, want 0", n)
	}
	if n := testutil.CollectAndCount(collector, "esi_ledger_divergence"); n != 0 {
		t.Errorf("a failing divergence source produced %d samples, want 0", n)
	}

	// Both sources failed on each of the two CollectAndCount scrapes above,
	// plus once more here — the counter is cumulative across scrapes, which
	// is what makes a persistently unreadable source visible as a rate.
	expected := `
# HELP hangar_metric_scrape_errors_total Scrapes that could not read a source. A non-zero value means the gauges below are stale, which is not the same as healthy.
# TYPE hangar_metric_scrape_errors_total counter
hangar_metric_scrape_errors_total{source="divergence"} 3
hangar_metric_scrape_errors_total{source="replicas"} 3
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "hangar_metric_scrape_errors_total"); err != nil {
		t.Error(err)
	}
}

// TestNilSourcesDoNotPanic covers the process roles that build fewer
// subsystems — `serve` and `schedule` construct no ESI gateway, so their
// ModeSource is nil and they report fewer series rather than crashing on
// scrape.
func TestNilSourcesDoNotPanic(t *testing.T) {
	collector := NewGatewayCollector(nil, nil, nil, nil, LiveThreshold, quietLogger())
	reg := prometheus.NewRegistry()
	reg.MustRegister(collector)
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("gathering with every source nil failed: %v", err)
	}
}

// TestStaleServerReadingEmitsNoSample — PHASE 20.2, found by running the
// system against real ESI rather than by reading the code.
//
// LocalRemaining is computed live; ServerRemaining is a snapshot from the
// last response that carried the header. On an idle bucket the local
// consumption ages out of the floating window by design and climbs back
// towards max_tokens while the stored reading stands still, so subtracting
// them reports most of the bucket as divergence. `corp-detail` read 173
// against Gate 1.3's tolerance of 1, from a reading 69 seconds old, on an
// installation with nothing wrong with it.
//
// A subtraction whose operands describe different moments is not a
// measurement — the same rule as the nil case, one step further.
func TestStaleServerReadingEmitsNoSample(t *testing.T) {
	stale := time.Now().Add(-20 * time.Minute)
	collector := NewGatewayCollector(nil, nil, stubDivergence{rows: []DivergenceRow{
		{Group: "idle", LocalAtReading: int64p(300), ServerRemaining: int64p(127), ObservedAt: &stale, Window: 15 * time.Minute},
	}}, nil, LiveThreshold, quietLogger())

	if n := testutil.CollectAndCount(collector, "esi_ledger_divergence"); n != 0 {
		t.Errorf("a reading older than the bucket window produced %d samples, want 0 — "+
			"local and server would be describing different moments", n)
	}
}

// TestReadingInsideTheWindowStillCounts is the other half: the freshness
// rule must not quietly switch the gauge off. A reading from within the
// window is exactly what Gate 1.3 is built on, and under load every bucket
// is reconciled on every response.
func TestReadingInsideTheWindowStillCounts(t *testing.T) {
	recent := time.Now().Add(-30 * time.Second)
	collector := NewGatewayCollector(nil, nil, stubDivergence{rows: []DivergenceRow{
		{Group: "busy", LocalAtReading: int64p(300), ServerRemaining: int64p(299), ObservedAt: &recent, Window: 15 * time.Minute},
	}}, nil, LiveThreshold, quietLogger())

	expected := `
# HELP esi_ledger_divergence Maximum absolute difference, across the buckets of a rate-limit group, between the local ledger's remaining count and the server's X-Ratelimit-Remaining. Gate 1.3 requires max <= 1 per group. Groups the server has not reported on emit no sample.
# TYPE esi_ledger_divergence gauge
esi_ledger_divergence{group="busy"} 1
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "esi_ledger_divergence"); err != nil {
		t.Error(err)
	}
}

// ── PHASE 20.4: GATE 3'S METRICS ─────────────────────────────────────────

// TestAlertDeliveryTotalExportsEveryLabelPairAtZero is 20.3's lesson,
// applied one phase later to a CounterVec instead of a HistogramVec: a Vec
// with no observations exports NO SERIES AT ALL, so a `/metrics` scrape of
// an installation whose alerting is wired but quiet is byte-for-byte
// identical to one where it is not wired.
//
// That is not a hypothetical. It is exactly what B25 looked like from
// outside for two years, and it is why every (kind, outcome) pair is
// pre-initialised.
func TestAlertDeliveryTotalExportsEveryLabelPairAtZero(t *testing.T) {
	deliveries := NewAlertDeliveries("smtp", "slack_webhook", "discord_webhook")

	// 4 kinds (three real plus "unset") x 3 outcomes.
	if n := testutil.CollectAndCount(deliveries, "alert_delivery_total"); n != 12 {
		t.Errorf("alert_delivery_total exported %d series before any delivery, want 12 — "+
			"a quiet alerting subsystem must be distinguishable from an unwired one", n)
	}
}

// TestAlertDeliveryTotalCountsWhatTheDispatcherSettles pins the label
// vocabulary itself: the pump's outcome constants and this metric's label
// values must be one list, not two that can drift.
func TestAlertDeliveryTotalCountsWhatTheDispatcherSettles(t *testing.T) {
	deliveries := NewAlertDeliveries("slack_webhook")
	deliveries.ObserveAlertDelivery("slack_webhook", AlertDelivered)
	deliveries.ObserveAlertDelivery("slack_webhook", AlertDelivered)
	deliveries.ObserveAlertDelivery("slack_webhook", AlertDeadLettered)
	// A delivery settled before its channel row could be read still has a
	// fate and must still be counted.
	deliveries.ObserveAlertDelivery("", AlertRetried)

	expected := `
# HELP alert_delivery_total Alert deliveries settled by the outbox pump, by channel kind and outcome. 04_RELEASE_GATES.md §3.1: an alert is DROPPED only if it was generated and neither delivered nor dead-lettered — dead-lettering is a visible outcome, not a loss, which is why it is a label value here rather than a separate metric.
# TYPE alert_delivery_total counter
alert_delivery_total{kind="slack_webhook",outcome="dead_lettered"} 1
alert_delivery_total{kind="slack_webhook",outcome="retried"} 0
alert_delivery_total{kind="slack_webhook",outcome="sent"} 2
alert_delivery_total{kind="unset",outcome="dead_lettered"} 0
alert_delivery_total{kind="unset",outcome="retried"} 1
alert_delivery_total{kind="unset",outcome="sent"} 0
`
	if err := testutil.CollectAndCompare(deliveries, strings.NewReader(expected), "alert_delivery_total"); err != nil {
		t.Error(err)
	}
}

// stubDeadLetters is a DeadLetterDepthSource double.
type stubDeadLetters struct {
	depth int64
	ok    bool
	err   error
}

func (s stubDeadLetters) DeadLetterDepth(context.Context) (int64, bool, error) {
	return s.depth, s.ok, s.err
}

// TestDeadLetterDepthReportsAnEmptyBoardAsZero draws the line the
// nil-is-not-zero rule actually draws. An EMPTY dead-letter board is a
// real, fully-known reading: the query ran and counted nothing, and an
// operator watching this gauge wants to see the zero.
func TestDeadLetterDepthReportsAnEmptyBoardAsZero(t *testing.T) {
	collector := NewAlertCollector(stubDeadLetters{depth: 0, ok: true}, nil, quietLogger())

	expected := `
# HELP alert_dead_letter_depth Alert deliveries currently on the dead-letter board: generated, attempted, permanently not delivered, and visible to an administrator who can requeue them. 04_RELEASE_GATES.md §3.1 counts these as a delivered outcome rather than a drop. No sample is emitted when the count cannot be read — an unreadable board is not an empty one.
# TYPE alert_dead_letter_depth gauge
alert_dead_letter_depth 0
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "alert_dead_letter_depth"); err != nil {
		t.Error(err)
	}
}

// TestDeadLetterDepthEmitsNoSampleWhenItCannotBeRead is the other side: a
// board nobody could read is not a board with nothing on it. Reporting
// zero here would tell an operator their alerting is healthy at the exact
// moment the database stopped answering.
func TestDeadLetterDepthEmitsNoSampleWhenItCannotBeRead(t *testing.T) {
	collector := NewAlertCollector(stubDeadLetters{err: errors.New("connection refused")}, nil, quietLogger())

	if n := testutil.CollectAndCount(collector, "alert_dead_letter_depth"); n != 0 {
		t.Errorf("an unreadable dead-letter board produced %d samples, want 0 — "+
			"a missing reading is never a zero", n)
	}
}

// TestAlertCollectorWithNoSourceIsSilent covers the process-role split:
// `serve` runs no outbox pump, so it reports nothing about dead letters
// rather than a permanent zero from a subsystem it does not run.
func TestAlertCollectorWithNoSourceIsSilent(t *testing.T) {
	collector := NewAlertCollector(nil, nil, quietLogger())
	if n := testutil.CollectAndCount(collector, "alert_dead_letter_depth"); n != 0 {
		t.Errorf("a process with no dead-letter source produced %d samples, want 0", n)
	}
}
