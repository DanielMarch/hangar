-- app.webhook_endpoint, app.outbox_event, app.webhook_delivery
-- (02_DATABASE_SCHEMA.md §4.6 #44-#46).

-- name: CreateWebhookEndpoint :one
INSERT INTO app.webhook_endpoint (owner_user_id, url, hmac_key_version, hmac_wrapped_dek, hmac_nonce, hmac_ciphertext, event_filter)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListWebhookEndpointsForUser :many
SELECT * FROM app.webhook_endpoint WHERE owner_user_id = $1 AND enabled ORDER BY created_at;

-- name: RevokeWebhookEndpoint :exec
UPDATE app.webhook_endpoint SET enabled = false WHERE endpoint_id = $1;

-- name: InsertOutboxEvent :one
-- Written in the same transaction as the mutation it announces (Phase 19
-- exit criterion: rolling the transaction back must drop both).
INSERT INTO app.outbox_event (aggregate, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ClaimUndispatchedOutboxEvents :many
-- Index-only scan in causal order: event_id is uuidv7()-keyed, and the
-- partial index is on (event_id) WHERE dispatched_at IS NULL.
SELECT * FROM app.outbox_event
 WHERE dispatched_at IS NULL
 ORDER BY event_id
 LIMIT sqlc.arg(claim_size);

-- name: MarkOutboxEventDispatched :exec
UPDATE app.outbox_event SET dispatched_at = now() WHERE event_id = $1;

-- name: EnqueueWebhookDelivery :one
INSERT INTO app.webhook_delivery (endpoint_id, event_id)
VALUES ($1, $2)
RETURNING *;

-- name: ClaimPendingWebhookDeliveries :many
SELECT * FROM app.webhook_delivery
 WHERE delivered_at IS NULL AND (next_retry_at IS NULL OR next_retry_at <= now())
 ORDER BY created_at
 LIMIT sqlc.arg(claim_size);

-- name: MarkWebhookDeliverySent :exec
UPDATE app.webhook_delivery
   SET delivered_at = now(), response_status = $2, attempt = attempt + 1
 WHERE delivery_id = $1;

-- name: MarkWebhookDeliveryRetry :exec
UPDATE app.webhook_delivery
   SET attempt = attempt + 1, response_status = $2, next_retry_at = $3, error = $4
 WHERE delivery_id = $1;
