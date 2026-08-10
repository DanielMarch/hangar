//go:build integration

package alerting_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/alerting/render"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gopkg.in/yaml.v3"
)

const notificationsFixtureDir = "../../testdata/notifications"

// newMigratedPool boots a real, migrated PG18 via testcontainers and
// applies db/seed — internal/provisioning's established pattern. The seed
// matters here specifically: db/seed/alert_types.sql is what populates
// app.alert_type, and app.alert_event has a foreign key to it.
func newMigratedPool(t testing.TB) *pgxpool.Pool {
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

	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

// recordingChannel captures what the pump sent instead of talking to
// Slack. failWith, when non-nil, is returned from every Send — the way
// TestDeadLetterAfterMaxAttempts simulates a channel that is down.
type recordingChannel struct {
	kind     string
	failWith error

	mu       sync.Mutex
	messages []channels.Message
	attempts int
}

func (c *recordingChannel) Kind() string { return c.kind }

func (c *recordingChannel) Send(_ context.Context, msg channels.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	if c.failWith != nil {
		return c.failWith
	}
	c.messages = append(c.messages, msg)
	return nil
}

func (c *recordingChannel) sent() []channels.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]channels.Message(nil), c.messages...)
}

func (c *recordingChannel) sendAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}

func seedChannelAndRule(t *testing.T, s *store.Store, alertType, targetKind, targetRef string) (gen.AppAlertChannel, gen.AppAlertRoutingRule) {
	t.Helper()
	ctx := context.Background()

	channel, err := s.CreateAlertChannel(ctx, channels.KindSlackWebhook, "test-"+uuid.NewString(), json.RawMessage(`{"url":"https://example.invalid/hook"}`))
	require.NoError(t, err)

	ref := targetRef
	rule, err := s.CreateAlertRoutingRule(ctx, gen.CreateAlertRoutingRuleParams{
		AlertType: alertType, TargetKind: targetKind, TargetRef: &ref, ChannelID: channel.ChannelID,
	})
	require.NoError(t, err)
	return channel, rule
}

// fixturePayload reads a testdata/notifications/*.yaml fixture and returns
// it in the exact shape app.character_notification.payload holds — i.e.
// what internal/sync/handlers produced from CCP's YAML `text` field.
func fixturePayload(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(notificationsFixtureDir, name))
	require.NoError(t, err)

	var decoded any
	require.NoError(t, yaml.Unmarshal(raw, &decoded))
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	return encoded
}

func deliveryStates(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT state, count(*) FROM app.alert_delivery GROUP BY state`)
	require.NoError(t, err)
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		require.NoError(t, rows.Scan(&state, &n))
		out[state] = n
	}
	require.NoError(t, rows.Err())
	return out
}

// TestFortyEventsCoalesceToOneMessage is Phase 14's named exit criterion:
// "one message, correct roll-up, within channel size limits".
func TestFortyEventsCoalesceToOneMessage(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const alertType = "StructureUnderAttack"
	seedChannelAndRule(t, s, alertType, "squad", "42")

	// The burst happened ten minutes ago, so its five-minute coalescing
	// window has already closed and every delivery is claimable now.
	//
	// Anchored to a window BOUNDARY on purpose. An unanchored "now minus
	// ten minutes" makes this test flaky roughly two times in five: the
	// forty events span two minutes, and if that span happens to straddle
	// a bucket edge the fixed window correctly produces TWO groups. That
	// is the documented cost of a fixed window (see coalesce.go), and this
	// test asserts the in-window behaviour, so it must place the burst
	// inside one window rather than leaving it to the wall clock.
	window := alerting.DefaultCoalesceWindow
	burstAt := time.Now().Add(-10 * time.Minute).Truncate(window)
	emitter := &alerting.Emitter{Pool: pool, Window: window}

	payload := fixturePayload(t, "valid_structure_under_attack.yaml")
	for i := 0; i < 40; i++ {
		result, err := emitter.IngestNotification(ctx, alerting.Notification{
			Type:           alertType,
			NotificationID: int64(2000000000 + i),
			Payload:        payload,
			// Spread across the window, but all inside it.
			OccurredAt: burstAt.Add(time.Duration(i) * 3 * time.Second),
		})
		require.NoError(t, err)
		require.True(t, result.Known)
		require.True(t, result.Routed)
		require.Equal(t, 1, result.EventsRecorded)
		require.Equal(t, 1, result.DeliveriesEnqueued)
	}

	var events, deliveries int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.alert_event`).Scan(&events))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.alert_delivery`).Scan(&deliveries))
	require.Equal(t, 40, events, "forty distinct notifications are forty events")
	require.Equal(t, 40, deliveries)

	// Every delivery shares one coalescing key — that is what makes them
	// one message rather than forty.
	var distinctKeys int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(DISTINCT coalesce_key) FROM app.alert_event`).Scan(&distinctKeys))
	require.Equal(t, 1, distinctKeys, "all forty events must share one (target, alert type, window) key")

	recorder := &recordingChannel{kind: channels.KindSlackWebhook}
	dispatcher := &alerting.Dispatcher{
		Pool:     pool,
		Channels: func(gen.AppAlertChannel) (channels.Channel, error) { return recorder, nil },
	}

	result, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 40, result.Claimed)
	require.Equal(t, 1, result.Groups, "forty claimed deliveries must collapse into ONE group")
	require.Equal(t, 40, result.Sent, "every delivery in the roll-up is settled, not just the first")

	sent := recorder.sent()
	require.Len(t, sent, 1, "§4.4: forty events inside the window render as one message")
	msg := sent[0]
	require.Equal(t, 40, msg.Count)
	require.Len(t, msg.Lines, 40, "the roll-up must carry every event; truncation is the channel's job, not the pump's")
	require.Contains(t, msg.Header, "40 events")
	require.Contains(t, msg.Subject, "Structure under attack", "the subject uses the catalogue's human-readable summary")

	// Within channel size limits — checked against each channel's REAL
	// limit. Whether a given roll-up actually needs truncating depends on
	// the payload, so the assertion is conditional on the untruncated
	// length: over the limit MUST truncate with §4.4's explicit remainder
	// count; under it MUST carry every event. Asserting truncation
	// unconditionally would make this test a hostage to how long a
	// fixture's rendered line happens to be.
	//
	// With this fixture the two channels genuinely differ, which is the
	// point of §4.4's "different size limits": the same forty events fit
	// Slack's 3,000 and do not fit Discord's 2,000.
	full := render.Rollup(msg.Header, msg.Lines, channels.SMTPBodyLimit)
	require.NotContains(t, full, "… and ", "the SMTP body must carry all forty events — nothing is lost, only abbreviated where a platform demands it")

	for name, limit := range map[string]int{
		"slack":   channels.SlackSectionTextLimit,
		"discord": channels.DiscordContentLimit,
	} {
		body := render.Rollup(msg.Header, msg.Lines, limit)
		require.LessOrEqual(t, utf8.RuneCountInString(body), limit, "%s body must fit its limit", name)
		if utf8.RuneCountInString(full) > limit {
			require.Contains(t, body, "… and ", "%s body exceeded its limit and must say what it truncated", name)
			require.Contains(t, body, "more")
		} else {
			require.Equal(t, full, body, "%s body fits, so nothing may be dropped", name)
		}
	}
	require.Greater(t, utf8.RuneCountInString(full), channels.DiscordContentLimit,
		"the premise of this assertion: forty events do not fit Discord's 2,000-character limit")

	// Large EVE ids must stay readable — a structure id rendered as
	// 1.021975179626e+12 is not something an operator can paste into the
	// game client (see render.compactValue).
	require.Contains(t, msg.Lines[0], "1021975179626")
	require.NotContains(t, msg.Lines[0], "e+12")

	require.Equal(t, map[string]int{alerting.StateSent: 40}, deliveryStates(t, pool))

	// A second pass has nothing to do — the group is settled, not stuck.
	again, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, again.Claimed)
	require.Len(t, recorder.sent(), 1)
}

// TestUnrecognisedTypeUsesGenericRenderer is Phase 14's named exit
// criterion: "delivers, does not dead-letter, appears on the unknown-types
// board".
//
// It walks the REAL operator workflow, which is necessarily two-step: a
// type nobody has ever seen cannot already have a routing rule (the rule
// table has a foreign key to app.alert_type), so the first sighting
// registers and boards it and the sighting after an operator routes it
// delivers. Both halves are asserted, and the queue never halts in either.
func TestUnrecognisedTypeUsesGenericRenderer(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const unknownType = "SomeTypeCCPInventedLastTuesday"
	_, inCatalogue := catalogue.ByName(unknownType)
	require.False(t, inCatalogue, "the premise: this type is not in the build's catalogue")
	require.False(t, render.HasTemplate(unknownType), "and it has no template")

	// The payload arrives through the real ingest path: CCP's YAML `text`
	// field, parsed by internal/sync/handlers into the jsonb column shape
	// migration 00035 defined.
	const characterID int64 = 90000501
	_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{
		CharacterID: characterID, Name: "Alert Test Pilot", OwnerHash: "owner-" + uuid.NewString(),
	})
	require.NoError(t, err)

	fixtureText, err := os.ReadFile(filepath.Join(notificationsFixtureDir, "unrecognised_shape.yaml"))
	require.NoError(t, err)

	_, err = handlers.SyncCharacterNotifications(ctx, s, characterID, []handlers.CharacterNotificationDTO{{
		NotificationID: 3000000001, SenderID: 98000001, SenderType: "corporation",
		Type: unknownType, Text: string(fixtureText), Timestamp: time.Now().Add(-time.Hour),
	}})
	require.NoError(t, err, "an unrecognised type must not fail the sync either")

	rows, err := s.ListCharacterNotificationsPage(ctx, characterID, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	stored := rows[0]
	require.False(t, stored.ParseFailed, "this fixture is valid YAML; the fallback under test is the TYPE, not the parse")
	require.NotEmpty(t, stored.Payload)

	emitter := &alerting.Emitter{Pool: pool}

	// ── First sighting: register + board, nothing to deliver yet ────────
	first, err := emitter.IngestNotification(ctx, alerting.Notification{
		Type: unknownType, NotificationID: 3000000001, Payload: stored.Payload, OccurredAt: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err, "an unrecognised type must never halt the queue")
	require.False(t, first.Known)
	require.True(t, first.OnUnknownBoard)
	require.False(t, first.Routed, "nobody can have routed a type that had never been seen")

	board, err := s.ListUnacknowledgedNotificationTypes(ctx)
	require.NoError(t, err)
	var onBoard bool
	for _, entry := range board {
		if entry.Type == unknownType {
			onBoard = true
			require.NotNil(t, entry.SamplePayload, "the board must carry a sample so an operator can see the shape")
		}
	}
	require.True(t, onBoard, "§4.4: the type must appear on the unknown-types board")

	// It was registered as a first-class, routable alert type — without
	// this row, app.alert_event's foreign key would reject the event and
	// the queue WOULD halt.
	registered, err := s.GetAlertType(ctx, unknownType)
	require.NoError(t, err)
	require.Equal(t, string(catalogue.DomainUnknown), registered.Domain)
	require.False(t, registered.DefaultEnabled, "a runtime-discovered type must not switch itself on")

	// ── Operator routes it, second sighting delivers ────────────────────
	seedChannelAndRule(t, s, unknownType, "installation", "")

	second, err := emitter.IngestNotification(ctx, alerting.Notification{
		Type: unknownType, NotificationID: 3000000002, Payload: stored.Payload, OccurredAt: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)
	require.True(t, second.Routed)
	require.Equal(t, 1, second.EventsRecorded)
	require.Equal(t, 1, second.DeliveriesEnqueued)

	recorder := &recordingChannel{kind: channels.KindSlackWebhook}
	dispatcher := &alerting.Dispatcher{
		Pool:     pool,
		Channels: func(gen.AppAlertChannel) (channels.Channel, error) { return recorder, nil },
	}
	tick, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tick.Sent, "it DELIVERS")
	require.Zero(t, tick.DeadLettered, "and it never dead-letters")

	sent := recorder.sent()
	require.Len(t, sent, 1)
	body := sent[0].Lines[0]
	// The generic key/value renderer produced this, not a template.
	require.Contains(t, body, "mysteriousField: 42")
	require.Contains(t, body, "whoKnows: a value CCP invented after this build shipped")
	require.Contains(t, body, "innerA: alpha", "nested fields must render too")

	require.Equal(t, map[string]int{alerting.StateSent: 1}, deliveryStates(t, pool))

	// Dedupe still applies to an unknown type: re-reading the same
	// notification on the next poll must not deliver it twice.
	repeat, err := emitter.IngestNotification(ctx, alerting.Notification{
		Type: unknownType, NotificationID: 3000000002, Payload: stored.Payload, OccurredAt: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)
	require.Equal(t, 0, repeat.EventsRecorded)
	require.Equal(t, 1, repeat.EventsDeduplicated)
	require.Equal(t, 0, repeat.DeliveriesEnqueued)
}

// TestDeadLetterAfterMaxAttempts is Phase 14's named exit criterion:
// "exhausted deliveries land on the admin-visible dead-letter queue".
//
// It also proves the other half of §4.4's guarantee — that a failing
// channel never blocks the queue: a healthy channel's delivery, enqueued
// behind the failing one, goes out on the very first pass.
func TestDeadLetterAfterMaxAttempts(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const alertType = "StructureFuelAlert"
	brokenChannel, _ := seedChannelAndRule(t, s, alertType, "squad", "7")
	seedChannelAndRule(t, s, alertType, "squad", "7") // a second, healthy channel for the same target

	emitter := &alerting.Emitter{Pool: pool, Window: -1} // coalescing off: one event, one message
	_, err := emitter.IngestNotification(ctx, alerting.Notification{
		Type: alertType, NotificationID: 4000000001,
		Payload: fixturePayload(t, "structure_fuel_alert.yaml"), OccurredAt: time.Now().Add(-time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, map[string]int{alerting.StatePending: 2}, deliveryStates(t, pool))

	broken := &recordingChannel{kind: channels.KindSlackWebhook, failWith: errors.New("dial tcp: connect: connection refused")}
	healthy := &recordingChannel{kind: channels.KindSlackWebhook}

	policy := alerting.RetryPolicy{MaxAttempts: 3, Base: time.Minute, Cap: time.Hour}
	now := time.Now()
	dispatcher := &alerting.Dispatcher{
		Pool:   pool,
		Policy: policy,
		Now:    func() time.Time { return now },
		Channels: func(row gen.AppAlertChannel) (channels.Channel, error) {
			if row.ChannelID == brokenChannel.ChannelID {
				return broken, nil
			}
			return healthy, nil
		},
	}

	// Pass 1: the healthy channel delivers immediately — a broken sibling
	// must not hold it up.
	first, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, first.Claimed)
	require.Equal(t, 1, first.Sent)
	require.Equal(t, 1, first.Retried)
	require.Zero(t, first.DeadLettered)
	require.Len(t, healthy.sent(), 1, "a failing channel must never block the queue")

	// The failed delivery is scheduled, not lost: still pending, with a
	// future attempt and a stored reason.
	var state string
	var attempts int
	var nextAttempt *time.Time
	var errText *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state, attempts, next_attempt_at, error FROM app.alert_delivery WHERE channel_id = $1`,
		brokenChannel.ChannelID).Scan(&state, &attempts, &nextAttempt, &errText))
	require.Equal(t, alerting.StatePending, state)
	require.Equal(t, 1, attempts)
	require.NotNil(t, nextAttempt)
	require.True(t, nextAttempt.After(now), "the retry must be scheduled into the future, not immediately")
	require.NotNil(t, errText)
	require.Contains(t, *errText, "connection refused")

	// Passes 2 and 3. The backoff schedule is real, so the row is not
	// claimable until its next_attempt_at passes — the test moves the
	// clock rather than sleeping through it.
	for attempt := 2; attempt <= policy.MaxAttempts; attempt++ {
		_, err := pool.Exec(ctx, `UPDATE app.alert_delivery SET next_attempt_at = now() - interval '1 second' WHERE state = 'pending'`)
		require.NoError(t, err)

		tick, err := dispatcher.Tick(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, tick.Claimed)
		if attempt < policy.MaxAttempts {
			require.Equal(t, 1, tick.Retried, "attempt %d must retry", attempt)
			require.Zero(t, tick.DeadLettered)
		} else {
			require.Equal(t, 1, tick.DeadLettered, "the final attempt must dead-letter")
		}
	}

	require.Equal(t, policy.MaxAttempts, broken.sendAttempts(), "the channel must have been tried exactly MaxAttempts times")
	require.Equal(t, map[string]int{alerting.StateSent: 1, alerting.StateDeadLetter: 1}, deliveryStates(t, pool))

	// §4.4: dead-lettering is a VISIBLE outcome. The admin board must show
	// it, with enough context to act on.
	count, err := alerting.DeadLetterCount(ctx, s)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	board, err := alerting.DeadLetterBoard(ctx, s, 50)
	require.NoError(t, err)
	require.Len(t, board, 1)
	require.Equal(t, alertType, board[0].AlertType)
	require.Equal(t, brokenChannel.ChannelID, board[0].ChannelID)
	require.Equal(t, int32(policy.MaxAttempts), board[0].Attempts)
	require.NotNil(t, board[0].Error)
	require.Contains(t, *board[0].Error, "exhausted 3 attempts")
	require.NotEmpty(t, board[0].Payload, "the board must carry the payload so the alert can still be read")

	// A dead-lettered delivery is NOT claimed again — it is out of the
	// queue, not silently retried forever.
	idle, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, idle.Claimed)

	// The operator fixes the channel and requeues it: a full budget again.
	require.NoError(t, alerting.Requeue(ctx, s, board[0].DeliveryID))
	dispatcher.Channels = func(gen.AppAlertChannel) (channels.Channel, error) { return healthy, nil }
	recovered, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered.Sent)
	require.Equal(t, map[string]int{alerting.StateSent: 2}, deliveryStates(t, pool))
}

// TestPermanentFailureDeadLettersImmediately covers the other route onto
// the board: a webhook that no longer exists is not worth five attempts.
func TestPermanentFailureDeadLettersImmediately(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const alertType = "SkyhookUnderAttack"
	seedChannelAndRule(t, s, alertType, "corporation", "98000001")

	emitter := &alerting.Emitter{Pool: pool, Window: -1}
	_, err := emitter.IngestNotification(ctx, alerting.Notification{
		Type: alertType, NotificationID: 5000000001,
		Payload: fixturePayload(t, "skyhook_under_attack.yaml"), OccurredAt: time.Now().Add(-time.Minute),
	})
	require.NoError(t, err)

	gone := &recordingChannel{kind: channels.KindDiscordWebhook, failWith: &channels.PermanentError{
		Reason: "discord: webhook returned 404: unknown webhook",
	}}
	dispatcher := &alerting.Dispatcher{
		Pool:     pool,
		Channels: func(gen.AppAlertChannel) (channels.Channel, error) { return gone, nil },
	}

	tick, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tick.DeadLettered)
	require.Equal(t, 1, gone.sendAttempts(), "a permanent failure must not be retried")
	require.Equal(t, map[string]int{alerting.StateDeadLetter: 1}, deliveryStates(t, pool))
}

// TestDisabledChannelDeadLettersRatherThanStalling pins the third way a
// delivery can end up on the board — and rules out the two worse
// alternatives (pending forever, or quietly marked sent).
func TestDisabledChannelDeadLettersRatherThanStalling(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const alertType = "WarDeclared"
	channel, _ := seedChannelAndRule(t, s, alertType, "alliance", "99000001")

	emitter := &alerting.Emitter{Pool: pool, Window: -1}
	_, err := emitter.IngestNotification(ctx, alerting.Notification{
		Type: alertType, NotificationID: 6000000001,
		Payload: fixturePayload(t, "war_declared.yaml"), OccurredAt: time.Now().Add(-time.Minute),
	})
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `UPDATE app.alert_channel SET enabled = false WHERE channel_id = $1`, channel.ChannelID)
	require.NoError(t, err)

	unused := &recordingChannel{kind: channels.KindSlackWebhook}
	dispatcher := &alerting.Dispatcher{
		Pool:     pool,
		Channels: func(gen.AppAlertChannel) (channels.Channel, error) { return unused, nil },
	}
	tick, err := dispatcher.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tick.DeadLettered)
	require.Zero(t, unused.sendAttempts(), "a disabled channel must not be contacted")

	board, err := alerting.DeadLetterBoard(ctx, s, 10)
	require.NoError(t, err)
	require.Len(t, board, 1)
	require.Contains(t, *board[0].Error, "disabled")
}

// TestSeededCatalogueMatchesTheGoCatalogue closes the loop between the
// build-time assertions (which read the Go catalogue and the seed FILE)
// and a live database: after migrate + seed, app.alert_type holds exactly
// the catalogue's non-threshold rows.
//
// The four threshold rows are absent here BY DESIGN and the assertion says
// so: they are inserted through a JOIN against app.esi_route, which no
// test in this package ingests. See db/seed/alert_types.sql's Phase 14
// header — a fabricated route_id would defeat the very FK the build-time
// check relies on.
func TestSeededCatalogueMatchesTheGoCatalogue(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	rows, err := s.ListAlertTypes(ctx)
	require.NoError(t, err)

	seeded := make(map[string]gen.AppAlertType, len(rows))
	for _, row := range rows {
		seeded[row.AlertType] = row
	}

	var wantNonThreshold, wantThreshold int
	for _, entry := range catalogue.Catalogue {
		if entry.Category == catalogue.CategoryThreshold {
			wantThreshold++
			require.NotContains(t, seeded, entry.Name,
				"threshold row %q must wait for a real app.esi_route row", entry.Name)
			continue
		}
		wantNonThreshold++
		row, ok := seeded[entry.Name]
		require.True(t, ok, "alert type %q must be seeded", entry.Name)
		require.Equal(t, string(entry.Domain), row.Domain)
		require.Equal(t, string(entry.Category), row.Category)
		require.Equal(t, entry.DefaultEnabled, row.DefaultEnabled)
		require.False(t, row.SourceRouteID.Valid, "only threshold alerts carry a source route")
	}
	require.Len(t, rows, wantNonThreshold)
	require.Equal(t, 4, wantThreshold, "the catalogue declares four threshold alerts")

	// Per-domain counts, live: what the build-time test asserts over the
	// Go catalogue must also hold against a real seeded database, minus
	// the deferred threshold rows.
	counts, err := s.CountAlertTypesByDomain(ctx)
	require.NoError(t, err)
	live := map[string]int{}
	for _, row := range counts {
		live[row.Domain] = int(row.N)
	}
	require.Equal(t, catalogue.ExpectedCounts[catalogue.DomainPlatform], live["platform"])
	require.Equal(t, catalogue.ExpectedCounts[catalogue.DomainWars], live["wars"])
	require.Equal(t, catalogue.ExpectedCounts[catalogue.DomainSovereignty], live["sovereignty"])
	require.Equal(t, catalogue.ExpectedCounts[catalogue.DomainStructures]-2, live["structures"], "less the two deferred fuel thresholds")
	require.Equal(t, catalogue.ExpectedCounts[catalogue.DomainCorporations]-1, live["corporations"], "less the deferred extraction threshold")

	// Re-applying the seed is idempotent (ApplySeeds runs on every
	// `hangar migrate up`).
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	after, err := s.ListAlertTypes(ctx)
	require.NoError(t, err)
	require.Len(t, after, len(rows))
}

// TestThresholdSeedResolvesRouteIdsOnceIngested proves the deferred half
// works: with the four source routes present in app.esi_route, a re-run of
// the seed completes the catalogue and every threshold row points at a
// real route.
func TestThresholdSeedResolvesRouteIdsOnceIngested(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	for i, path := range catalogue.ThresholdSourceRoutes() {
		_, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
			OperationID: fmt.Sprintf("TestOperation%d", i), Method: "GET", UpstreamPath: path,
			CompatibilityDate: pgtype.Date{Time: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), Valid: true},
			SpecFragment:      json.RawMessage(`{}`), IdentifierTypes: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	require.NoError(t, hangardb.ApplySeeds(ctx, pool))

	for _, threshold := range catalogue.Thresholds() {
		row, err := s.GetAlertType(ctx, threshold.Name)
		require.NoError(t, err, "threshold %q must be seeded once its source route exists", threshold.Name)
		require.Equal(t, string(catalogue.CategoryThreshold), row.Category)
		require.True(t, row.SourceRouteID.Valid, "§4.4: every threshold alert declares its source route")

		route, err := s.GetEsiRouteByID(ctx, row.SourceRouteID.UUID)
		require.NoError(t, err)
		require.Equal(t, threshold.SourceRoute, route.UpstreamPath)
	}

	rows, err := s.ListAlertTypes(ctx)
	require.NoError(t, err)
	require.Len(t, rows, len(catalogue.Catalogue),
		"with routes ingested, the live catalogue is complete — all %d rows", len(catalogue.Catalogue))
}
