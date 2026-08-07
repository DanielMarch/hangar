-- app.esi_ledger_bucket, app.esi_ledger_entry — Governor 1's cluster-shared
-- floating-window consumption ledger (02_DATABASE_SCHEMA.md §4.3, SRS v3.1
-- §4.1.3, defects B1/B9). Both tables are UNLOGGED. `"window"` is quoted
-- throughout: it is a reserved word in Postgres.
--
-- The acquire sequence (Phase 4 consumes these in one short transaction,
-- the bucket row is the only lock taken):
--   1. GetLedgerBucketForUpdate      -- SELECT ... FOR UPDATE, serialises this bucket only
--   2. EvictExpiredLedgerEntries     -- evict settled-and-aged and expired reservations
--   3. SumLedgerEntryCost            -- measure consumption in the current window
--   4. ReserveLedgerEntry            -- reserve the worst-case cost (5), issue time + expiry

-- name: UpsertLedgerBucket :exec
INSERT INTO app.esi_ledger_bucket (rate_limit_group, user_key, max_tokens, "window")
VALUES ($1, $2, $3, $4)
ON CONFLICT (rate_limit_group, user_key) DO UPDATE
   SET max_tokens = EXCLUDED.max_tokens, "window" = EXCLUDED."window", updated_at = now()
 WHERE (app.esi_ledger_bucket.max_tokens, app.esi_ledger_bucket."window")
    IS DISTINCT FROM (EXCLUDED.max_tokens, EXCLUDED."window");

-- name: GetLedgerBucketForUpdate :one
SELECT max_tokens, "window" FROM app.esi_ledger_bucket
 WHERE rate_limit_group = $1 AND user_key = $2
   FOR UPDATE;

-- name: EvictExpiredLedgerEntries :exec
DELETE FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2
   AND (consumed_at <= now() - sqlc.arg(window_interval)::interval
        OR (state = 'reserved' AND expires_at < now()));

-- name: SumLedgerEntryCost :one
SELECT coalesce(sum(cost), 0)::bigint AS total_cost
  FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2;

-- name: ReserveLedgerEntry :one
INSERT INTO app.esi_ledger_entry (rate_limit_group, user_key, cost, consumed_at, state, expires_at)
VALUES ($1, $2, 5, now(), 'reserved', now() + sqlc.arg(request_timeout)::interval)
RETURNING *;

-- name: SettleLedgerEntry :exec
-- Re-stamps consumed_at to the RESPONSE timestamp (defect B9) and records
-- the observed cost by status (2XX=2, 3XX=1, 4XX=5, 429=0, 5XX/transport=0/5
-- per the caller's classification — this query just persists the outcome).
UPDATE app.esi_ledger_entry
   SET cost = $2, consumed_at = now(), state = 'settled', expires_at = NULL
 WHERE entry_id = $1;

-- name: InsertSyntheticLedgerEntry :exec
-- Header reconciliation (§4.1.3): when the server reports less headroom
-- than the ledger holds, a synthetic entry expiring a full window from now
-- is injected.
INSERT INTO app.esi_ledger_entry (rate_limit_group, user_key, cost, consumed_at, state)
VALUES ($1, $2, $3, now(), 'synthetic');

-- name: EvictOldestLedgerEntries :many
-- Header reconciliation, the other direction: when the server reports more
-- headroom than the ledger holds, the oldest entries are evicted until the
-- values agree. Caller evicts however many rows the reconciliation math
-- says to; this returns candidates oldest-first.
SELECT entry_id FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2
 ORDER BY consumed_at ASC
 LIMIT sqlc.arg(max_evict);

-- name: DeleteLedgerEntryByID :exec
DELETE FROM app.esi_ledger_entry WHERE entry_id = $1;

-- name: RecordServerLedgerReading :exec
UPDATE app.esi_ledger_bucket
   SET server_remaining = $3, server_observed_at = now(), updated_at = now()
 WHERE rate_limit_group = $1 AND user_key = $2;

-- ---- solo/clustered mode flush (§5.6 — both transitions must not lose or
-- double-count entries) ----

-- name: FlushLedgerEntriesForBucket :many
-- clustered -> solo: read the shared table into memory before the fast path
-- engages.
SELECT * FROM app.esi_ledger_entry WHERE rate_limit_group = $1 AND user_key = $2;

-- name: BulkInsertLedgerEntry :exec
-- solo -> clustered: flush the in-process ledger into the shared table
-- before any further request is admitted. Called once per in-memory entry;
-- entry_id is supplied by the caller so a retried flush is idempotent.
INSERT INTO app.esi_ledger_entry (entry_id, rate_limit_group, user_key, cost, consumed_at, state, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (entry_id) DO NOTHING;
