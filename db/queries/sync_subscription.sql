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
-- The 5-second planner claim loop (Phase 6, internal/sync/planner/claim.go).
-- §4.3's illustrative index predicate couldn't include `now()` (not
-- IMMUTABLE — see 00006_platform_esi_gateway.sql); this query reproduces
-- the intended filter in two steps instead: the partial index on
-- (next_due_at) WHERE enabled narrows to enabled rows past due, and the
-- snoozed_until check is then cheap over that small remainder.
--
-- blocked_by_pin and retired routes are excluded HERE, in the predicate —
-- never as a post-claim filter, or the planner burns its claim budget on
-- work it can't run (01_ARCHITECTURE.md §6.1 edge case). Likewise the
-- snoozed_until check: excluding it in the predicate, not after claiming,
-- is what keeps a 429-snoozed subscription from eating a claim slot every
-- 5s until it wakes up.
--
-- FOR UPDATE OF ss SKIP LOCKED is the first line of defence against a
-- double claim when N planner instances race the same tick (only one can
-- ever hold leadership at a time per §6.1, but this makes the query safe
-- even if that invariant is ever relaxed); River's unique-job option on
-- (route_id, entity_kind, entity_id) is the second line, not the first.
-- Locking `esi_route` too would serialise unrelated claims against the
-- catalogue, so only `ss` is locked.
SELECT ss.* FROM app.sync_subscription ss
 JOIN app.esi_route r ON r.route_id = ss.route_id
 WHERE ss.enabled
   AND NOT r.blocked_by_pin
   AND r.retired_at IS NULL
   AND ss.next_due_at <= now()
   AND (ss.snoozed_until IS NULL OR ss.snoozed_until < now())
 ORDER BY ss.next_due_at
 LIMIT sqlc.arg(claim_size)
 FOR UPDATE OF ss SKIP LOCKED;

-- name: LeaseSyncSubscriptions :exec
-- Advances next_due_at for just-claimed rows so the NEXT 5s tick doesn't
-- reclaim them before the in-flight attempt (a Phase 7+ worker) records its
-- real outcome via RecordSyncSuccess/RecordSync304/RecordSync403. Run in
-- the SAME transaction as ClaimDueSubscriptions and the River enqueue —
-- this is the claim transaction's own defence against duplicate enqueues
-- (01_ARCHITECTURE.md §6.1), independent of and prior to River's
-- unique-job option.
UPDATE app.sync_subscription
   SET next_due_at = sqlc.arg(leased_until)
 WHERE subscription_id = ANY(sqlc.arg(subscription_ids)::uuid[]);

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

-- ---- sync_acting_character_history (Phase 8, 01_ARCHITECTURE.md §6.3) ----
-- Per-candidate 403 history for the acting-character election, keyed by
-- (entity_kind, entity_id, route_id, character_id) rather than by
-- subscription_id — see 00031_phase8_acting_character_history.sql's header
-- for why app.sync_subscription.consecutive_403 alone can't serve this.

-- name: RecordActingCharacter403 :exec
INSERT INTO app.sync_acting_character_history (entity_kind, entity_id, route_id, character_id, consecutive_403, last_403_at)
VALUES ($1, $2, $3, $4, 1, now())
ON CONFLICT (entity_kind, entity_id, route_id, character_id) DO UPDATE
   SET consecutive_403 = app.sync_acting_character_history.consecutive_403 + 1,
       last_403_at = now(), updated_at = now();

-- name: ResetActingCharacter403 :exec
-- Called on a success by the character that just acted, so a candidate
-- that failed twice and then succeeded once is no longer penalised.
INSERT INTO app.sync_acting_character_history (entity_kind, entity_id, route_id, character_id, consecutive_403)
VALUES ($1, $2, $3, $4, 0)
ON CONFLICT (entity_kind, entity_id, route_id, character_id) DO UPDATE
   SET consecutive_403 = 0, updated_at = now();

-- name: ListActingCharacterHistory :many
SELECT * FROM app.sync_acting_character_history
 WHERE entity_kind = $1 AND entity_id = $2 AND route_id = $3;

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
