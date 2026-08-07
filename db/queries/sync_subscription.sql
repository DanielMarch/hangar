-- app.sync_subscription, app.sync_run (02_DATABASE_SCHEMA.md §4.3 #31-#32).

-- name: UpsertSyncSubscription :one
INSERT INTO app.sync_subscription (entity_kind, entity_id, route_id, acting_character_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (entity_kind, entity_id, route_id) DO UPDATE
   SET acting_character_id = EXCLUDED.acting_character_id
RETURNING *;

-- name: GetSyncSubscription :one
SELECT * FROM app.sync_subscription WHERE subscription_id = $1;

-- name: SetSyncSubscriptionEnabled :exec
UPDATE app.sync_subscription SET enabled = $2 WHERE subscription_id = $1;

-- name: ClaimDueSubscriptions :many
-- The 5-second planner claim loop. §4.3's illustrative index predicate
-- couldn't include `now()` (not IMMUTABLE — see 00006_platform_esi_gateway.sql);
-- this query reproduces the intended filter in two steps instead: the
-- partial index on (next_due_at) WHERE enabled narrows to enabled rows past
-- due, and the snoozed_until check is then cheap over that small remainder.
SELECT * FROM app.sync_subscription
 WHERE enabled
   AND next_due_at <= now()
   AND (snoozed_until IS NULL OR snoozed_until < now())
 ORDER BY next_due_at
 LIMIT sqlc.arg(claim_size);

-- name: RecordSyncSuccess :exec
UPDATE app.sync_subscription
   SET last_success_at = now(), last_status = $2, etag = $3, last_modified = $4,
       cursor_after = $5, next_due_at = $6, consecutive_304 = $7, consecutive_403 = 0
 WHERE subscription_id = $1;

-- name: RecordSync304 :exec
UPDATE app.sync_subscription
   SET last_status = 304, next_due_at = $2, consecutive_304 = consecutive_304 + 1, consecutive_403 = 0
 WHERE subscription_id = $1;

-- name: RecordSync403 :exec
UPDATE app.sync_subscription
   SET last_status = 403, consecutive_403 = consecutive_403 + 1
 WHERE subscription_id = $1;

-- name: SnoozeSyncSubscription :exec
UPDATE app.sync_subscription SET snoozed_until = $2 WHERE subscription_id = $1;

-- name: ElectActingCharacter :exec
UPDATE app.sync_subscription SET acting_character_id = $2 WHERE subscription_id = $1;

-- name: SetSyncNoCacheOptIn :exec
UPDATE app.sync_subscription SET opt_in_no_cache = $2 WHERE subscription_id = $1;

-- ---- sync_run ----

-- name: StartSyncRun :one
INSERT INTO app.sync_run (subscription_id)
VALUES ($1)
RETURNING *;

-- name: FinishSyncRun :exec
UPDATE app.sync_run
   SET finished_at = now(), status = $2, outcome = $3, error = $4, rows_affected = $5
 WHERE run_id = $1;

-- name: ListRecentSyncRuns :many
-- Source of the `_sync` response envelope.
SELECT * FROM app.sync_run
 WHERE subscription_id = $1
 ORDER BY started_at DESC
 LIMIT sqlc.arg(page_size);
