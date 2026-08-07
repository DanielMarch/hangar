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

-- name: DeleteStaleReplicas :exec
-- Flagged by sqlc's flag-delete rule for review: a replica past the
-- liveness threshold is dead by definition, not a soft-deletable entity.
DELETE FROM app.esi_replica WHERE last_heartbeat <= now() + sqlc.arg(live_threshold)::interval;
