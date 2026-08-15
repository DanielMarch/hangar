-- app.webhook_endpoint, app.outbox_event, app.webhook_delivery
-- (02_DATABASE_SCHEMA.md §4.6 #44-#46).

-- name: CreateWebhookEndpoint :one
INSERT INTO app.webhook_endpoint (owner_user_id, url, hmac_key_version, hmac_wrapped_dek, hmac_nonce, hmac_ciphertext, event_filter)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListWebhookEndpointsForUser :many
-- PHASE 20.5: `AND enabled` removed. An endpoint HANGAR auto-disabled after
-- consecutive failures is the one its owner most needs to see — hiding it
-- makes a working configuration and a broken one look identical, which is
-- the same shape of defect as everything else this phase closed. The handler
-- returns `enabled`, `disabled_at` and `disabled_reason` so the difference is
-- visible rather than inferred.
SELECT * FROM app.webhook_endpoint WHERE owner_user_id = $1 ORDER BY created_at;

-- name: RotateWebhookSecret :one
-- PHASE 20.5 (B24). Installs a new signing secret and demotes the current one
-- to `prev_*` for a bounded grace window, IN ONE STATEMENT — the demotion
-- reads the columns it is overwriting, so doing it as two statements would
-- leave a gap in which a delivery could be signed with a secret nobody would
-- accept.
--
-- The endpoint must belong to the caller: owner_user_id is in the predicate
-- rather than checked in Go, so a handler that forgets the check returns no
-- rows instead of rotating somebody else's endpoint.
UPDATE app.webhook_endpoint
   SET prev_hmac_key_version = hmac_key_version,
       prev_hmac_wrapped_dek = hmac_wrapped_dek,
       prev_hmac_nonce       = hmac_nonce,
       prev_hmac_ciphertext  = hmac_ciphertext,
       prev_hmac_expires_at  = now() + sqlc.arg(grace)::interval,
       hmac_key_version      = sqlc.arg(hmac_key_version),
       hmac_wrapped_dek      = sqlc.arg(hmac_wrapped_dek),
       hmac_nonce            = sqlc.arg(hmac_nonce),
       hmac_ciphertext       = sqlc.arg(hmac_ciphertext),
       rotated_at            = now()
 WHERE endpoint_id = sqlc.arg(endpoint_id) AND owner_user_id = sqlc.arg(owner_user_id)
RETURNING *;

-- name: ExpireWebhookPreviousSecret :exec
-- Clears a superseded secret once its grace window has passed. Called by the
-- dispatcher at delivery time rather than by a sweeper: the dispatcher is
-- already reading this exact row, and a second secret that outlives its
-- window because a cron did not run is a second secret to steal.
UPDATE app.webhook_endpoint
   SET prev_hmac_key_version = NULL, prev_hmac_wrapped_dek = NULL,
       prev_hmac_nonce = NULL, prev_hmac_ciphertext = NULL, prev_hmac_expires_at = NULL
 WHERE endpoint_id = $1 AND prev_hmac_expires_at IS NOT NULL AND prev_hmac_expires_at <= now();

-- name: GetWebhookEndpointForOwner :one
-- The owner-scoped read. Same reasoning as RotateWebhookSecret: ownership is
-- a predicate, not a Go-side comparison somebody can omit.
SELECT * FROM app.webhook_endpoint WHERE endpoint_id = $1 AND owner_user_id = $2;

-- name: RevokeWebhookEndpointForOwner :exec
UPDATE app.webhook_endpoint SET enabled = false, disabled_at = now(), disabled_reason = 'revoked by owner'
 WHERE endpoint_id = $1 AND owner_user_id = $2 AND enabled;

-- name: InsertOutboxEvent :one
-- Written in the same transaction as the mutation it announces (Phase 19
-- exit criterion: rolling the transaction back must drop both).
INSERT INTO app.outbox_event (aggregate, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ClaimUndispatchedOutboxEvents :many
-- Index-only scan in causal order: event_id is uuidv7()-keyed, and the
-- partial index is on (event_id) WHERE dispatched_at IS NULL.
--
-- PHASE 19: FOR UPDATE SKIP LOCKED, and the caller must hold a
-- transaction. Without the lock two replicas' dispatchers both read the
-- same undispatched rows and both fan them out, so every third-party
-- endpoint gets each event twice — and the receiver has no way to tell
-- that from a genuine retry. SKIP LOCKED (rather than plain FOR UPDATE) so
-- the second dispatcher takes the NEXT batch instead of blocking on the
-- first: the outbox must drain faster with more replicas, not the same.
SELECT * FROM app.outbox_event
 WHERE dispatched_at IS NULL
 ORDER BY event_id
 LIMIT sqlc.arg(claim_size)
   FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEventDispatched :exec
UPDATE app.outbox_event SET dispatched_at = now() WHERE event_id = $1;

-- name: EnqueueWebhookDelivery :one
INSERT INTO app.webhook_delivery (endpoint_id, event_id)
VALUES ($1, $2)
RETURNING *;

-- name: ListEndpointsForEvent :many
-- The fan-out lookup: every enabled endpoint that has not filtered this
-- event type out. An EMPTY event_filter means "everything" — the array
-- containment test would be false for an empty left operand, so the empty
-- case is spelled out rather than left to the operator.
SELECT * FROM app.webhook_endpoint
 WHERE enabled
   AND (cardinality(event_filter) = 0 OR sqlc.arg(event_type)::text = ANY (event_filter))
 ORDER BY endpoint_id;

-- name: LeasePendingWebhookDeliveries :many
-- Claim-by-lease, not claim-by-read.
--
-- The dispatcher makes an HTTP call between claiming a delivery and
-- settling it, and it must not hold a transaction open across that call.
-- So the claim MOVES next_retry_at forward by a lease before releasing the
-- transaction: a crash between claim and send leaves the row untouched
-- except for the lease, and it becomes claimable again when the lease
-- expires. Nothing is lost (the roadmap's "must not lose an event when the
-- dispatcher crashes between claim and send") and nothing is double-sent
-- inside the lease window. The guarantee is at-least-once, which is what a
-- signed webhook contract promises.
--
-- The join to app.webhook_endpoint is not decoration: a delivery for an
-- endpoint that has since been disabled must stop consuming attempts.
UPDATE app.webhook_delivery AS d
   SET next_retry_at = now() + sqlc.arg(lease)::interval
  FROM (
       SELECT c.delivery_id
         FROM app.webhook_delivery c
         JOIN app.webhook_endpoint e USING (endpoint_id)
        WHERE c.delivered_at IS NULL
          AND c.failed_at IS NULL
          AND e.enabled
          AND (c.next_retry_at IS NULL OR c.next_retry_at <= now())
        ORDER BY c.created_at
        LIMIT sqlc.arg(claim_size)
          FOR UPDATE OF c SKIP LOCKED
       ) AS claimed
 WHERE d.delivery_id = claimed.delivery_id
RETURNING d.*;

-- name: MarkWebhookDeliverySent :exec
UPDATE app.webhook_delivery
   SET delivered_at = now(), response_status = $2, attempt = attempt + 1,
       next_retry_at = NULL, error = NULL
 WHERE delivery_id = $1;

-- name: MarkWebhookDeliveryRetry :exec
UPDATE app.webhook_delivery
   SET attempt = attempt + 1, response_status = $2, next_retry_at = $3, error = $4
 WHERE delivery_id = $1;

-- name: MarkWebhookDeliveryFailed :exec
-- Dead-letter. next_retry_at is cleared deliberately: leaving it set would
-- be harmless but misleading, and leaving it NULL WITHOUT failed_at would
-- make the row instantly re-claimable forever (see migration 00041).
UPDATE app.webhook_delivery
   SET attempt = attempt + 1, response_status = $2, next_retry_at = NULL,
       failed_at = now(), error = $3
 WHERE delivery_id = $1;

-- name: RecordWebhookEndpointFailure :one
-- Bumps the endpoint-level breaker and returns the new count, so the
-- dispatcher decides to disable from the value the database actually
-- committed rather than from one it read a moment earlier.
UPDATE app.webhook_endpoint
   SET consecutive_failures = consecutive_failures + 1
 WHERE endpoint_id = $1
RETURNING consecutive_failures;

-- name: ClearWebhookEndpointFailures :exec
UPDATE app.webhook_endpoint
   SET consecutive_failures = 0
 WHERE endpoint_id = $1 AND consecutive_failures <> 0;

-- name: DisableWebhookEndpoint :exec
-- The auto-disable. disabled_at/disabled_reason distinguish this from the
-- owner switching the endpoint off (RevokeWebhookEndpoint), which is the
-- whole point of having them.
UPDATE app.webhook_endpoint
   SET enabled = false, disabled_at = now(), disabled_reason = $2
 WHERE endpoint_id = $1 AND enabled;

-- name: FailOutstandingDeliveriesForEndpoint :exec
-- Dead-letters everything still owed to an endpoint that has just been
-- auto-disabled.
--
-- Leaving them merely 'pending' would look tidier and be wrong twice over.
-- LeasePendingWebhookDeliveries joins on e.enabled, so a disabled
-- endpoint's queue is unclaimable — the rows would sit forever, invisible
-- to both the pump and the dead-letter board, which is precisely the
-- "neither delivered nor dead-lettered" state the whole design exists to
-- rule out. And the roadmap's requirement is not "stop trying", it is "must
-- not retain jobs forever". An operator who re-enables the endpoint should
-- get NEW events, not a month of backlog replayed at a receiver that has
-- no idea what to do with it.
UPDATE app.webhook_delivery
   SET failed_at = now(), next_retry_at = NULL, error = $2
 WHERE endpoint_id = $1 AND delivered_at IS NULL AND failed_at IS NULL;

-- name: GetWebhookEndpoint :one
SELECT * FROM app.webhook_endpoint WHERE endpoint_id = $1;

-- name: GetOutboxEvent :one
SELECT * FROM app.outbox_event WHERE event_id = $1;

-- name: CountUndispatchedOutboxEvents :one
-- Deliberately NOT ClaimUndispatchedOutboxEvents with a huge limit: that
-- query takes row locks and skips the ones another dispatcher holds, so it
-- would under-report exactly when the backlog matters most.
SELECT count(*) FROM app.outbox_event WHERE dispatched_at IS NULL;

-- name: ListDeadLetterWebhookDeliveries :many
-- The admin-visible counterpart to alerting's dead-letter board: deliveries
-- that will never be retried, newest first.
SELECT d.*, e.url, e.owner_user_id
  FROM app.webhook_delivery d
  JOIN app.webhook_endpoint e USING (endpoint_id)
 WHERE d.failed_at IS NOT NULL
 ORDER BY d.failed_at DESC
 LIMIT sqlc.arg(page_size);
