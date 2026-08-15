package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
)

// Store is the subset of gen.Querier the clustered ledger needs, declared
// narrowly against gen's own types — the same convention
// internal/esi/catalogue.Store uses — so *gen.Queries (and anything built on
// top of a pgx.Tx via gen.New) satisfies it with no adapter.
type Store interface {
	UpsertLedgerBucket(ctx context.Context, arg gen.UpsertLedgerBucketParams) error
	GetLedgerBucketForUpdate(ctx context.Context, rateLimitGroup string, userKey string) (gen.GetLedgerBucketForUpdateRow, error)
	ExpireLedgerReservations(ctx context.Context, rateLimitGroup string, userKey string) ([]gen.AppEsiLedgerEntry, error)
	EvictAgedLedgerEntries(ctx context.Context, rateLimitGroup string, userKey string, windowInterval time.Duration) error
	SumSettledLedgerEntryCost(ctx context.Context, rateLimitGroup string, userKey string) (int64, error)
	GetOldestLiveLedgerEntry(ctx context.Context, rateLimitGroup string, userKey string) (time.Time, error)
	ReserveLedgerEntry(ctx context.Context, rateLimitGroup string, userKey string, requestTimeout time.Duration) (gen.AppEsiLedgerEntry, error)
	SettleLedgerEntry(ctx context.Context, entryID uuid.UUID, cost int16) error
	InsertSyntheticLedgerEntry(ctx context.Context, rateLimitGroup string, userKey string, cost int16) error
	EvictOldestLedgerEntries(ctx context.Context, rateLimitGroup string, userKey string, maxEvict int32) ([]gen.EvictOldestLedgerEntriesRow, error)
	DeleteLedgerEntryByID(ctx context.Context, entryID uuid.UUID) error
	RecordServerLedgerReading(ctx context.Context, arg gen.RecordServerLedgerReadingParams) error
	RecordReconciledLedgerLocal(ctx context.Context, rateLimitGroup string, userKey string, localRemainingAfter *int32) error
	ReduceLedgerEntryCost(ctx context.Context, entryID uuid.UUID, reduceBy int16) error
	FlushLedgerEntriesForBucket(ctx context.Context, rateLimitGroup string, userKey string) ([]gen.AppEsiLedgerEntry, error)
	BulkInsertLedgerEntry(ctx context.Context, arg gen.BulkInsertLedgerEntryParams) error
	ListLedgerBuckets(ctx context.Context) ([]gen.AppEsiLedgerBucket, error)
}

// ClusterPool is the pgx handle the clustered ledger needs: Begin to open
// its own short transactions, plus the plain gen.DBTX surface so flush.go
// can run its (non-transactional — each call is already atomic) bucket
// enumeration and bulk writes directly against the pool. *pgxpool.Pool
// satisfies both.
type ClusterPool interface {
	gen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// LedgerClustered is the shared-Postgres floating-window ledger, selected
// automatically when two or more replicas are live (§5.6). Acquire is one
// short transaction that takes FOR UPDATE on the bucket row only — 01_
// ARCHITECTURE.md §5.6.
type LedgerClustered struct {
	pool ClusterPool
}

var _ Ledger = (*LedgerClustered)(nil)

// NewLedgerClustered constructs a clustered ledger backed by pool.
func NewLedgerClustered(pool ClusterPool) *LedgerClustered {
	return &LedgerClustered{pool: pool}
}

// directStore returns a Store bound straight to the pool (no explicit
// transaction) — used by flush.go, whose individual calls are each already
// atomic and don't need one giant cross-bucket transaction.
func (l *LedgerClustered) directStore() Store {
	return gen.New(l.pool)
}

// withTx runs fn inside a fresh transaction against a Store built from that
// transaction's handle, committing on success and rolling back on error.
func (l *LedgerClustered) withTx(ctx context.Context, fn func(ctx context.Context, q Store) error) error {
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ratelimit: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	if err := fn(ctx, gen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ratelimit: commit tx: %w", err)
	}
	return nil
}

// acquireLedgerEntrySQL is db/queries/esi_ledger.sql's documented
// AcquireLedgerEntry statement, kept as a hand-maintained const rather than
// a sqlc query — see that file's comment for the full rationale (one
// round-trip fused statement vs. sqlc's inability to infer nullability
// through a scalar subquery).
// $1=group, $2=userKey, $3=requestTimeout, $4=admission ceiling.
//
// PHASE 20.3 — WHY THE ADMISSION TEST TAKES A PARAMETER. The `ins` CTE used
// to test `locked.max_tokens - used.total >= 5`, i.e. it admitted against
// the STORED ceiling and nothing else. That made a per-caller ceiling
// (§4.4's char-notification reserve) expressible only by storing it, which
// turned the bucket's max_tokens into a fiction shared by every caller and
// produced a permanent, structural esi_ledger_divergence of 5 — see
// AcquireRequest.AdmissionMaxTokens for the whole mechanism.
//
// `least(locked.max_tokens, $4)` is the clamp, not a bare `$4`: the stored
// ceiling is the truth, a caller may hold tokens BACK from itself but may
// never grant itself more than the bucket actually has. It also keeps the
// statement correct when the stored ceiling has been reconciled DOWN from
// the server's own X-Ratelimit-Limit since the caller read the catalogue.
const acquireLedgerEntrySQL = `
WITH locked AS (
    SELECT b.max_tokens, b."window" FROM app.esi_ledger_bucket b
     WHERE b.rate_limit_group = $1 AND b.user_key = $2
       FOR UPDATE
), expired AS (
    UPDATE app.esi_ledger_entry e
       SET cost = 5, consumed_at = e.expires_at, state = 'settled', expires_at = NULL
     WHERE e.rate_limit_group = $1 AND e.user_key = $2
       AND e.state = 'reserved' AND e.expires_at < now()
    RETURNING 1
), aged AS (
    DELETE FROM app.esi_ledger_entry e
     WHERE e.rate_limit_group = $1 AND e.user_key = $2
       AND e.state != 'reserved'
       AND e.consumed_at <= now() - (SELECT locked."window" FROM locked)
    RETURNING 1
), used AS (
    SELECT coalesce(sum(e.cost), 0)::bigint AS total
      FROM app.esi_ledger_entry e
     WHERE e.rate_limit_group = $1 AND e.user_key = $2
), oldest AS (
    SELECT min(e.consumed_at) AS consumed_at
      FROM app.esi_ledger_entry e
     WHERE e.rate_limit_group = $1 AND e.user_key = $2 AND e.state != 'reserved'
), ins AS (
    INSERT INTO app.esi_ledger_entry (rate_limit_group, user_key, cost, consumed_at, state, expires_at)
    SELECT $1, $2, 5, now(), 'reserved', now() + $3::interval
      FROM locked, used
     WHERE least(locked.max_tokens, $4::int) - used.total >= 5
    RETURNING entry_id, consumed_at, expires_at
)
SELECT
    (SELECT max_tokens FROM locked)  AS max_tokens,
    (SELECT "window" FROM locked)    AS window,
    (SELECT total FROM used)         AS used_total,
    (SELECT consumed_at FROM oldest) AS oldest_consumed_at,
    (SELECT entry_id FROM ins)       AS entry_id,
    (SELECT consumed_at FROM ins)    AS reserved_at,
    (SELECT expires_at FROM ins)     AS reserved_expires_at
`

// acquireRow is acquireLedgerEntrySQL's result, scanned directly into
// pointer types so a genuinely NULL column (the "no bucket row yet"
// branch, or "insufficient budget" for the ins-CTE columns) never fails
// the Scan the way sqlc's inferred non-nullable types did.
type acquireRow struct {
	maxTokens         *int32
	window            *time.Duration
	usedTotal         int64
	oldestConsumedAt  *time.Time
	entryID           *uuid.UUID
	reservedAt        *time.Time
	reservedExpiresAt *time.Time
}

func (l *LedgerClustered) queryAcquire(ctx context.Context, req AcquireRequest) (acquireRow, error) {
	var row acquireRow
	err := l.pool.QueryRow(ctx, acquireLedgerEntrySQL, req.Group, req.UserKey, req.RequestTimeout, req.admissionCeiling()).Scan(
		&row.maxTokens, &row.window, &row.usedTotal, &row.oldestConsumedAt,
		&row.entryID, &row.reservedAt, &row.reservedExpiresAt,
	)
	return row, err
}

// Acquire implements Ledger. One round trip in the common case
// (acquireLedgerEntrySQL fuses the whole sequence from
// 02_DATABASE_SCHEMA.md §4.3 into a single atomic statement — see that
// const's comment); a bucket's very first-ever acquire costs a second
// round trip to create the row, then retries.
func (l *LedgerClustered) Acquire(ctx context.Context, req AcquireRequest) (*Reservation, error) {
	row, err := l.queryAcquire(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: acquire: %w", err)
	}

	// Two cases upsert-then-retry: no bucket row yet, or this call's
	// max_tokens/window (reconciled from the route's current
	// X-Ratelimit-Limit) has drifted from what's stored. Both are rare —
	// a route's advertised limit essentially never changes — so the
	// steady-state common case stays at exactly one round trip;
	// UpsertLedgerBucket's own guard (WHERE ... IS DISTINCT FROM) makes a
	// same-value call a cheap no-op besides.
	//
	// PHASE 20.3: "rare" is now true. req.MaxTokens is the route's REAL
	// ceiling for every caller, so two callers with different POLICIES for
	// the same bucket agree on what is stored and this branch stays cold.
	// Before the AdmissionMaxTokens split it was the background poller's
	// reduced ceiling that landed here, so an interactive caller and a
	// background one flipped max_tokens back and forth — each flip a real
	// write, on the hottest row in the system.
	needsUpsert := row.maxTokens == nil ||
		int(*row.maxTokens) != req.MaxTokens ||
		row.window == nil || *row.window != req.Window
	if needsUpsert {
		// The first attempt ran against whatever config was already
		// stored, and — if that was stale — may have admitted a
		// reservation the caller is about to lose track of (its handle
		// is discarded below in favour of the retry's). Roll that back
		// explicitly rather than leaking a live cost-5 charge with no
		// owner: EvictExpiredLedgerEntries the crashed-reservation test
		// this fix was found by proved leaks precisely this way.
		if row.entryID != nil && *row.entryID != uuid.Nil {
			if err := gen.New(l.pool).DeleteLedgerEntryByID(ctx, *row.entryID); err != nil {
				return nil, fmt.Errorf("ratelimit: acquire: rollback stale-config reservation: %w", err)
			}
		}
		if err := gen.New(l.pool).UpsertLedgerBucket(ctx, gen.UpsertLedgerBucketParams{
			RateLimitGroup: req.Group, UserKey: req.UserKey, MaxTokens: int32(req.MaxTokens), Window: req.Window,
		}); err != nil {
			return nil, fmt.Errorf("ratelimit: acquire: upsert bucket: %w", err)
		}
		row, err = l.queryAcquire(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("ratelimit: acquire (after bucket upsert): %w", err)
		}
	}

	if row.entryID == nil || *row.entryID == uuid.Nil {
		window := req.Window
		if row.window != nil {
			window = *row.window
		}
		retryAt := time.Now().Add(window)
		if row.oldestConsumedAt != nil {
			retryAt = row.oldestConsumedAt.Add(window)
		}
		return nil, &RetryAtError{RetryAt: retryAt}
	}

	return &Reservation{
		EntryID:  *row.entryID,
		Group:    req.Group,
		UserKey:  req.UserKey,
		IssuedAt: *row.reservedAt,
		Deadline: *row.reservedExpiresAt,
	}, nil
}

// Settle implements Ledger: a single UPDATE, no explicit transaction
// needed — one row's cost/state/consumed_at update is already atomic on
// its own.
func (l *LedgerClustered) Settle(ctx context.Context, res *Reservation, cost int16, respondedAt time.Time) error {
	if err := gen.New(l.pool).SettleLedgerEntry(ctx, res.EntryID, cost); err != nil {
		return fmt.Errorf("ratelimit: settle: %w", err)
	}
	// Note: consumed_at is re-stamped to now() by the query itself
	// (db/queries/esi_ledger.sql's SettleLedgerEntry), which — per §5.6 —
	// is the shared clock every replica agrees on. respondedAt is accepted
	// for interface parity with LedgerSolo but the database's own now()
	// is authoritative here, exactly as §5.6 specifies.
	return nil
}

// Reconcile implements Ledger — "the server always wins" (§5.5), run in one
// transaction against the locked bucket row.
func (l *LedgerClustered) Reconcile(ctx context.Context, group, userKey string, maxTokens int, serverRemaining int) error {
	return l.withTx(ctx, func(ctx context.Context, q Store) error {
		if _, err := q.GetLedgerBucketForUpdate(ctx, group, userKey); err != nil {
			if err == pgx.ErrNoRows {
				return nil // nothing acquired against this bucket yet
			}
			return fmt.Errorf("lock bucket: %w", err)
		}

		used, err := q.SumSettledLedgerEntryCost(ctx, group, userKey)
		if err != nil {
			return fmt.Errorf("sum cost: %w", err)
		}
		localAvailable := maxTokens - int(used)

		inject, evictTarget, needsEvict := reconcileAction(maxTokens, localAvailable, serverRemaining)

		// PHASE 20.4: both operands of esi_ledger_divergence, written
		// together, under the lock taken above, BEFORE the correction that
		// follows makes them agree. localAvailable is the same number
		// reconcileAction has just judged — that is the whole point. It is
		// floored at zero to match the reader's `greatest(..., 0)`
		// convention: a bucket that has over-consumed is out of headroom,
		// not in negative headroom, and the two readers must not disagree
		// about that at the boundary.
		//
		// Writing this AFTER the evict/inject below would store a pair that
		// agrees by construction — see db/queries/esi_ledger.sql's
		// RecordServerLedgerReading and migration 00042.
		remaining32 := int32(serverRemaining)
		local32 := int32(max(localAvailable, 0))
		if err := q.RecordServerLedgerReading(ctx, gen.RecordServerLedgerReadingParams{
			RateLimitGroup: group, UserKey: userKey,
			ServerRemaining: &remaining32, LocalRemaining: &local32,
		}); err != nil {
			return fmt.Errorf("record server reading: %w", err)
		}

		// localAfter is availability once the correction below has landed.
		// It is COMPUTED rather than re-summed: injection moves
		// availability by exactly the injected cost, and evictUntil returns
		// what it actually achieved. A second SumSettledLedgerEntryCost
		// would be a third round-trip inside the bucket lock to learn a
		// number this transaction already knows.
		localAfter := localAvailable
		switch {
		case needsEvict:
			achieved, err := l.evictUntil(ctx, q, group, userKey, maxTokens, evictTarget)
			if err != nil {
				return err
			}
			localAfter = achieved
		case inject > 0:
			if err := q.InsertSyntheticLedgerEntry(ctx, group, userKey, int16(inject)); err != nil {
				return fmt.Errorf("insert synthetic: %w", err)
			}
			localAfter -= inject
		}

		// PHASE 20.4.1: the other operand of esi_ledger_divergence as Gate
		// 1.3 now reads it — see RecordReconciledLedgerLocal and migration
		// 00043. Written LAST, still inside this transaction and still
		// under the lock taken above, so a reader can never see the
		// pre-correction pair refreshed beside a residual from a previous
		// reading.
		after32 := int32(max(localAfter, 0))
		if err := q.RecordReconciledLedgerLocal(ctx, group, userKey, &after32); err != nil {
			return fmt.Errorf("record reconciled local: %w", err)
		}
		return nil
	})
}

// evictUntil forgives oldest non-reservation consumption until availability
// reaches target (or there is nothing left to forgive), and returns the
// availability it achieved.
//
// ── PHASE 20.4.1: THE LAST ENTRY IS REDUCED, NOT DELETED ─────────────────
// This used to delete whole entries until availability REACHED target, so
// a cost-5 entry closing a 1-token gap forgave 4 tokens the server had not
// forgiven. That is the direction that causes breaches — HANGAR believing
// it holds headroom ESI has not granted — and it put a floor of up to 4
// under the gate's own metric. The boundary entry now has its cost reduced
// by exactly the remainder (ReduceLedgerEntryCost), keeping its consumed_at
// so it still ages out of the floating window on its own schedule.
func (l *LedgerClustered) evictUntil(ctx context.Context, q Store, group, userKey string, maxTokens, target int) (int, error) {
	for {
		used, err := q.SumSettledLedgerEntryCost(ctx, group, userKey)
		if err != nil {
			return 0, fmt.Errorf("sum cost: %w", err)
		}
		available := maxTokens - int(used)
		deficit := target - available
		if deficit <= 0 {
			return available, nil
		}
		candidates, err := q.EvictOldestLedgerEntries(ctx, group, userKey, 16)
		if err != nil {
			return 0, fmt.Errorf("evict oldest candidates: %w", err)
		}
		if len(candidates) == 0 {
			// Nothing left to forgive: converge as far as possible and
			// report it honestly. This is the one case esi_ledger_divergence
			// exists to catch, so returning `target` here would be the
			// vacuous pass 20.4 was right to fear — just in the other half
			// of the metric.
			return available, nil
		}
		for _, c := range candidates {
			if int(c.Cost) <= deficit {
				if err := q.DeleteLedgerEntryByID(ctx, c.EntryID); err != nil {
					return 0, fmt.Errorf("delete evicted entry: %w", err)
				}
				deficit -= int(c.Cost)
				available += int(c.Cost)
				if deficit == 0 {
					return available, nil
				}
				continue
			}
			if err := q.ReduceLedgerEntryCost(ctx, c.EntryID, int16(deficit)); err != nil {
				return 0, fmt.Errorf("reduce evicted entry: %w", err)
			}
			return target, nil
		}
	}
}
