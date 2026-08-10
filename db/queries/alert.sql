-- app.alert_type, app.alert_channel, app.alert_routing_rule, app.alert_event,
-- app.alert_delivery, app.notification_unknown_type
-- (02_DATABASE_SCHEMA.md §4.5 #38-#43).

-- name: ListAlertTypes :many
SELECT * FROM app.alert_type ORDER BY domain, alert_type;

-- name: GetAlertType :one
SELECT * FROM app.alert_type WHERE alert_type = $1;

-- name: CountAlertTypesByDomain :many
SELECT domain, count(*) AS n FROM app.alert_type GROUP BY domain ORDER BY domain;

-- name: CreateAlertChannel :one
INSERT INTO app.alert_channel (kind, name, config)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListAlertChannels :many
SELECT * FROM app.alert_channel WHERE enabled ORDER BY name;

-- name: CreateAlertRoutingRule :one
INSERT INTO app.alert_routing_rule (alert_type, target_kind, target_ref, channel_id, mention)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAlertRoutingRulesForType :many
SELECT * FROM app.alert_routing_rule WHERE alert_type = $1 AND enabled;

-- name: RecordAlertEvent :one
-- dedupe_hash carries the natural key; ON CONFLICT makes re-delivery of an
-- already-seen CCP notification a no-op rather than a duplicate event.
INSERT INTO app.alert_event (alert_type, dedupe_hash, coalesce_key, payload)
VALUES ($1, $2, $3, $4)
ON CONFLICT (dedupe_hash) DO NOTHING
RETURNING *;

-- name: ListRecentAlertEvents :many
SELECT * FROM app.alert_event
 WHERE alert_type = $1
 ORDER BY occurred_at DESC
 LIMIT sqlc.arg(page_size);

-- name: EnqueueAlertDelivery :one
-- PHASE 14: gained a third parameter. next_attempt_at is the moment the
-- delivery becomes claimable, and for a coalesced event that is the close
-- of its coalescing window — so the forty deliveries of a forty-event
-- burst all become eligible at the same instant and one claim picks up the
-- whole group. Setting it here rather than deferring each delivery on
-- first claim keeps attempts (and therefore the dead-letter budget)
-- untouched by coalescing: waiting for a window is not a failed attempt.
-- NULL means "claimable immediately", which is what an uncoalesced event
-- passes and what the column defaulted to before.
INSERT INTO app.alert_delivery (event_id, channel_id, next_attempt_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ClaimPendingAlertDeliveries :many
SELECT * FROM app.alert_delivery
 WHERE state = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= now())
 ORDER BY created_at
 LIMIT sqlc.arg(claim_size);

-- name: MarkAlertDeliverySent :exec
UPDATE app.alert_delivery
   SET state = 'sent', attempts = attempts + 1, last_attempt_at = now()
 WHERE delivery_id = $1;

-- name: MarkAlertDeliveryFailed :exec
UPDATE app.alert_delivery
   SET state = $2, attempts = attempts + 1, last_attempt_at = now(),
       next_attempt_at = $3, error = $4
 WHERE delivery_id = $1;

-- name: RecordUnknownNotificationType :exec
INSERT INTO app.notification_unknown_type (type, sample_payload)
VALUES ($1, $2)
ON CONFLICT (type) DO UPDATE
   SET last_seen_at = now(), occurrences = app.notification_unknown_type.occurrences + 1;

-- name: ListUnacknowledgedNotificationTypes :many
SELECT * FROM app.notification_unknown_type WHERE acknowledged_at IS NULL ORDER BY first_seen_at;

-- name: AcknowledgeNotificationType :exec
UPDATE app.notification_unknown_type SET acknowledged_at = now() WHERE type = $1;

-- ── PHASE 14 ADDITIONS ──────────────────────────────────────────────────
-- Everything above already existed (Phase 1a) and is reused as-is: the
-- outbox is app.alert_delivery + ClaimPendingAlertDeliveries, the dedupe
-- primitive is RecordAlertEvent's `ON CONFLICT (dedupe_hash) DO NOTHING`,
-- and the unknown-types board is RecordUnknownNotificationType. The
-- queries below are the ones that had no Phase 1a consumer to justify
-- them: reading back a claimed delivery's event/channel, the coalescing
-- window's member events, the admin-visible dead-letter queue, and the
-- open-vocabulary insert that lets a CCP type nobody has ever seen satisfy
-- app.alert_event's foreign key instead of halting the queue.

-- name: EnsureAlertType :exec
-- Principle 14 applied to app.alert_type: a CCP notification type this
-- build's catalogue does not know is REGISTERED, never rejected. Without a
-- row here, app.alert_event.alert_type's foreign key would reject the
-- event and the unrecognised notification would halt the queue — the exact
-- failure §4.4 forbids. DO NOTHING (not DO UPDATE) so a runtime discovery
-- can never overwrite a seeded row's domain/category/default_enabled.
INSERT INTO app.alert_type (alert_type, domain, category, default_enabled)
VALUES ($1, $2, $3, false)
ON CONFLICT (alert_type) DO NOTHING;

-- name: GetAlertEvent :one
SELECT * FROM app.alert_event WHERE event_id = $1;

-- name: GetAlertChannel :one
SELECT * FROM app.alert_channel WHERE channel_id = $1;

-- name: GetAlertChannelByName :one
-- Used at worker boot to find the env-configured "default-*" channels
-- without creating a second one on every restart. app.alert_channel.name
-- has no UNIQUE constraint (an installation may legitimately want two
-- channels with the same label), so this is a lookup-then-insert rather
-- than an upsert; the boot path is single-writer and idempotent.
SELECT * FROM app.alert_channel WHERE name = $1;

-- name: ListAlertEventsForCoalesceKeySince :many
-- The coalescing window's members: every event sharing one
-- (routing target, alert type) key since the window opened. Ordered
-- oldest-first so a roll-up reads chronologically and its "and N more"
-- remainder always truncates the TAIL, never the first thing that
-- happened.
SELECT * FROM app.alert_event
 WHERE coalesce_key = $1
   AND occurred_at >= sqlc.arg(since)
 ORDER BY occurred_at;

-- name: ListDeadLetterAlertDeliveries :many
-- The admin-visible dead-letter queue (§4.4: "an alert is lost only if it
-- was neither delivered nor dead-lettered; dead-lettering is a visible
-- outcome, not a loss"). Joined to the event and channel so the board can
-- name what failed and where without an N+1 read per row.
SELECT d.delivery_id, d.event_id, d.channel_id, d.attempts, d.last_attempt_at,
       d.error, d.created_at,
       e.alert_type, e.payload, e.occurred_at,
       c.kind AS channel_kind, c.name AS channel_name
  FROM app.alert_delivery d
  JOIN app.alert_event   e ON e.event_id   = d.event_id
  JOIN app.alert_channel c ON c.channel_id = d.channel_id
 WHERE d.state = 'dead_letter'
 ORDER BY d.last_attempt_at DESC NULLS LAST
 LIMIT sqlc.arg(page_size);

-- name: CountDeadLetterAlertDeliveries :one
SELECT count(*) FROM app.alert_delivery WHERE state = 'dead_letter';

-- name: RequeueDeadLetterAlertDelivery :exec
-- The administrator's escape hatch once the cause is fixed (SMTP host back
-- up, webhook URL corrected). attempts is reset so the requeued delivery
-- gets a full retry budget rather than dead-lettering again on its first
-- attempt; the previous error text is kept until the next attempt
-- overwrites it, so the board's history is not erased by the retry.
UPDATE app.alert_delivery
   SET state = 'pending', attempts = 0, next_attempt_at = NULL
 WHERE delivery_id = $1 AND state = 'dead_letter';
