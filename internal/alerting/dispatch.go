package alerting

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/alerting/render"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// DefaultClaimSize bounds one pump pass.
//
// It must comfortably exceed the largest coalescing group expected, since
// a group is rolled up from the deliveries claimed in ONE pass: a group
// split across two claims would send two messages instead of one. §4.4's
// worked example is 40 events, so 500 leaves an order of magnitude of
// headroom. TestFortyEventsCoalesceToOneMessage is the guard.
const DefaultClaimSize = 500

// Dispatcher is the outbox pump: claim pending deliveries, group them into
// coalesced roll-ups, render, send, and settle. One Tick is one pass; the
// caller (a River periodic job, or a plain ticker) decides the cadence.
//
// Nothing in a Tick sleeps or retries in place — §4.4's "never block the
// queue". A failing channel costs one attempt and a future
// next_attempt_at, and the pass moves on to the next group.
type Dispatcher struct {
	Pool store.Pool
	// Channels builds a live channel from its app.alert_channel row.
	// Defaults to channels.New. Overridable so a test can substitute a
	// recording fake without an HTTP or SMTP server.
	Channels func(row gen.AppAlertChannel) (channels.Channel, error)
	Policy   RetryPolicy
	// ClaimSize bounds one pass; zero selects DefaultClaimSize.
	ClaimSize int32
	// Observer, when set, is told the outcome of every delivery this pump
	// settles — Phase 20.4's alert_delivery_total. Nil is a valid pump
	// (every test uses one); see DeliveryObserver.
	Observer DeliveryObserver
	Now      func() time.Time
	Log      *slog.Logger
}

// DeliveryObserver receives one call per SETTLED delivery, with the channel
// kind it was for and what happened to it.
//
// An interface rather than a *telemetry.AlertDeliveries so internal/
// alerting does not import internal/telemetry — the same direction rule
// internal/esi.Observer follows for the Gate 1 counters, and for the same
// reason: the pump must be constructible and testable without a Prometheus
// registry.
//
// ── WHY SETTLED, NOT ATTEMPTED ───────────────────────────────────────────
// Gate 3.1's accounting identity is over TERMINAL outcomes: an alert is
// dropped if it was generated and neither delivered nor dead-lettered. A
// delivery that failed and will be retried has not reached a terminal
// state, and counting the attempt as a failure would make the counter a
// measure of channel flakiness rather than of alert fate. `retried` is
// therefore its own outcome and not a failure — it says "this one is
// still owed", which is exactly the third possibility §3.1 admits.
type DeliveryObserver interface {
	ObserveAlertDelivery(kind, outcome string)
}

// Delivery outcomes reported to a DeliveryObserver. They match
// telemetry.Alert* deliberately — the metric's label values and the pump's
// vocabulary must not be two lists that can drift.
const (
	OutcomeSent         = "sent"
	OutcomeRetried      = "retried"
	OutcomeDeadLettered = "dead_lettered"
	// KindUnknown labels a delivery settled before its channel row could
	// be read — the row was deleted between enqueue and claim. The
	// delivery still has a fate and must still be counted; what it does
	// not have is a knowable kind, and calling it "smtp" to avoid an
	// awkward label value would be a lie in a metric.
	KindUnknown = "unset"
)

// TickResult summarises one pass, for logging and for tests.
type TickResult struct {
	Claimed      int
	Groups       int
	Sent         int
	Retried      int
	DeadLettered int
}

// Tick runs one pass of the pump.
func (d *Dispatcher) Tick(ctx context.Context) (TickResult, error) {
	var result TickResult
	s := store.New(d.Pool)

	claimed, err := s.ClaimPendingAlertDeliveries(ctx, d.claimSize())
	if err != nil {
		return result, fmt.Errorf("alerting: claiming pending deliveries: %w", err)
	}
	result.Claimed = len(claimed)
	if len(claimed) == 0 {
		return result, nil
	}

	groups, err := d.group(ctx, s, claimed)
	if err != nil {
		return result, err
	}
	result.Groups = len(groups)

	for _, g := range groups {
		sent, retried, dead := d.deliver(ctx, s, g)
		result.Sent += sent
		result.Retried += retried
		result.DeadLettered += dead
	}
	return result, nil
}

// deliveryGroup is one coalesced roll-up: everything claimed that shares a
// channel and a coalescing key.
type deliveryGroup struct {
	ChannelID   uuid.UUID
	CoalesceKey string
	AlertType   string
	// Target is read back out of the coalescing key — it is the routing
	// audience this group is for, and the only way to find which routing
	// rule's mention applies (app.alert_delivery carries a channel, not a
	// rule).
	Target     Target
	Deliveries []gen.AppAlertDelivery
	Events     []gen.AppAlertEvent
}

// group buckets claimed deliveries by (channel, coalescing key) — §4.4's
// "(routing target, alert type)", since the coalescing key already
// encodes the target and the alert type and the channel is the concrete
// destination the message goes to.
//
// A delivery whose event carries a NULL coalesce_key gets a group of its
// own, keyed on the event id, so coalescing can be switched off per
// installation without any other code path changing.
func (d *Dispatcher) group(ctx context.Context, s *store.Store, claimed []gen.AppAlertDelivery) ([]deliveryGroup, error) {
	byKey := make(map[string]*deliveryGroup, len(claimed))
	var order []string

	for _, delivery := range claimed {
		event, err := s.GetAlertEvent(ctx, delivery.EventID)
		if err != nil {
			return nil, fmt.Errorf("alerting: reading event %s for delivery %s: %w", delivery.EventID, delivery.DeliveryID, err)
		}

		coalesceKey := ""
		if event.CoalesceKey != nil {
			coalesceKey = *event.CoalesceKey
		}
		parsed, parseOK := ParseCoalesceKey(coalesceKey)

		bucket := delivery.ChannelID.String() + "\x00" + coalesceKey
		if !parseOK || !parsed.Coalesces() {
			// Coalescing disabled (or an unparseable key from another
			// version): this event gets a group of its own rather than
			// being lumped in with every other event of its type.
			bucket += "\x00" + event.EventID.String()
		}

		g, ok := byKey[bucket]
		if !ok {
			g = &deliveryGroup{
				ChannelID: delivery.ChannelID, CoalesceKey: coalesceKey,
				AlertType: event.AlertType, Target: parsed.Target,
			}
			byKey[bucket] = g
			order = append(order, bucket)
		}
		g.Deliveries = append(g.Deliveries, delivery)
		g.Events = append(g.Events, event)
	}

	out := make([]deliveryGroup, 0, len(order))
	for _, bucket := range order {
		g := byKey[bucket]
		// Oldest first: a truncated roll-up must drop the tail, never the
		// first thing that happened (render.Rollup's contract).
		sort.SliceStable(g.Events, func(i, j int) bool {
			return g.Events[i].OccurredAt.Before(g.Events[j].OccurredAt)
		})
		out = append(out, *g)
	}
	return out, nil
}

// deliver renders and sends one group, then settles every delivery in it.
func (d *Dispatcher) deliver(ctx context.Context, s *store.Store, g deliveryGroup) (sent, retried, dead int) {
	now := d.now()

	channelRow, err := s.GetAlertChannel(ctx, g.ChannelID)
	if err != nil {
		// No channel row, so no knowable kind — see KindUnknown.
		return d.settleAll(ctx, s, g, KindUnknown, fmt.Errorf("reading channel %s: %w", g.ChannelID, err), now)
	}
	if !channelRow.Enabled {
		// Disabled between enqueue and delivery. Dead-lettering (rather
		// than leaving it pending forever, or quietly marking it sent) is
		// the honest outcome: the alert was not delivered, and §4.4 says
		// that must be visible.
		return d.settleAll(ctx, s, g, channelRow.Kind, &channels.PermanentError{
			Reason: fmt.Sprintf("channel %q (%s) is disabled", channelRow.Name, channelRow.Kind),
		}, now)
	}

	channel, err := d.channelFor(channelRow)
	if err != nil {
		return d.settleAll(ctx, s, g, channelRow.Kind, &channels.PermanentError{Reason: "building channel", Err: err}, now)
	}

	msg := d.message(g)
	msg.Mention = d.mentionFor(ctx, s, g)
	if err := channel.Send(ctx, msg); err != nil {
		if d.Log != nil {
			d.Log.WarnContext(ctx, "alerting: delivery failed",
				"alert_type", g.AlertType, "channel", channelRow.Name, "kind", channelRow.Kind,
				"events", len(g.Events), "error", err)
		}
		return d.settleAll(ctx, s, g, channelRow.Kind, err, now)
	}

	// One send, every delivery in the group marked sent: a coalesced
	// sibling WAS delivered — inside the roll-up — so 'sent' is accurate,
	// not a convenient fiction. Marking them anything else would either
	// re-send the same content or hide a delivered alert from the audit
	// trail.
	for _, delivery := range g.Deliveries {
		if err := s.MarkAlertDeliverySent(ctx, delivery.DeliveryID); err != nil {
			if d.Log != nil {
				d.Log.ErrorContext(ctx, "alerting: marking delivery sent failed", "delivery_id", delivery.DeliveryID, "error", err)
			}
			continue
		}
		sent++
		d.observe(channelRow.Kind, OutcomeSent)
	}
	return sent, 0, 0
}

// observe reports one settled delivery to the DeliveryObserver, if there is
// one. It is called only where the database write SUCCEEDED, so the counter
// can never claim an outcome the table does not also record.
func (d *Dispatcher) observe(kind, outcome string) {
	if d.Observer == nil {
		return
	}
	d.Observer.ObserveAlertDelivery(kind, outcome)
}

// message builds the channel-agnostic Message for a group. Each channel
// truncates it to its own limit (§4.4's "different size limits"), which is
// why the lines are handed over as a slice rather than a finished body.
//
// ── PHASE 20.4: A GROUP OF ONE IS RENDERED, NOT FOLDED ───────────────────
// render.Line folds the generic key/value listing onto ONE line, because a
// roll-up of forty events must list one line per event. render.Render is
// the same chain without the folding, and its doc comment has said since
// Phase 14 that it is "the full body for a message carrying a single
// event" — it simply had no caller, so every single-event alert went out
// as a one-line `key value; key value; key value` squash of what should
// have been a readable listing. That is most of the alerts on a normal
// installation, and every alert that matters urgently: a structure comes
// under attack once, not forty times in five minutes.
func (d *Dispatcher) message(g deliveryGroup) channels.Message {
	summary := g.AlertType
	if entry, ok := catalogue.ByName(g.AlertType); ok && entry.Summary != "" {
		summary = entry.Summary
	}

	lines := make([]string, 0, len(g.Events))
	if len(g.Events) == 1 {
		event := g.Events[0]
		// Split back into lines so each channel still applies its own size
		// limit per line, as Message.Lines' contract requires — Render's
		// output is a multi-line listing, not a body.
		lines = append(lines, strings.Split(strings.TrimRight(render.Render(event.AlertType, event.Payload), "\n"), "\n")...)
	} else {
		for _, event := range g.Events {
			lines = append(lines, render.Line(event.AlertType, event.Payload))
		}
	}

	return channels.Message{
		AlertType: g.AlertType,
		Subject:   render.Header(summary, len(g.Events)),
		Header:    render.Header(summary, len(g.Events)),
		Lines:     lines,
		Count:     len(g.Events),
	}
}

// mentionFor resolves the routing rule's mention for this group's
// (target, channel) pair. app.alert_delivery stores a channel, not a rule,
// so the mention is re-resolved at send time rather than frozen at enqueue
// time — which also means an operator correcting a mention fixes messages
// that are already queued.
//
// A resolution failure is not a delivery failure: the alert goes out
// without the mention rather than not at all. That trade is deliberate —
// §4.4's delivery guarantees are about the alert arriving, and a missing
// "@here" is a far smaller harm than a lost structure-under-attack.
func (d *Dispatcher) mentionFor(ctx context.Context, s *store.Store, g deliveryGroup) string {
	if g.Target.Kind == "" {
		return ""
	}
	routing, err := Resolve(ctx, s, g.AlertType)
	if err != nil {
		if d.Log != nil {
			d.Log.WarnContext(ctx, "alerting: resolving mention failed; delivering without it",
				"alert_type", g.AlertType, "error", err)
		}
		return ""
	}
	for _, dest := range routing.Destinations[g.Target] {
		if dest.ChannelID == g.ChannelID {
			return dest.Mention
		}
	}
	return ""
}

// settleAll applies the retry/dead-letter decision to every delivery in a
// failed group. Each row is judged on its OWN attempts count: a delivery
// that has already failed four times dead-letters on this pass even though
// a sibling that just joined the group gets another try.
func (d *Dispatcher) settleAll(ctx context.Context, s *store.Store, g deliveryGroup, kind string, cause error, now time.Time) (sent, retried, dead int) {
	for _, delivery := range g.Deliveries {
		decision := d.Policy.Decide(int(delivery.Attempts), delivery.CreatedAt, cause, now)
		if err := Settle(ctx, s, delivery.DeliveryID, decision); err != nil {
			if d.Log != nil {
				d.Log.ErrorContext(ctx, "alerting: settling failed delivery failed", "delivery_id", delivery.DeliveryID, "error", err)
			}
			continue
		}
		if decision.DeadLetter {
			dead++
			d.observe(kind, OutcomeDeadLettered)
		} else {
			retried++
			d.observe(kind, OutcomeRetried)
		}
	}
	return 0, retried, dead
}

func (d *Dispatcher) channelFor(row gen.AppAlertChannel) (channels.Channel, error) {
	if d.Channels != nil {
		return d.Channels(row)
	}
	return channels.New(row.Kind, row.Config)
}

func (d *Dispatcher) claimSize() int32 {
	if d.ClaimSize > 0 {
		return d.ClaimSize
	}
	return DefaultClaimSize
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
