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
INSERT INTO app.alert_delivery (event_id, channel_id)
VALUES ($1, $2)
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
