-- app.esi_replica — the heartbeat registry that selects `solo` vs
-- `clustered` mode (02_DATABASE_SCHEMA.md §4.3 #30). Written every 10s by
-- internal/telemetry.ReplicaHeartbeat (present since Phase 0); read here by
-- Phase 4's ledger mode selector. A replica is live if its heartbeat is
-- under 30s old (internal/telemetry.LiveThreshold).

-- name: UpsertReplicaHeartbeat :exec
INSERT INTO app.esi_replica (replica_id, role, version, started_at, last_heartbeat)
VALUES ($1, $2, $3, now(), now())
ON CONFLICT (replica_id) DO UPDATE SET last_heartbeat = now(), version = EXCLUDED.version;

-- name: DeregisterReplica :exec
DELETE FROM app.esi_replica WHERE replica_id = $1;

-- name: CountLiveReplicas :one
-- The solo/clustered predicate itself: exactly one live row ⇒ solo, two or
-- more ⇒ clustered. sqlc.arg(live_threshold) is a negative interval, e.g.
-- '-30 seconds', added to now().
SELECT count(*) FROM app.esi_replica
 WHERE last_heartbeat > now() + sqlc.arg(live_threshold)::interval;

-- name: ListLiveReplicas :many
SELECT * FROM app.esi_replica
 WHERE last_heartbeat > now() + sqlc.arg(live_threshold)::interval
 ORDER BY started_at;

-- name: DeleteStaleReplicas :execrows
-- Flagged by sqlc's flag-delete rule for review: a replica past the
-- liveness threshold is dead by definition, not a soft-deletable entity.
--
-- PHASE 21 (B-2). Called by internal/housekeeping.Sweeper. The argument is
-- deliberately NOT the liveness threshold and is no longer named for it:
-- it is a RETENTION window, and it must be far longer than
-- telemetry.LiveThreshold (30s).
--
-- Why the distinction is load-bearing. CountLiveReplicas above decides
-- solo vs clustered mode. If this delete ran at the liveness threshold, a
-- replica whose heartbeat was one second late would have its registration
-- DELETED rather than merely not counted — and on a two-replica
-- installation that turns a transient stall into a mode flip, where two
-- replicas each believe they are solo and each spend the full bucket.
-- That is a Governor 1 breach (Gate 1.1) manufactured by a housekeeping
-- job. At a retention window two orders of magnitude above the liveness
-- threshold, a row can only be removed long after every reader has agreed
-- the replica is dead.
--
-- Deleting a dead row is otherwise harmless to mode selection, which is
-- why this is retention rather than correctness: CountLiveReplicas filters
-- on the heartbeat window, so a stale row was already invisible to it.
-- What accumulates without this is one row per force-killed process.
DELETE FROM app.esi_replica WHERE last_heartbeat <= now() + sqlc.arg(retention)::interval;
