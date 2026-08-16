package main

// world.go builds the installation Gate 3 generates into: the entities the
// threshold evaluator reads, and one stub channel per failure behaviour
// with routing rules that cover all eight §4.4 domains.
//
// ── WHY THE ROUTED TYPES COME FROM THE CATALOGUE ─────────────────────────
// The alert types this run exercises are read out of
// internal/alerting/catalogue rather than listed here. A hand-written list
// would drift from the seeded set, and the drift would show up as a gate
// that quietly stopped covering a domain — which is exactly the failure
// §3.2's "all eight domains" clause exists to catch.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/test/load"
)

const (
	gate3CorporationID = int64(98_000_001)
	gate3CharacterID   = int64(2_124_613_505)
	// gate3UnknownType is a CCP notification type this build has never
	// heard of — §3.2's "unrecognised types deliver via the generic
	// fallback renderer and appear on the unknown-types board".
	gate3UnknownType = "GateThreeInventedTypeMsg"
)

// stubChannel is one configured channel and the behaviour its stub models.
type stubChannel struct {
	id        uuid.UUID
	name      string
	behaviour string
	stub      *load.FlakyChannel
}

type world struct {
	pool     *pgxpool.Pool
	store    *store.Store
	channels []*stubChannel
	byID     map[uuid.UUID]*stubChannel
	mu       sync.Mutex

	// notificationTypes and domainEventTypes are what the generator cycles
	// through, taken from the catalogue.
	notificationTypes []string
	domainEventTypes  []string
}

func newWorld(ctx context.Context, pool *pgxpool.Pool) (*world, error) {
	w := &world{pool: pool, store: store.New(pool), byID: map[uuid.UUID]*stubChannel{}}

	if err := w.seedEntities(ctx); err != nil {
		return nil, err
	}

	// Four behaviours, so the run exercises every terminal outcome §3.1's
	// identity has a term for. A run with only healthy channels balances
	// trivially and proves nothing about the failure paths.
	healthy := w.newStub(channels.KindSlackWebhook, 0, false, false)
	transient := w.newStub(channels.KindDiscordWebhook, 2, false, false)
	permanent := w.newStub(channels.KindSMTP, 0, true, false)
	alwaysDown := w.newStub(channels.KindSlackWebhook, 0, false, true)

	behaviours := []struct {
		name      string
		behaviour string
		stub      *load.FlakyChannel
	}{
		{"healthy", "always accepts", healthy},
		{"transient", "fails the first 2 sends, then accepts (retry path)", transient},
		{"permanent", "PermanentError on every send (immediate dead-letter)", permanent},
		{"always-down", "transient failure on every send (dead-letters the slow way)", alwaysDown},
	}
	for _, b := range behaviours {
		if _, err := w.createChannel(ctx, b.name, b.behaviour, b.stub); err != nil {
			return nil, err
		}
	}

	// Every seeded alert type is routed, and the four behaviours are dealt
	// round-robin across them. That guarantees all eight domains produce
	// deliveries AND that each behaviour sees traffic from more than one
	// domain.
	seeded := catalogue.Names()

	for i, name := range seeded {
		target := w.channels[i%len(w.channels)]
		if err := w.route(ctx, name, target); err != nil {
			return nil, err
		}
	}
	// The unknown type is routed too, so §3.2's board entry ALSO delivers
	// rather than only being recorded.
	if err := w.ensureUnknownRoutable(ctx); err != nil {
		return nil, err
	}

	for _, t := range catalogue.Catalogue {
		switch t.Category {
		case catalogue.CategoryESINotification:
			w.notificationTypes = append(w.notificationTypes, t.Name)
		case catalogue.CategoryDomainEvent:
			w.domainEventTypes = append(w.domainEventTypes, t.Name)
		}
	}
	if len(w.notificationTypes) == 0 || len(w.domainEventTypes) == 0 {
		return nil, fmt.Errorf("gate3: the catalogue produced no notification or domain-event types to generate")
	}
	return w, nil
}

// seedEntities creates the rows the threshold evaluator reads. All four
// thresholds are given real subjects, because a threshold with no subject
// emits nothing and would silently reduce §3.2's three categories to two.
//
// ── WHY THE DEADLINES ARE MINUTES AWAY AND NOT HOURS ─────────────────────
// Measured on the first full smoke run: 336 deliveries sat `pending` with
// attempts = 0 and next_attempt_at as far out as the following evening, and
// §3.1's drop condition failed on them.
//
// They were not dropped. They were not yet DUE. A coalesced delivery becomes
// claimable at the close of its coalescing window (CoalesceKey.Due), the
// bucket comes from the event's OccurredAt, and the structure-fuel and
// contract-expiry thresholds deliberately stamp OccurredAt as the EXPIRY
// rather than the evaluation time — so a structure whose fuel runs out in 17
// hours produces a delivery claimable in 17 hours.
//
// That is a real finding about the product, recorded in
// docs/PRE_V1_OPEN_ITEMS.md: an early-warning alert scheduled for the moment
// the thing it warns about happens is not an early warning. It is not
// something this gate should measure as a DROP, though — §3 is about whether
// the delivery pipeline loses alerts, and a delivery that has not come due
// has not been lost. So the subjects are seeded with deadlines inside the
// run, the pipeline is exercised end to end, and the scheduling behaviour is
// reported separately by its own condition rather than swallowing this
// gate's verdict.
func (w *world) seedEntities(ctx context.Context) error {
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO app.corporation (corporation_id, name, ticker)
		VALUES ($1, 'Gate3 Corp', 'G3') ON CONFLICT (corporation_id) DO NOTHING`, gate3CorporationID); err != nil {
		return fmt.Errorf("gate3: seeding corporation: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO app.character (character_id, name, owner_hash, corporation_id)
		VALUES ($1, 'Gate3 Pilot', 'gate3-owner-hash', $2) ON CONFLICT (character_id) DO NOTHING`,
		gate3CharacterID, gate3CorporationID); err != nil {
		return fmt.Errorf("gate3: seeding character: %w", err)
	}

	// corporation.structure.fuel_low — inside the 48h margin, and with
	// expiries MINUTES away rather than hours. That is not cosmetic; see
	// the note below on why a threshold's deadline has to fall inside the
	// run for its deliveries to be measurable at all.
	for i := 0; i < 12; i++ {
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO app.corporation_structure
			    (corporation_id, structure_id, type_id, system_id, fuel_expires, state)
			VALUES ($1, $2, 35832, 30000142, now() + make_interval(mins => $3), 'shield_vulnerable')
			ON CONFLICT (corporation_id, structure_id) DO NOTHING`,
			gate3CorporationID, int64(1_000_000_000_000+i), int32(i*3)); err != nil {
			return fmt.Errorf("gate3: seeding structure: %w", err)
		}
	}

	// corporation.member.inactive — logged off long enough ago to cross.
	//
	// The member characters have to EXIST first: app.corporation_member_tracking
	// carries a foreign key to app.character, which is the schema saying that
	// membership is a fact about a character HANGAR knows, not a bare id.
	for i := 0; i < 8; i++ {
		memberID := int64(2_200_000_000 + i)
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO app.character (character_id, name, owner_hash, corporation_id)
			VALUES ($1, $2, $3, $4) ON CONFLICT (character_id) DO NOTHING`,
			memberID, fmt.Sprintf("Gate3 Member %d", i), fmt.Sprintf("gate3-member-hash-%d", i),
			gate3CorporationID); err != nil {
			return fmt.Errorf("gate3: seeding member character: %w", err)
		}
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO app.corporation_member_tracking
			    (corporation_id, character_id, logon_date, logoff_date)
			VALUES ($1, $2, now() - interval '200 days', now() - interval '180 days')
			ON CONFLICT (corporation_id, character_id) DO NOTHING`,
			gate3CorporationID, memberID); err != nil {
			return fmt.Errorf("gate3: seeding member tracking: %w", err)
		}
	}

	// corporation.contract.expiring — outstanding, expiring inside 72h.
	for i := 0; i < 6; i++ {
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO app.contract
			    (contract_id, owner_kind, owner_id, issuer_id, issuer_corporation_id,
			     type, status, availability, date_issued, date_expired, title)
			VALUES ($1, 'corporation', $2, $3, $2, 'item_exchange', 'outstanding', 'corporation',
			        now() - interval '1 day', now() + make_interval(mins => $4), $5)
			ON CONFLICT (owner_kind, owner_id, contract_id) DO NOTHING`,
			int64(4_000_000+i), gate3CorporationID, gate3CharacterID, int32(5+i*12),
			fmt.Sprintf("Gate3 Contract %d", i)); err != nil {
			return fmt.Errorf("gate3: seeding contract: %w", err)
		}
	}
	return nil
}

func (w *world) createChannel(ctx context.Context, name, behaviour string, stub *load.FlakyChannel) (*stubChannel, error) {
	row, err := w.store.CreateAlertChannel(ctx, stub.Kind(), "gate3-"+name+"-"+uuid.NewString(),
		json.RawMessage(`{"url":"https://example.invalid/hook"}`))
	if err != nil {
		return nil, fmt.Errorf("gate3: creating channel %s: %w", name, err)
	}
	ch := &stubChannel{id: row.ChannelID, name: name, behaviour: behaviour, stub: stub}
	w.channels = append(w.channels, ch)
	w.byID[row.ChannelID] = ch
	return ch, nil
}

// route sends alertType to a stub channel, addressed to the INSTALLATION
// audience.
//
// The ref is empty deliberately. `installation` means "everyone who operates
// this installation" — an audience, not an entity — so it has no id to
// carry, and internal/alerting's threshold TargetFilter never narrows it for
// exactly that reason: a target kind with no ownership claim has nothing to
// contradict. Giving it a corporation id would still deliver, because
// routing resolves by alert TYPE, but it would encode a target no operator
// would ever configure and make the evidence describe a routing table that
// does not occur in practice.
func (w *world) route(ctx context.Context, alertType string, target *stubChannel) error {
	ref := ""
	_, err := w.store.CreateAlertRoutingRule(ctx, gen.CreateAlertRoutingRuleParams{
		AlertType: alertType, TargetKind: "installation", TargetRef: &ref, ChannelID: target.id,
	})
	if err != nil {
		return fmt.Errorf("gate3: routing %s: %w", alertType, err)
	}
	return nil
}

// ensureUnknownRoutable registers the invented notification type and routes
// it. The registration happens on FIRST SIGHTING through the emitter
// (Principle 14), so this pre-creates the app.alert_type row the same way
// EnsureAlertType would, then routes it — the real operator workflow, where
// a type is discovered and then given a destination.
func (w *world) ensureUnknownRoutable(ctx context.Context) error {
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO app.alert_type (alert_type, domain, category, default_enabled)
		VALUES ($1, 'unknown', 'esi_notification', true)
		ON CONFLICT (alert_type) DO NOTHING`, gate3UnknownType); err != nil {
		return fmt.Errorf("gate3: registering the unknown type: %w", err)
	}
	return w.route(ctx, gate3UnknownType, w.channels[0])
}

// channelFor is the Dispatcher's channel factory: every configured channel
// resolves to its stub rather than to a real webhook.
func (w *world) channelFor(row gen.AppAlertChannel) (channels.Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch, ok := w.byID[row.ChannelID]
	if !ok {
		return nil, fmt.Errorf("gate3: no stub for channel %s", row.ChannelID)
	}
	return ch.stub, nil
}

// largestRollup is §3.4's evidence, and no database query can produce it:
// app.alert_delivery records forty rows marked sent whether they went out
// as forty messages or as one. Only the channel knows.
func (w *world) largestRollup() int {
	largest := 0
	for _, c := range w.channels {
		for _, m := range c.stub.Accepted() {
			if m.Count > largest {
				largest = m.Count
			}
		}
	}
	return largest
}

func (w *world) newStub(kind string, failFirst int, permanent, always bool) *load.FlakyChannel {
	return &load.FlakyChannel{
		ChannelKind: kind, FailFirst: failFirst,
		FailPermanently: permanent, FailAlways: always,
	}
}
