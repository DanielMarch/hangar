package load

// gate3_alerts.go is Gate 3's driver — 04_RELEASE_GATES.md §3.
//
// Like gate1_esi.go and gate2_revocation.go it does NOT decide when to run
// or for how long. §3's real run is four hours across all eight domains and
// all three categories, and it belongs to Phase 20.8; nothing in this file
// starts one. What lives here is the measurement, the accounting, and the
// failure-injecting channel stub, so that when the real run happens a
// failure means the system is wrong rather than the harness.
//
// ── WHERE THE NUMBERS COME FROM, AND WHY MOSTLY NOT FROM THE APPLICATION ─
// §3.1 defines a drop as "generated and neither delivered nor
// dead-lettered". Every OUTCOME term below is counted from
// app.alert_event and app.alert_delivery with SQL, for the same reason
// gate2_revocation.go reads app.provisioning_audit rather than
// provisioning_revocation_latency_seconds: a gate that takes its verdict
// from the system's own instrumentation is asking the system whether it
// thinks it passed. alert_delivery_total exists and will agree; the two
// agreeing is a useful cross-check, and is reported as one, but the
// verdict comes from the tables.
//
// The INPUT term is the exception, necessarily. `generated` is how many
// alert occurrences were offered to the pipeline, and an occurrence that
// deduplicated leaves NO DATABASE TRACE AT ALL — that is what
// RecordAlertEvent's `ON CONFLICT (dedupe_hash) DO NOTHING` means. It
// cannot be recovered from the tables afterwards by any query. So the
// harness counts what it fed in, which is not the system reporting on
// itself: it is the test's own tally of its own input, and the whole point
// of the identity below is to check that tally against what the database
// holds.
//
// ── THE IDENTITY, MADE EXACT ─────────────────────────────────────────────
// §3.1 states `generated == delivered + coalesced_into + dead_lettered +
// suppressed_by_dedupe`, over an unstated unit. Stated precisely there are
// two units and therefore two identities, and conflating them is how a
// gate like this quietly becomes unfalsifiable:
//
//	OCCURRENCES (what was offered):
//	    offered == events_written + suppressed_by_dedupe
//
//	DELIVERIES (what can actually be dropped — one (event, channel) row):
//	    enqueued == messages_sent + coalesced_into + dead_lettered + pending
//
// A delivery is the unit §3.1's definition is really about: one occurrence
// routed to three channels is three things that can independently succeed,
// dead-letter or be lost. `messages_sent` counts MESSAGES — distinct
// (channel, coalescing key) groups that went out — and `coalesced_into` is
// the remainder of those groups, which were delivered inside somebody
// else's message. That is what makes §3.1's third term mean anything;
// counting each sent row as its own delivery would make coalesced_into
// permanently zero and §3.4 unmeasurable from the same data.
//
// `pending` is the drop term. §3.1: an alert is dropped if it was
// generated and neither delivered nor dead-lettered. At end of run that
// number must be zero, and it is a CONDITION rather than a residual — no
// term here is computed by subtracting the others from the total, because
// a residual can only ever balance.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/jackc/pgx/v5"
)

// Gate3Config parameterises one Gate 3 measurement window.
type Gate3Config struct {
	// Since bounds the measurement to alert events that occurred at or
	// after this instant — the start of the run. Without it a gate would
	// silently include every alert the installation has ever raised,
	// including ones from a previous, failed attempt.
	Since time.Time

	// MinAlerts is the smallest sample the harness will pronounce on, and
	// it is the most important field in this struct.
	//
	// B25 is the reason it exists. §4.4's whole delivery pipeline was
	// built, wired and admin-visible with NOTHING PRODUCING ALERTS, so a
	// four-hour Gate 3 run would have dropped zero alerts — truthfully,
	// because zero alerts existed — and passed. "Zero dropped" must never
	// be read as a pass on an empty run, so an empty run reports a FAILED
	// condition with a reason, exactly as gate2's MinRevocations does.
	MinAlerts int

	// MinCategories is the number of distinct alert CATEGORIES the run must
	// exercise. §3.2 requires all three (esi_notification, domain_event,
	// threshold), and a run that exercised only CCP notifications would
	// pass every other condition here while proving nothing about the two
	// categories that had no producer before Phase 20.4.
	MinCategories int

	// OutputDir receives the artefacts. Empty writes nothing, which is what
	// the integration test wants.
	OutputDir string

	// Notes is free text recorded alongside the result.
	Notes string
}

// Gate3Tally is what the GENERATOR fed in — the harness's own count of its
// own input, kept by whatever drives the run and handed to MeasureGate3.
//
// EmitResult already reports exactly these per call
// (alerting.EmitResult.EventsRecorded / EventsDeduplicated /
// DeliveriesEnqueued), so a driver accumulates rather than instruments.
type Gate3Tally struct {
	// Offered is how many alert OCCURRENCES were handed to the emitter,
	// counting one per (occurrence, routed target) — the unit
	// app.alert_event has one row per, so that events_written and
	// suppressed_by_dedupe below sum to it.
	Offered int `json:"offered"`
	// EventsWritten and SuppressedByDedupe are the emitter's own report of
	// what became of them.
	EventsWritten      int `json:"events_written"`
	SuppressedByDedupe int `json:"suppressed_by_dedupe"`
	// DeliveriesEnqueued is how many app.alert_delivery rows the emitter
	// reported creating.
	DeliveriesEnqueued int `json:"deliveries_enqueued"`
}

// Add accumulates one emit's outcome. Safe for concurrent use, since a
// realistic run generates from several goroutines.
func (t *Gate3Tally) Add(offered, eventsWritten, suppressed, deliveries int) {
	gate3TallyMu.Lock()
	defer gate3TallyMu.Unlock()
	t.Offered += offered
	t.EventsWritten += eventsWritten
	t.SuppressedByDedupe += suppressed
	t.DeliveriesEnqueued += deliveries
}

var gate3TallyMu sync.Mutex

// Gate3Result is one measurement window's verdict.
type Gate3Result struct {
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Notes      string            `json:"notes"`
	Generated  Gate3Tally        `json:"generated"`
	Observed   Gate3Observed     `json:"observed"`
	Conditions []ConditionResult `json:"conditions"`
}

// Gate3Observed is what the DATABASE holds at the end of the run.
type Gate3Observed struct {
	// Events is COUNT(app.alert_event) inside the window — the independent
	// check on the emitter's own EventsWritten tally.
	Events int `json:"events"`
	// EventsWithoutDelivery is an event nobody will ever act on: a row
	// with no app.alert_delivery children. Not a drop by §3.1's letter —
	// nothing was ever owed to a channel — but it is a silent loss by its
	// spirit, and it is the one failure mode the delivery-side identity
	// cannot see.
	EventsWithoutDelivery int `json:"events_without_delivery"`
	// Deliveries is COUNT(app.alert_delivery) inside the window.
	Deliveries int `json:"deliveries"`
	// MessagesSent is the number of distinct (channel, coalescing key)
	// groups among SENT deliveries — the number of messages that actually
	// left the system.
	MessagesSent int `json:"messages_sent"`
	// CoalescedInto is sent deliveries minus MessagesSent: alerts
	// delivered inside somebody else's roll-up.
	CoalescedInto int `json:"coalesced_into"`
	// DeadLettered and Pending are the other two terminal-ish states.
	// Pending at end of run is §3.1's drop.
	DeadLettered int `json:"dead_lettered"`
	Pending      int `json:"pending"`
	// Failed counts the 'failed' state, which internal/alerting
	// deliberately never writes (see deadletter.go's header). A non-zero
	// value here means something outside the dispatcher wrote it, and it
	// would be invisible to both the pump and the dead-letter board — an
	// alert in a state nobody looks at. Counted so it can never become
	// that.
	Failed int `json:"failed"`
	// Categories is how many distinct app.alert_type.category values the
	// run's events span, for §3.2's three.
	Categories int `json:"categories"`
	// Domains is the same for domain, for §3's "all eight".
	Domains int `json:"domains"`
	// UnknownTypesBoarded is how many rows the run put on
	// app.notification_unknown_type — Gate 3.2's other half.
	UnknownTypesBoarded int `json:"unknown_types_boarded"`
}

// Passed reports whether every evaluated condition passed.
func (r *Gate3Result) Passed() bool {
	if len(r.Conditions) == 0 {
		return false
	}
	for _, c := range r.Conditions {
		if !c.Passed {
			return false
		}
	}
	return true
}

// Gate3Querier is the database handle the measurement needs. *pgxpool.Pool
// satisfies it; an interface so the harness can be pointed at a
// transaction in a test.
type Gate3Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// MeasureGate3 reads the run's outcome out of the database and evaluates
// the conditions this harness can decide from it.
//
// Conditions 3.3 (unparseable YAML imports as JSONB and renders
// generically), 3.4 (40 coalesced events render as one message inside each
// channel's size limit), 3.5 (char-notification's 5-token reserve holds),
// 3.6 (dedupe hashes survive a restart) and 3.8 (build-time source-route
// validation) are NOT evaluated here, and the omission is deliberate in
// exactly the way gate2's is: each is a statement about a code path or a
// rendered string rather than about a run's row counts, and each is
// asserted directly by a test that can actually see the thing
// (internal/alerting's suite, catalogue's ValidateThresholds under `make
// check-alert-sources`, and internal/esi/ratelimit's admission tests).
// Reporting them as "passed" because a run completed would be inventing
// evidence.
func MeasureGate3(ctx context.Context, db Gate3Querier, cfg Gate3Config, generated Gate3Tally) (*Gate3Result, error) {
	res := &Gate3Result{StartedAt: cfg.Since, Notes: cfg.Notes, Generated: generated}

	if err := db.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE NOT EXISTS (
		            SELECT 1 FROM app.alert_delivery d WHERE d.event_id = e.event_id))::int,
		       count(DISTINCT t.category)::int,
		       count(DISTINCT t.domain)::int
		  FROM app.alert_event e
		  JOIN app.alert_type t ON t.alert_type = e.alert_type
		 WHERE e.occurred_at >= $1`, cfg.Since).
		Scan(&res.Observed.Events, &res.Observed.EventsWithoutDelivery,
			&res.Observed.Categories, &res.Observed.Domains); err != nil {
		return nil, fmt.Errorf("gate3: counting alert events: %w", err)
	}

	// Deliveries are scoped by their EVENT's occurred_at, not by their own
	// created_at: a delivery belongs to the run that generated it, and a
	// coalesced group's deliveries are created across the window while the
	// occurrence they answer to is fixed.
	//
	// A sent delivery whose event has a NULL coalesce_key (coalescing
	// disabled) is its own message, which is why the group key falls back
	// to the event id rather than collapsing every uncoalesced alert on a
	// channel into one — the same fallback alerting.Dispatcher.group makes,
	// and it must agree with it or this measurement would count messages
	// the pump never sent.
	// `sent` and `messages` come out of ONE statement, and coalesced_into
	// is their difference. Two queries would give two instants, and a
	// delivery settling between them would make the identity fail on a
	// perfectly healthy run — which is exactly how esi_ledger_divergence
	// spent three phases reporting throughput as error.
	var sent int
	if err := db.QueryRow(ctx, `
		WITH scoped AS (
		    SELECT d.delivery_id, d.state, d.channel_id,
		           coalesce(e.coalesce_key, e.event_id::text) AS group_key
		      FROM app.alert_delivery d
		      JOIN app.alert_event e ON e.event_id = d.event_id
		     WHERE e.occurred_at >= $1
		)
		SELECT count(*)::int,
		       count(*) FILTER (WHERE state = 'sent')::int,
		       count(DISTINCT (channel_id, group_key)) FILTER (WHERE state = 'sent')::int,
		       count(*) FILTER (WHERE state = 'dead_letter')::int,
		       count(*) FILTER (WHERE state = 'pending')::int,
		       count(*) FILTER (WHERE state = 'failed')::int
		  FROM scoped`, cfg.Since).
		Scan(&res.Observed.Deliveries, &sent, &res.Observed.MessagesSent,
			&res.Observed.DeadLettered, &res.Observed.Pending, &res.Observed.Failed); err != nil {
		return nil, fmt.Errorf("gate3: counting alert deliveries: %w", err)
	}
	res.Observed.CoalescedInto = sent - res.Observed.MessagesSent

	if err := db.QueryRow(ctx, `
		SELECT count(*)::int FROM app.notification_unknown_type WHERE last_seen_at >= $1`,
		cfg.Since).Scan(&res.Observed.UnknownTypesBoarded); err != nil {
		return nil, fmt.Errorf("gate3: counting boarded unknown types: %w", err)
	}

	res.FinishedAt = time.Now()
	res.Conditions = evaluateGate3(cfg, res)
	return res, nil
}

func evaluateGate3(cfg Gate3Config, res *Gate3Result) []ConditionResult {
	var out []ConditionResult
	o, g := res.Observed, res.Generated

	// The sample-size gate comes FIRST and is itself a condition, so an
	// empty run reports a failure with a reason rather than a vacuous
	// pass. This is the condition B25 would have failed for two years.
	enough := g.Offered >= cfg.MinAlerts && o.Events > 0
	out = append(out, ConditionResult{
		ID:          "3.1-sample",
		Description: "the run actually generated alerts — \"zero dropped\" is meaningless over an empty pipeline",
		Passed:      enough,
		Measurement: fmt.Sprintf("%d occurrences offered (minimum %d), %d events in the database", g.Offered, cfg.MinAlerts, o.Events),
	})
	if !enough {
		return out
	}

	out = append(out, ConditionResult{
		ID:          "3.1-categories",
		Description: "all three §4.4 alert categories were exercised (esi_notification, domain_event, threshold)",
		Passed:      o.Categories >= cfg.MinCategories,
		Measurement: fmt.Sprintf("%d distinct categories, %d distinct domains (minimum %d categories)", o.Categories, o.Domains, cfg.MinCategories),
	})

	// The occurrence identity: everything offered either became an event
	// or was deduplicated, and the emitter's count of the former agrees
	// with what the database holds.
	occurrencesBalance := g.Offered == g.EventsWritten+g.SuppressedByDedupe
	out = append(out, ConditionResult{
		ID:          "3.1-occurrences",
		Description: "offered == events_written + suppressed_by_dedupe",
		Passed:      occurrencesBalance,
		Measurement: fmt.Sprintf("%d offered, %d written + %d deduplicated = %d",
			g.Offered, g.EventsWritten, g.SuppressedByDedupe, g.EventsWritten+g.SuppressedByDedupe),
	})
	out = append(out, ConditionResult{
		ID:          "3.1-events-persisted",
		Description: "every event the emitter reported writing is in the database",
		Passed:      o.Events == g.EventsWritten,
		Measurement: fmt.Sprintf("emitter reported %d events written, database holds %d", g.EventsWritten, o.Events),
	})

	// The delivery identity — §3.1's own, over the unit that can be lost.
	deliveriesBalance := o.Deliveries == o.MessagesSent+o.CoalescedInto+o.DeadLettered+o.Pending+o.Failed
	out = append(out, ConditionResult{
		ID:          "3.1",
		Description: "enqueued == messages_sent + coalesced_into + dead_lettered + pending (the §3.1 accounting identity)",
		Passed:      deliveriesBalance,
		Measurement: fmt.Sprintf("%d enqueued = %d messages + %d coalesced + %d dead-lettered + %d pending + %d failed",
			o.Deliveries, o.MessagesSent, o.CoalescedInto, o.DeadLettered, o.Pending, o.Failed),
	})

	// THE DROP CONDITION. Everything above can balance while alerts sit in
	// a queue nobody drains; this is the one that says they did not.
	out = append(out, ConditionResult{
		ID:          "3.1-dropped",
		Description: "no delivery is left neither sent nor dead-lettered at end of run — §3.1's definition of a drop",
		Passed:      o.Pending == 0 && o.Failed == 0,
		Measurement: fmt.Sprintf("%d still pending, %d in the unreachable 'failed' state", o.Pending, o.Failed),
	})

	// An event with no delivery row is the failure mode the delivery-side
	// identity structurally cannot see: it balances perfectly, because the
	// alert never entered the delivery accounting at all.
	out = append(out, ConditionResult{
		ID:          "3.1-actionable",
		Description: "every generated event has at least one delivery — an event with none can never be acted on",
		Passed:      o.EventsWithoutDelivery == 0,
		Measurement: fmt.Sprintf("%d of %d events have no delivery row", o.EventsWithoutDelivery, o.Events),
	})

	out = append(out, ConditionResult{
		ID:          "3.2",
		Description: "unrecognised CCP notification types reached the unknown-types board",
		Passed:      o.UnknownTypesBoarded > 0,
		Measurement: fmt.Sprintf("%d unknown types boarded during the run", o.UnknownTypesBoarded),
	})

	return out
}

// ── §3.2's "channel stubs that inject failures" ──────────────────────────

// FlakyChannel is an alerting channel that fails on demand, so a Gate 3 run
// exercises §3.3's "channel outages produce retries then dead-letters,
// never queue blockage" rather than only the happy path.
//
// It records every message it ACCEPTS, which is what makes §3.4 checkable
// from the same run: "40 coalesced events render as one message" is a
// statement about what arrived at a channel, and no database query can see
// that — app.alert_delivery records forty rows marked sent either way.
type FlakyChannel struct {
	// ChannelKind is what Kind() reports; it decides which
	// alert_delivery_total{kind} series this stub's outcomes land on, so a
	// run can stub all three of §4.4's kinds distinctly.
	ChannelKind string
	// FailFirst makes the first N sends fail transiently — retried with
	// backoff, then delivered. Models a channel that is briefly down.
	FailFirst int
	// FailPermanently makes every send return a channels.PermanentError,
	// which dead-letters immediately without burning the attempt budget.
	// Models a webhook URL that has been deleted.
	FailPermanently bool
	// FailAlways makes every send fail transiently, so deliveries exhaust
	// their attempts and dead-letter the slow way. Models a channel that
	// is down for the whole run — §3.3's real case, and the one that
	// proves the queue does not block behind it.
	FailAlways bool

	mu       sync.Mutex
	attempts int
	accepted []channels.Message
}

// Kind implements channels.Channel.
func (c *FlakyChannel) Kind() string {
	if c.ChannelKind == "" {
		return channels.KindSlackWebhook
	}
	return c.ChannelKind
}

// Send implements channels.Channel.
func (c *FlakyChannel) Send(_ context.Context, msg channels.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++

	switch {
	case c.FailPermanently:
		return &channels.PermanentError{Reason: "gate3 stub: channel permanently unavailable"}
	case c.FailAlways:
		return fmt.Errorf("gate3 stub: channel unavailable (attempt %d)", c.attempts)
	case c.attempts <= c.FailFirst:
		return fmt.Errorf("gate3 stub: transient failure (attempt %d of %d)", c.attempts, c.FailFirst)
	}
	c.accepted = append(c.accepted, msg)
	return nil
}

// Accepted returns the messages this channel took delivery of, in order.
func (c *FlakyChannel) Accepted() []channels.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]channels.Message(nil), c.accepted...)
}

// Attempts returns how many times Send was called, successful or not.
func (c *FlakyChannel) Attempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}
