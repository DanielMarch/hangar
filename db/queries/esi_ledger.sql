-- app.esi_ledger_bucket, app.esi_ledger_entry — Governor 1's cluster-shared
-- floating-window consumption ledger (02_DATABASE_SCHEMA.md §4.3, SRS v3.1
-- §4.1.3, defects B1/B9). Both tables are UNLOGGED. `"window"` is quoted
-- throughout: it is a reserved word in Postgres.
--
-- The acquire sequence (Phase 4 consumes these in one short transaction,
-- the bucket row is the only lock taken):
--   1. GetLedgerBucketForUpdate      -- SELECT ... FOR UPDATE, serialises this bucket only
--   2. ExpireLedgerReservations      -- charge the worst case for reservations that timed out
--   3. EvictAgedLedgerEntries        -- evict settled/synthetic entries that have aged out of the window
--   4. SumLedgerEntryCost            -- measure consumption in the current window
--   5. ReserveLedgerEntry            -- reserve the worst-case cost (5), issue time + expiry
--
-- PHASE 4 FIX: the Phase 1a scaffold's original EvictExpiredLedgerEntries
-- DELETEd expired reservations outright, which silently reclaimed an
-- abandoned request's budget for free. 01_ARCHITECTURE.md §5.5's edge case
-- is explicit that a reservation whose request never returns "must expire
-- at the request timeout and be charged the worst case — never silently
-- reclaimed for free." Split into ExpireLedgerReservations (converts an
-- expired reservation into a settled, worst-case-cost entry timestamped at
-- its deadline, so it still ages out of the window on its own schedule) and
-- EvictAgedLedgerEntries (the original window-eviction, now scoped away
-- from 'reserved' rows so a live, not-yet-expired reservation is never
-- evicted just because its issue-time consumed_at looks old).

-- name: UpsertLedgerBucket :exec
-- PHASE 20.3. max_tokens here is the route's REAL, advertised ceiling and
-- nothing else. It used to receive whatever ceiling the calling code asked
-- to be admitted against, which meant §4.4's char-notification reserve
-- (background poller: 15-5=10) was persisted into a row every caller
-- shares. Two consequences, both real: ListLedgerDivergence below measured
-- local_remaining against 10 while ESI reported against 15, so Gate 1.3 saw
-- a permanent divergence of exactly 5 on a healthy installation; and an
-- interactive caller passing the real 15 flipped the row back, with the
-- IS DISTINCT FROM guard turning every flip into a real write. The
-- admission ceiling is now a parameter of the acquire statement instead —
-- see internal/esi/ratelimit's AcquireRequest.AdmissionMaxTokens.
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

-- name: ExpireLedgerReservations :many
-- A reservation past its request-timeout deadline without a Settle is
-- charged the worst case (5), stamped at its deadline (the best available
-- proxy for "response time" of a request that never answered), and turned
-- into a normal settled entry — never deleted for free.
UPDATE app.esi_ledger_entry
   SET cost = 5, consumed_at = expires_at, state = 'settled', expires_at = NULL
 WHERE rate_limit_group = $1 AND user_key = $2
   AND state = 'reserved' AND expires_at < now()
RETURNING *;

-- name: EvictAgedLedgerEntries :exec
-- Settled/synthetic entries only: a 'reserved' row's consumed_at is its
-- issue time, not a window-eviction signal — ExpireLedgerReservations
-- above is what retires a reservation, on the request-timeout schedule,
-- not the window schedule.
DELETE FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2
   AND state != 'reserved'
   AND consumed_at <= now() - sqlc.arg(window_interval)::interval;

-- name: SumSettledLedgerEntryCost :one
-- RECONCILIATION ONLY, and it deliberately EXCLUDES 'reserved' rows.
--
-- ── PHASE 20.2, MEASURED ON A LIVE INSTALLATION ──────────────────────────
-- This was SumLedgerEntryCost, with no state filter, and it is the input to
-- 01_ARCHITECTURE.md §5.5's "the server always wins" comparison. Including
-- in-flight reservations in that comparison is a category error: a
-- reservation is HANGAR's PREDICTION of a request whose result the server's
-- X-Ratelimit-Remaining cannot possibly have counted yet, because the
-- request has not finished.
--
-- The consequence was visible within a minute of B29's wiring going live
-- against real ESI. One in-flight reservation makes local availability look
-- 5 lower than the server's, so the reconciler evicts settled entries —
-- forgiving consumption that really happened. The reservation then settles
-- at cost 2, local availability now looks HIGHER than the server's, and the
-- next response injects a synthetic entry to compensate. The two directions
-- chase each other: measured on the development installation,
-- `char-location` held 6 synthetic entries worth 15 and reported a
-- divergence of 10 against a Gate 1.3 tolerance of 1, on an installation
-- with no errors and nothing wrong with it.
--
-- Settled and synthetic entries are exactly the population the server has
-- had a chance to observe, so they are the population to reconcile against.
-- The ACQUIRE path is untouched and still counts reservations — that is the
-- whole point of predictive reservation (see acquireLedgerEntrySQL's `used`
-- CTE in shared.go, which has no state filter and must not gain one).
SELECT coalesce(sum(cost), 0)::bigint AS total_cost
  FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2
   AND state != 'reserved';

-- AcquireLedgerEntry (the primary clustered acquire path) is deliberately
-- NOT a sqlc query — see shared.go's acquireLedgerEntrySQL const for both
-- the statement and the full rationale. Its admission test compares
-- consumption against `least(locked.max_tokens, $4)` since Phase 20.3: the
-- stored ceiling is the truth, $4 is the caller's own (never-stored)
-- reduction, and a caller can hold tokens back from itself but can never
-- grant itself more than the bucket has. PHASE 4 PERFORMANCE FIX summary:
-- running the acquire sequence above as five separate round trips inside
-- an explicit BEGIN/COMMIT (seven round trips total) fell well short of
-- BenchmarkLedgerClusteredThroughput's >=2000 ops/s/replica-at-p99<10ms
-- target. acquireLedgerEntrySQL folds every step into one multi-CTE
-- statement that Postgres executes as one atomic unit — no explicit
-- transaction needed, since the FOR UPDATE lock taken in its `locked` CTE
-- is held for the whole statement's execution, exactly the scope its
-- conditional INSERT needs it for. It is hand-scanned in shared.go rather
-- than sqlc-generated because sqlc's static analysis can't infer
-- nullability through a bare scalar subquery.
--
-- The five named queries below remain as the individually-callable,
-- sqlc-generated building blocks: GetOldestLiveLedgerEntry backs the
-- retryAt computation on the "insufficient budget" branch,
-- ExpireLedgerReservations/EvictAgedLedgerEntries/SumLedgerEntryCost/
-- ReserveLedgerEntry remain available for Reconcile and any future caller
-- that needs one step in isolation rather than the fused fast path.

-- name: GetOldestLiveLedgerEntry :one
-- Feeds the retryAt computation when acquire cannot reserve: retryAt =
-- oldest live entry's consumed_at + window. Reservations are excluded —
-- their eventual release time isn't knowable in advance, so §5.5's retryAt
-- formula is defined over settled/synthetic entries only.
SELECT consumed_at FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2 AND state != 'reserved'
 ORDER BY consumed_at ASC
 LIMIT 1;

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
-- values agree. Returns candidates oldest-first with their cost, so the
-- caller can accumulate exactly enough to close the gap rather than
-- guessing a row count up front; reservations are excluded — evicting an
-- in-flight reservation would silently forgive a cost that hasn't happened
-- yet.
SELECT entry_id, cost FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2 AND state != 'reserved'
 ORDER BY consumed_at ASC
 LIMIT sqlc.arg(max_evict);

-- name: DeleteLedgerEntryByID :exec
DELETE FROM app.esi_ledger_entry WHERE entry_id = $1;

-- name: RecordServerLedgerReading :exec
-- PHASE 20.4. Writes BOTH operands of esi_ledger_divergence in one
-- statement, under the bucket lock the reconciler already holds, because
-- §1.3's quantity is a difference and a difference between two moments is
-- not a measurement — see migration 00042 for the readings that forced
-- this, and ListLedgerDivergence below for what now consumes the pair.
--
-- local_remaining is the caller's PRE-CORRECTION availability: the number
-- reconcileAction is about to judge against server_remaining, computed one
-- statement earlier from SumSettledLedgerEntryCost inside this same
-- transaction. Storing the post-correction value instead would make the
-- gauge read ~0 by construction, since making the two agree is the whole
-- purpose of the statement that follows.
--
-- It is also measured against the ceiling the RECONCILER was given — the
-- server's own X-Ratelimit-Limit when it sent one — rather than against
-- the stored max_tokens ListLedgerDivergence used to use. Those agree
-- since Phase 20.3, but the server's reading and the ceiling it is a
-- remainder of arrived in the same response, and pairing them is one less
-- thing that can drift.
UPDATE app.esi_ledger_bucket
   SET server_remaining = $3,
       local_remaining_at_reading = sqlc.arg(local_remaining),
       server_observed_at = now(),
       updated_at = now()
 WHERE rate_limit_group = $1 AND user_key = $2;

-- ---- solo/clustered mode flush (§5.6 — both transitions must not lose or
-- double-count entries) ----

-- name: ListLedgerDivergence :many
-- PHASE 18 — the rate-limit dashboard's own query (roadmap Phase 18 edge
-- case: "surface esi_ledger_divergence prominently: sustained divergence
-- is the early warning for a Gate 1 failure").
--
-- `esi_ledger_divergence` is named in 01_ARCHITECTURE.md §16 and
-- 04_RELEASE_GATES.md §1.3 as a PROMETHEUS metric, and no metric surface
-- exists yet (internal/telemetry/metrics.go is a bare registry; the metric
-- set is Phase 20's, alongside the gate harnesses that read it). The
-- dashboard cannot wait for that and cannot derive divergence from
-- ListLedgerBuckets either: the bucket row carries the SERVER's reading
-- (server_remaining) but local headroom lives in app.esi_ledger_entry, so
-- the two have to be brought together, which is what this does.
--
-- Definitions match internal/esi/ratelimit's reconciler exactly:
--   local_consumed  = sum of live SETTLED/SYNTHETIC entry costs in this bucket
--   local_remaining = max_tokens - local_consumed, floored at zero
-- Gate 1.3's threshold is max(|local_remaining - server_remaining|) <= 1
-- per group.
--
-- PHASE 20.2: `state != 'reserved'` added, and it is load-bearing. It must
-- measure the SAME population SumSettledLedgerEntryCost reconciles against
-- (see that query for the measurement that forced this) — otherwise the
-- metric reports 5 per in-flight request on a perfectly healthy
-- installation, and Gate 1.3 becomes a measure of concurrency rather than
-- of ledger accuracy.
--
-- ── PHASE 20.4: local_consumed/local_remaining ARE NO LONGER THE GAUGE ───
-- They are computed HERE, at query time, and the row's server_remaining
-- was stored by whichever response last carried the header. Subtracting
-- them measures how much has been consumed since that response, which is
-- throughput; the 40-55 readings in migration 00042 are what that looks
-- like on a healthy installation under per-bucket concurrency.
--
-- b.local_remaining_at_reading is the fix: the local half as it stood at
-- the instant server_remaining was written, under the same lock (see
-- RecordServerLedgerReading). §1.3's divergence is that pair's difference
-- and nothing else. The live columns stay in the result because the ADMIN
-- BOARD wants them — "the ledger holds 47 right now, and the last time we
-- compared, we and the server agreed" are two different operator
-- questions, and answering only the second would hide current headroom.
--
-- The subtraction itself is deliberately NOT done here. Both stored
-- operands are nullable — the server has said nothing about this bucket
-- yet — and sqlc's static analysis types `abs(... - b.server_remaining)`
-- as a non-null bigint however the expression is cast or wrapped, so
-- scanning a genuine NULL would fail at runtime. Collapsing "never
-- observed" into "zero divergence" to dodge that would be the exact
-- empty-versus-unavailable confusion SRS §6 forbids: zero divergence is a
-- healthy reading, no reading is not. The operands come back typed
-- (*int32) and the callers subtract where the nil case is explicit.
SELECT b.rate_limit_group,
       b.user_key,
       b.max_tokens,
       b."window",
       b.server_remaining,
       b.local_remaining_at_reading,
       b.server_observed_at,
       b.updated_at,
       coalesce(e.local_consumed, 0)::bigint AS local_consumed,
       greatest(b.max_tokens - coalesce(e.local_consumed, 0), 0)::bigint AS local_remaining
  FROM app.esi_ledger_bucket b
  LEFT JOIN (
        SELECT rate_limit_group, user_key, sum(cost)::bigint AS local_consumed
          FROM app.esi_ledger_entry
         WHERE state != 'reserved'
         GROUP BY rate_limit_group, user_key
       ) e
    ON e.rate_limit_group = b.rate_limit_group AND e.user_key = b.user_key
 ORDER BY b.rate_limit_group, b.user_key;

-- name: ListLedgerBuckets :many
-- clustered -> solo: enumerate every bucket the shared table knows about so
-- the fast path can be primed for all of them before it engages, not just
-- the one the next request happens to touch.
SELECT * FROM app.esi_ledger_bucket;

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
