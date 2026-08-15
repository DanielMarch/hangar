//go:build integration

package load_test

// ── THE GATE 3 HARNESS, EXERCISED AS AN INTEGRATION TEST ─────────────────
//
// 04_RELEASE_GATES.md §0 rule 6 requires a gate's harness to land in an
// earlier phase than the run, "with their own exit criteria and their own
// tests". This file is those tests, and it is the counterpart of
// gate1_integration_test.go and gate2_integration_test.go.
//
// It runs the SAME measurement Phase 20.8 will run — MeasureGate3's SQL
// over app.alert_event and app.alert_delivery — against real rows produced
// by the real Emitter, the real threshold Evaluator and the real
// Dispatcher, at a scale of tens over seconds rather than thousands over
// four hours.
//
// IT IS NOT A GATE 3 RUN. No four hours, no eight domains at volume, no
// evidence artefact, and nothing here publishes a verdict about the
// release. 20.8 owns that.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/test/load"
)

func newGate3Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))
	// db/seed/alert_types.sql populates app.alert_type, which
	// app.alert_event has a foreign key to. Without it nothing can be
	// generated at all — which is itself worth knowing, and is why the
	// threshold registration path deliberately does not paper over a
	// missing row (see Evaluator.emitThreshold).
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))

	// The four THRESHOLD alert types cannot be seeded until their source
	// routes exist: app.alert_type's threshold_declares_source CHECK
	// requires a NOT NULL source_route_id, and db/seed/alert_types.sql
	// inserts them through a JOIN against app.esi_route (see that file's
	// header). On a real installation Phase 2's spec ingest supplies them;
	// here the four routes the catalogue declares are inserted directly,
	// and the seed re-run completes the catalogue.
	//
	// This is not test scaffolding around an inconvenience — it is §4.4's
	// build-time rule showing its teeth. A threshold alert whose source
	// route is not in the sync set can never fire, so the seed refuses to
	// create a row that would claim otherwise.
	s := store.New(pool)
	for i, path := range catalogue.ThresholdSourceRoutes() {
		_, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
			OperationID: fmt.Sprintf("Gate3Route%d", i), Method: "GET", UpstreamPath: path,
			CompatibilityDate: pgtype.Date{Time: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), Valid: true},
			SpecFragment:      json.RawMessage(`{}`), IdentifierTypes: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

// gate3World is one installation with routing rules for the alert types
// this run exercises, and a stub channel per rule.
type gate3World struct {
	pool          *pgxpool.Pool
	store         *store.Store
	corporationID int64
	channels      map[uuid.UUID]*load.FlakyChannel
}

func newGate3World(t testing.TB, pool *pgxpool.Pool) *gate3World {
	return &gate3World{
		pool: pool, store: store.New(pool),
		corporationID: 98000001,
		channels:      map[uuid.UUID]*load.FlakyChannel{},
	}
}

// route creates a channel backed by `stub` and an enabled routing rule
// sending alertType to it.
func (w *gate3World) route(t testing.TB, alertType, targetKind, targetRef string, stub *load.FlakyChannel) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	kind := stub.Kind()
	channel, err := w.store.CreateAlertChannel(ctx, kind, "gate3-"+uuid.NewString(),
		json.RawMessage(`{"url":"https://example.invalid/hook"}`))
	require.NoError(t, err)

	ref := targetRef
	_, err = w.store.CreateAlertRoutingRule(ctx, gen.CreateAlertRoutingRuleParams{
		AlertType: alertType, TargetKind: targetKind, TargetRef: &ref, ChannelID: channel.ChannelID,
	})
	require.NoError(t, err)

	w.channels[channel.ChannelID] = stub
	return channel.ChannelID
}

// dispatcher builds a pump whose channels are this world's stubs.
func (w *gate3World) dispatcher(policy alerting.RetryPolicy, observer alerting.DeliveryObserver, now func() time.Time) *alerting.Dispatcher {
	return &alerting.Dispatcher{
		Pool:   w.pool,
		Policy: policy,
		Now:    now,
		Channels: func(row gen.AppAlertChannel) (channels.Channel, error) {
			stub, ok := w.channels[row.ChannelID]
			if !ok {
				return nil, fmt.Errorf("gate3: no stub for channel %s", row.ChannelID)
			}
			return stub, nil
		},
		Observer: observer,
	}
}

// deliveryOutcomeCounter is the METRIC side of the run, so the test can check
// that alert_delivery_total's outcomes agree with the table — the
// cross-check gate3_alerts.go's header says is worth having and must not
// be the source of the verdict.
type deliveryOutcomeCounter struct{ byOutcome map[string]int }

func newDeliveryOutcomeCounter() *deliveryOutcomeCounter {
	return &deliveryOutcomeCounter{byOutcome: map[string]int{}}
}

func (o *deliveryOutcomeCounter) ObserveAlertDelivery(_, outcome string) { o.byOutcome[outcome]++ }

// seedFuelThresholdWorld inserts a corporation with structures whose fuel
// is about to run out — the real rows ListStructuresLowOnFuel reads.
func seedFuelThresholdWorld(t testing.TB, w *gate3World, structures int) {
	t.Helper()
	ctx := context.Background()
	_, err := w.pool.Exec(ctx,
		`INSERT INTO app.corporation (corporation_id, name, ticker) VALUES ($1, 'Gate3 Corp', 'G3')
		 ON CONFLICT (corporation_id) DO NOTHING`, w.corporationID)
	require.NoError(t, err)

	for i := 0; i < structures; i++ {
		_, err := w.pool.Exec(ctx, `
			INSERT INTO app.corporation_structure
			    (corporation_id, structure_id, type_id, system_id, fuel_expires, state)
			VALUES ($1, $2, 35832, 30000142, now() + make_interval(hours => $3), 'shield_vulnerable')`,
			w.corporationID, int64(1_000_000_000_000+i), int32(i+1))
		require.NoError(t, err)
	}
}

// TestGate3HarnessMeasuresWhatTheGateDefines drives the whole pipeline —
// generation through both producers, coalescing, delivery, retry and
// dead-lettering — and then asks MeasureGate3 for a verdict.
func TestGate3HarnessMeasuresWhatTheGateDefines(t *testing.T) {
	pool := newGate3Pool(t)
	ctx := context.Background()
	since := time.Now().Add(-time.Minute)

	w := newGate3World(t, pool)
	var tally load.Gate3Tally

	// ── category 1: CCP notifications, through the REAL sync handler seam
	//
	// Driven through handlers.SyncCharacterNotifications rather than by
	// calling the emitter directly, because the seam IS what Phase 20.4
	// added: an emitter with no caller is precisely the defect (B25) this
	// gate could not previously detect.
	emitter := &alerting.Emitter{Pool: pool, Window: 5 * time.Minute}
	handlers.NotificationObservedHook = func(ctx context.Context, n handlers.ObservedNotification) error {
		result, err := emitter.IngestNotification(ctx, alerting.Notification{
			Type: n.Type, NotificationID: n.NotificationID,
			Payload: n.Payload, OccurredAt: n.OccurredAt,
		})
		if err != nil {
			return err
		}
		tally.Add(result.EventsRecorded+result.EventsDeduplicated,
			result.EventsRecorded, result.EventsDeduplicated, result.DeliveriesEnqueued)
		return nil
	}
	t.Cleanup(func() { handlers.NotificationObservedHook = nil })

	healthy := &load.FlakyChannel{ChannelKind: channels.KindSlackWebhook}
	w.route(t, "StructureUnderAttack", "installation", "", healthy)

	// An unrecognised type — §3.2. Routed only after the first sighting
	// registers it, which is the real operator workflow.
	const unknownType = "GateThreeInventedTypeMsg"

	// The character the notifications belong to.
	_, err := pool.Exec(ctx, `INSERT INTO app.character (character_id, name, owner_hash) VALUES (2124613505, 'Gate3 Pilot', 'gate3-owner-hash')`)
	require.NoError(t, err)

	notifications := make([]handlers.CharacterNotificationDTO, 0, 12)
	for i := 0; i < 10; i++ {
		notifications = append(notifications, handlers.CharacterNotificationDTO{
			NotificationID: int64(9000 + i), SenderID: 1, SenderType: "corporation",
			Type: "StructureUnderAttack", Timestamp: time.Now(),
			Text: fmt.Sprintf("structureID: %d\nsolarsystemID: 30000142\n", 1000+i),
		})
	}
	// §3.3: CCP YAML that no strict parser accepts. It must import as
	// JSONB and never halt the queue.
	notifications = append(notifications, handlers.CharacterNotificationDTO{
		NotificationID: 9100, SenderID: 1, SenderType: "corporation",
		Type: "StructureUnderAttack", Timestamp: time.Now(),
		Text: "\tthis: is: not: valid: yaml: [\n",
	})
	notifications = append(notifications, handlers.CharacterNotificationDTO{
		NotificationID: 9200, SenderID: 1, SenderType: "corporation",
		Type: unknownType, Timestamp: time.Now(), Text: "someField: 1\n",
	})

	_, err = handlers.SyncCharacterNotifications(ctx, w.store, 2124613505, notifications)
	require.NoError(t, err, "§4.4: the queue must never halt on a bad payload")

	// The unknown type is now registered, so an operator can route it —
	// and the SECOND sighting delivers. Re-syncing the same notification
	// is also §3.1's suppressed_by_dedupe path, exercised for real.
	unknownStub := &load.FlakyChannel{ChannelKind: channels.KindDiscordWebhook}
	w.route(t, unknownType, "installation", "", unknownStub)
	_, err = handlers.SyncCharacterNotifications(ctx, w.store, 2124613505, notifications)
	require.NoError(t, err)

	// ── category 2: thresholds, through the REAL evaluator
	seedFuelThresholdWorld(t, w, 4)
	fuelStub := &load.FlakyChannel{ChannelKind: channels.KindSMTP}
	w.route(t, "corporation.structure.fuel_low", "corporation", fmt.Sprint(w.corporationID), fuelStub)

	evaluator := &alerting.Evaluator{
		Pool: pool, Emitter: emitter,
		Policy: alerting.ThresholdPolicy{StructureFuelWithin: 48 * time.Hour},
	}
	first, err := evaluator.Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, first.Subjects, "four structures are inside the 48h fuel margin")
	require.Equal(t, 4, first.Emitted)
	tally.Add(first.Emitted+first.Deduplicated, first.Emitted, first.Deduplicated, 0)

	// A second pass finds the same four structures still low. Their
	// re-arm token (fuel_expires) has not moved, so all four deduplicate:
	// the property that keeps a ten-minute evaluator from sending an alert
	// every ten minutes forever.
	second, err := evaluator.Evaluate(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, second.Emitted, "an unchanged threshold must not re-fire")
	require.Equal(t, 4, second.Deduplicated)
	tally.Add(second.Emitted+second.Deduplicated, second.Emitted, second.Deduplicated, 0)

	// ── category 3: a domain event, fingerprinted from payload content
	//
	// SemanticFields is the tool for an event with no natural upstream id
	// — there is no notification_id and no threshold subject to bucket on.
	domainStub := &load.FlakyChannel{ChannelKind: channels.KindSlackWebhook}
	w.route(t, "hangar.platform.esi_pin_advanced", "installation", "", domainStub)
	payload := json.RawMessage(`{"from":"2026-07-01","to":"2026-08-04","routes_unblocked":3}`)
	domainResult, err := emitter.Emit(ctx, alerting.EmitRequest{
		AlertType: "hangar.platform.esi_pin_advanced", Payload: payload, OccurredAt: time.Now(),
		Fingerprint: func(target alerting.Target) alerting.Fingerprint {
			fields := alerting.SemanticFields(payload, "from", "to")
			fields["target_kind"] = target.Kind
			fields["target_ref"] = target.Ref
			return alerting.Fingerprint{AlertType: "hangar.platform.esi_pin_advanced", Fields: fields}
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, domainResult.EventsRecorded)
	tally.Add(domainResult.EventsRecorded+domainResult.EventsDeduplicated,
		domainResult.EventsRecorded, domainResult.EventsDeduplicated, domainResult.DeliveriesEnqueued)

	// ── a channel that is down for the whole run — §3.3
	//
	// Its deliveries must exhaust their attempts and DEAD-LETTER, which is
	// a visible outcome and not a drop, and must not stop any other
	// channel's group from going out on the same pass.
	brokenStub := &load.FlakyChannel{ChannelKind: channels.KindSlackWebhook, FailAlways: true}
	w.route(t, "StructureLostShields", "installation", "", brokenStub)
	broken, err := emitter.Emit(ctx, alerting.EmitRequest{
		AlertType: "StructureLostShields", Payload: json.RawMessage(`{"structureID":4242}`),
		OccurredAt: time.Now(),
		Fingerprint: func(target alerting.Target) alerting.Fingerprint {
			return alerting.ThresholdFingerprint("StructureLostShields", "structure", 4242, "once", target)
		},
	})
	require.NoError(t, err)
	tally.Add(broken.EventsRecorded+broken.EventsDeduplicated,
		broken.EventsRecorded, broken.EventsDeduplicated, broken.DeliveriesEnqueued)

	// ── drain the pump
	//
	// The clock is advanced between passes rather than slept through: the
	// coalescing window has to close before a group is claimable, and the
	// retry backoff has to elapse before a failed delivery is tried again.
	// Sleeping for five real minutes to prove it would make this test
	// useless as a test.
	observer := newDeliveryOutcomeCounter()
	policy := alerting.RetryPolicy{MaxAttempts: 3, Base: time.Minute, Cap: 5 * time.Minute}
	clock := time.Now().Add(10 * time.Minute)
	for pass := 0; pass < 6; pass++ {
		// Make everything owed claimable BEFORE the pass, not after: the
		// first pass would otherwise find nothing, because a coalescing
		// group is not claimable until its window closes.
		advanceClaimableDeliveries(t, pool)
		now := clock
		d := w.dispatcher(policy, observer, func() time.Time { return now })
		_, err := d.Tick(ctx)
		require.NoError(t, err, "§4.4: a failing channel must never fail the pass")
		clock = clock.Add(10 * time.Minute)
	}

	// ── the measurement
	result, err := load.MeasureGate3(ctx, pool, load.Gate3Config{
		Since: since, MinAlerts: 10, MinCategories: 3,
		Notes: "harness self-test, not a Gate 3 run",
	}, tally)
	require.NoError(t, err)

	for _, c := range result.Conditions {
		t.Logf("%-20s %-5v %s — %s", c.ID, c.Passed, c.Description, c.Measurement)
	}
	require.True(t, result.Passed(), "the harness must pass over a run in which nothing went wrong")

	// The measurement's own claims, checked against what the run did.
	require.Equal(t, 3, result.Observed.Categories,
		"all three §4.4 categories must be represented — an esi_notification-only run proves nothing about the other two")
	require.Positive(t, result.Observed.MessagesSent)
	require.Positive(t, result.Observed.CoalescedInto,
		"ten StructureUnderAttack notifications in one window must roll up, or §3.1's third term is unmeasurable")
	require.Positive(t, result.Observed.DeadLettered, "the permanently-down channel must dead-letter")
	require.Zero(t, result.Observed.Pending, "§3.1: nothing may be left neither delivered nor dead-lettered")
	require.Positive(t, result.Observed.UnknownTypesBoarded)

	// §3.4, which no query can see: the coalesced group arrived as ONE
	// message carrying every event.
	accepted := healthy.Accepted()
	require.Len(t, accepted, 1, "ten coalesced notifications must arrive as one message")
	require.Equal(t, 11, accepted[0].Count,
		"the roll-up must carry every event in the window, including the unparseable-YAML one")

	// The METRIC agrees with the TABLE. Reported as a cross-check, never
	// as the verdict — see gate3_alerts.go's header.
	require.Equal(t, result.Observed.DeadLettered, observer.byOutcome[alerting.OutcomeDeadLettered],
		"alert_delivery_total{outcome=\"dead_lettered\"} must agree with app.alert_delivery")
}

// TestGate3EmptyRunDoesNotPass is the condition B25 makes non-negotiable: a
// run that generated nothing must FAIL with a reason, never pass because
// it found no drops.
func TestGate3EmptyRunDoesNotPass(t *testing.T) {
	pool := newGate3Pool(t)
	ctx := context.Background()

	result, err := load.MeasureGate3(ctx, pool, load.Gate3Config{
		Since: time.Now().Add(-time.Hour), MinAlerts: 10, MinCategories: 3,
	}, load.Gate3Tally{})
	require.NoError(t, err)

	require.False(t, result.Passed(),
		"an empty run must not pass: \"zero alerts dropped\" is true of a pipeline with no producer, "+
			"which is exactly what defect B25 was")
	require.Len(t, result.Conditions, 1, "evaluation must stop at the sample gate rather than reporting vacuous passes")
	require.Equal(t, "3.1-sample", result.Conditions[0].ID)
	require.False(t, result.Conditions[0].Passed)
	require.Contains(t, result.Conditions[0].Measurement, "0 occurrences offered")
}

// TestGate3DetectsAnUndeliveredAlert proves the harness can FAIL. A gate
// that has only ever been seen to pass is not evidence of anything: this
// leaves a delivery pending — §3.1's exact definition of a drop — and
// requires the measurement to say so.
func TestGate3DetectsAnUndeliveredAlert(t *testing.T) {
	pool := newGate3Pool(t)
	ctx := context.Background()
	since := time.Now().Add(-time.Minute)

	w := newGate3World(t, pool)
	emitter := &alerting.Emitter{Pool: pool, Window: -1} // coalescing off: one event, one message
	stub := &load.FlakyChannel{ChannelKind: channels.KindSlackWebhook}
	w.route(t, "StructureDestroyed", "installation", "", stub)

	emitted, err := emitter.Emit(ctx, alerting.EmitRequest{
		AlertType: "StructureDestroyed", Payload: json.RawMessage(`{"structureID":77}`), OccurredAt: time.Now(),
		Fingerprint: func(target alerting.Target) alerting.Fingerprint {
			return alerting.ThresholdFingerprint("StructureDestroyed", "structure", 77, "once", target)
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, emitted.DeliveriesEnqueued)

	// The pump never runs. The delivery sits pending — generated, and
	// neither delivered nor dead-lettered.
	var tally load.Gate3Tally
	tally.Add(emitted.EventsRecorded, emitted.EventsRecorded, 0, emitted.DeliveriesEnqueued)

	result, err := load.MeasureGate3(ctx, pool, load.Gate3Config{
		Since: since, MinAlerts: 1, MinCategories: 1,
	}, tally)
	require.NoError(t, err)
	require.False(t, result.Passed(), "a delivery left pending IS §3.1's drop and must fail the gate")

	var dropped load.ConditionResult
	for _, c := range result.Conditions {
		if c.ID == "3.1-dropped" {
			dropped = c
		}
	}
	require.Equal(t, "3.1-dropped", dropped.ID, "the drop condition must be evaluated and reported")
	require.False(t, dropped.Passed)
	require.Contains(t, dropped.Measurement, "1 still pending")
}

// advanceClaimableDeliveries drags every pending delivery's next_attempt_at
// into the past, standing in for the wall-clock passage the coalescing
// window and the retry backoff would otherwise need — five minutes and
// several exponential retries of real sleeping, to prove something about
// arithmetic.
//
// It uses the DATABASE's clock deliberately. ClaimPendingAlertDeliveries
// compares against now() inside Postgres, so a Dispatcher.Now that has been
// wound forward changes the RETRY DECISIONS but not what is claimable —
// which is a distinction worth having in the pump (the database is the
// shared clock across replicas, per §5.6) and worth respecting here.
//
// It touches ONLY next_attempt_at. The delivery's state, attempts and
// error are the pump's to own, and rewriting any of them here would be the
// test arranging the outcome it then asserts.
func advanceClaimableDeliveries(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE app.alert_delivery SET next_attempt_at = now() - interval '1 second'
		  WHERE state = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at > now())`)
	require.NoError(t, err)
}
