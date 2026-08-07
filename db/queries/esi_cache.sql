-- app.esi_cache_entry (02_DATABASE_SCHEMA.md §4.3 #26) — the Postgres L2
-- tier of internal/esi/cache. UNLOGGED, never authoritative: a miss here
-- costs one revalidation round, never a request failure.

-- name: GetEsiCacheEntry :one
SELECT * FROM app.esi_cache_entry WHERE cache_key = $1 AND expires_at > now();

-- name: UpsertEsiCacheEntry :exec
INSERT INTO app.esi_cache_entry AS t (cache_key, etag, last_modified, body, status, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cache_key) DO UPDATE
   SET etag          = EXCLUDED.etag,
       last_modified = EXCLUDED.last_modified,
       body          = EXCLUDED.body,
       status        = EXCLUDED.status,
       expires_at    = EXCLUDED.expires_at,
       stored_at     = now()
 WHERE (t.etag, t.last_modified, t.body, t.status, t.expires_at)
    IS DISTINCT FROM
       (EXCLUDED.etag, EXCLUDED.last_modified, EXCLUDED.body, EXCLUDED.status, EXCLUDED.expires_at);

-- name: DeleteExpiredEsiCacheEntries :exec
-- Flagged by sqlc's flag-delete rule for review: this is a cache, never a
-- source of truth (§4.3), so an expired row has nothing worth a soft delete.
-- Intended as a periodic housekeeping sweep (a later phase's River job);
-- L2 reads already filter on expires_at > now() on their own, so this is
-- disk reclamation, not a correctness dependency.
DELETE FROM app.esi_cache_entry WHERE expires_at <= now();

-- name: CountEsiCacheEntries :one
-- Cheap operational visibility into L2 size — useful for the admin
-- observability surface (Phase 18) without needing a full table scan tool.
SELECT count(*) FROM app.esi_cache_entry;
