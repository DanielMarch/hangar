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
	RecordServerLedgerReading(ctx context.Context, rateLimitGroup string, userKey string, serverRemaining *int32) error
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
// through a scalar subquery). $1=group, $2=userKey, $3=requestTimeout.
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
     WHERE locked.max_tokens - used.total >= 5
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
	err := l.pool.QueryRow(ctx, acquireLedgerEntrySQL, req.Group, req.UserKey, req.RequestTimeout).Scan(
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
		remaining32 := int32(serverRemaining)
		if err := q.RecordServerLedgerReading(ctx, group, userKey, &remaining32); err != nil {
			return fmt.Errorf("record server reading: %w", err)
		}

		if needsEvict {
			return l.evictUntil(ctx, q, group, userKey, maxTokens, evictTarget)
		}
		if inject > 0 {
			if err := q.InsertSyntheticLedgerEntry(ctx, group, userKey, int16(inject)); err != nil {
				return fmt.Errorf("insert synthetic: %w", err)
			}
		}
		return nil
	})
}

// evictUntil deletes oldest non-reservation entries until availability
// reaches target (or there is nothing left to evict), accumulating exactly
// enough cost rather than guessing a row count.
func (l *LedgerClustered) evictUntil(ctx context.Context, q Store, group, userKey string, maxTokens, target int) error {
	for {
		used, err := q.SumSettledLedgerEntryCost(ctx, group, userKey)
		if err != nil {
			return fmt.Errorf("sum cost: %w", err)
		}
		if maxTokens-int(used) >= target {
			return nil
		}
		candidates, err := q.EvictOldestLedgerEntries(ctx, group, userKey, 16)
		if err != nil {
			return fmt.Errorf("evict oldest candidates: %w", err)
		}
		if len(candidates) == 0 {
			return nil // nothing left to evict; converge as far as possible
		}
		for _, c := range candidates {
			if err := q.DeleteLedgerEntryByID(ctx, c.EntryID); err != nil {
				return fmt.Errorf("delete evicted entry: %w", err)
			}
			used, err := q.SumSettledLedgerEntryCost(ctx, group, userKey)
			if err != nil {
				return fmt.Errorf("sum cost: %w", err)
			}
			if maxTokens-int(used) >= target {
				return nil
			}
		}
	}
}
