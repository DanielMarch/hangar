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

-- ── SUBSCRIPTION RECONCILIATION (defect B42, Phase 20.1.1) ────────────────
-- Nothing in production ever created a subscription, so app.sync_subscription
-- was permanently empty and the entire ESI sync engine had nothing to do.
-- These four statements are the fix, and they are deliberately SET-BASED
-- rather than a Go loop issuing one upsert per route: reconciliation runs on
-- every replica, on a timer and on every login, so it has to be atomic and
-- idempotent by construction rather than by careful sequencing.
--
-- SCOPE GATING is the NOT EXISTS clause, and it is not an optimisation. A
-- subscription whose route needs a scope the token never granted produces a
-- guaranteed 403 on every single attempt, and Governor 2 counts every 403
-- against an installation-wide budget of 100 per minute (§5.7). Creating
-- those rows would spend the error budget on requests that cannot succeed,
-- and could pause the whole installation. Routes requiring no scope (the
-- public ones) satisfy the clause trivially, which is correct.

-- name: ReconcileCharacterSubscriptions :execrows
-- One subscription per character-scoped route whose required scopes are all
-- present in this character's granted set. The character is its own acting
-- character. Existing rows are left completely alone — DO NOTHING, not DO
-- UPDATE, because a row may carry live sync state (etag, cursor_after,
-- consecutive_304) that reconciliation has no business resetting.
INSERT INTO app.sync_subscription (entity_kind, entity_id, route_id, acting_character_id)
SELECT 'character', c.character_id, r.route_id, c.character_id
  FROM app.character c
  JOIN app.character_token t ON t.character_id = c.character_id AND t.valid
  JOIN app.esi_route r
    ON r.method = 'GET'
   AND r.retired_at IS NULL
   AND NOT r.blocked_by_pin
   AND r.upstream_path = ANY(sqlc.arg(paths)::text[])
 WHERE c.character_id = sqlc.arg(character_id)
   AND c.deleted_at IS NULL
   AND NOT EXISTS (
         SELECT 1
           FROM app.esi_route_scope rs
          WHERE rs.route_id = r.route_id
            AND rs.scope NOT IN (
                  SELECT s.scope FROM app.character_token_scope s
                   WHERE s.character_id = c.character_id))
ON CONFLICT (entity_kind, entity_id, route_id) DO NOTHING;

-- name: ReconcileCorporationSubscriptions :execrows
-- Corporation-scoped routes, gated on the ACTING character's scopes rather
-- than the corporation's (a corporation has no token; §6.3 elects a
-- character to act for it). acting_character_id is set to the character this
-- row was justified by; internal/sync's elector may re-elect later, which is
-- why ON CONFLICT leaves the existing choice alone rather than fighting it.
--
-- This can only run once app.character.corporation_id is populated, and that
-- column is filled by /characters/{character_id} — a CHARACTER route. The
-- bootstrap is therefore genuinely ordered: character subscriptions must
-- exist and run before this produces anything at all. That is why
-- reconciliation is periodic rather than a single pass at link time.
INSERT INTO app.sync_subscription (entity_kind, entity_id, route_id, acting_character_id)
SELECT 'corporation', c.corporation_id, r.route_id, c.character_id
  FROM app.character c
  JOIN app.character_token t ON t.character_id = c.character_id AND t.valid
  JOIN app.esi_route r
    ON r.method = 'GET'
   AND r.retired_at IS NULL
   AND NOT r.blocked_by_pin
   AND r.upstream_path = ANY(sqlc.arg(paths)::text[])
 WHERE c.corporation_id IS NOT NULL
   AND c.deleted_at IS NULL
   AND NOT EXISTS (
         SELECT 1
           FROM app.esi_route_scope rs
          WHERE rs.route_id = r.route_id
            AND rs.scope NOT IN (
                  SELECT s.scope FROM app.character_token_scope s
                   WHERE s.character_id = c.character_id))
ON CONFLICT (entity_kind, entity_id, route_id) DO NOTHING;

-- name: ReconcileGlobalSubscriptions :execrows
-- Global routes (/status, sovereignty) belong to no owner: entity_id = 0 per
-- internal/sync.EntityGlobal, and acting_character_id stays NULL because
-- these routes are unauthenticated. They are created at boot rather than on
-- login — an installation with no characters yet should still know whether
-- Tranquility is up.
INSERT INTO app.sync_subscription (entity_kind, entity_id, route_id, acting_character_id)
SELECT 'global', 0, r.route_id, NULL
  FROM app.esi_route r
 WHERE r.method = 'GET'
   AND r.retired_at IS NULL
   AND NOT r.blocked_by_pin
   AND r.upstream_path = ANY(sqlc.arg(paths)::text[])
ON CONFLICT (entity_kind, entity_id, route_id) DO NOTHING;

-- name: DisableUnscopedSubscriptions :execrows
-- DISABLE, never delete, a subscription whose acting character no longer
-- carries every scope its route needs — the case where a user re-authorises
-- with a narrower grant, or a token is invalidated.
--
-- Disabling rather than deleting is deliberate and is what the `enabled`
-- column is for: the row carries accumulated sync state (etag, last_modified,
-- cursor_after) that is expensive to rebuild and still valid, so if the scope
-- comes back the subscription resumes conditionally instead of re-fetching
-- the whole collection. It also leaves evidence on the admin surface that the
-- route WAS being polled and no longer is, which a delete would erase.
--
-- The mirror case — a subscription that is disabled but now has its scopes
-- again — is handled by the three reconcile statements above only for rows
-- that do not exist. So this re-enables them explicitly.
WITH covered AS (
  SELECT s.subscription_id,
         (s.acting_character_id IS NULL
          OR (EXISTS (SELECT 1 FROM app.character_token t
                       WHERE t.character_id = s.acting_character_id AND t.valid)
              AND NOT EXISTS (
                    SELECT 1 FROM app.esi_route_scope rs
                     WHERE rs.route_id = s.route_id
                       AND rs.scope NOT IN (
                             SELECT ts.scope FROM app.character_token_scope ts
                              WHERE ts.character_id = s.acting_character_id))))
           AS has_scopes
    FROM app.sync_subscription s
)
UPDATE app.sync_subscription s
   SET enabled = covered.has_scopes
  FROM covered
 WHERE covered.subscription_id = s.subscription_id
   AND s.enabled IS DISTINCT FROM covered.has_scopes;

-- name: SubscriptionEnabledForPath :one
-- Whether this installation actually polls a given route (defect B42).
-- "Never scheduled" and "scheduled but failing" are different problems with
-- different operator actions, and before Phase 20.1.1 every surface
-- conflated them — which is a large part of why an installation with ZERO
-- subscriptions looked merely quiet for the whole life of the project.
SELECT EXISTS (
  SELECT 1
    FROM app.sync_subscription s
    JOIN app.esi_route r ON r.route_id = s.route_id
   WHERE r.upstream_path = sqlc.arg(upstream_path)
     AND s.enabled);
