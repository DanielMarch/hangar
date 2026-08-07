package ratelimit

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// flushSoloToClustered pushes every bucket LedgerSolo currently holds into
// the shared tables. Required before a solo->clustered transition admits
// any further request (§5.6): a request that lands in the shared ledger
// while some other request's cost is still sitting only in-process would
// let both replicas believe they alone hold the full budget.
//
// Idempotent: entry IDs are generated once per in-memory entry and carried
// through BulkInsertLedgerEntry's ON CONFLICT DO NOTHING, so re-running a
// flush that partially succeeded (e.g. after a retry) never double-counts.
func flushSoloToClustered(ctx context.Context, solo *LedgerSolo, store Store) error {
	for _, key := range solo.Keys() {
		entries, reservations, maxTokens, window := solo.snapshot(key.Group, key.UserKey)
		if maxTokens == 0 && window == 0 {
			continue // bucket vanished between Keys() and snapshot()
		}
		if err := store.UpsertLedgerBucket(ctx, gen.UpsertLedgerBucketParams{
			RateLimitGroup: key.Group, UserKey: key.UserKey, MaxTokens: int32(maxTokens), Window: window,
		}); err != nil {
			return fmt.Errorf("ratelimit: flush solo->clustered: upsert bucket %s/%s: %w", key.Group, key.UserKey, err)
		}
		for _, e := range entries {
			if err := store.BulkInsertLedgerEntry(ctx, gen.BulkInsertLedgerEntryParams{
				EntryID: uuid.New(), RateLimitGroup: key.Group, UserKey: key.UserKey,
				Cost: e.cost, ConsumedAt: e.consumedAt, State: "settled",
			}); err != nil {
				return fmt.Errorf("ratelimit: flush solo->clustered: entry %s/%s: %w", key.Group, key.UserKey, err)
			}
		}
		for id, r := range reservations {
			deadline := r.deadline
			if err := store.BulkInsertLedgerEntry(ctx, gen.BulkInsertLedgerEntryParams{
				EntryID: id, RateLimitGroup: key.Group, UserKey: key.UserKey,
				Cost: CostReserved, ConsumedAt: r.issuedAt, State: "reserved", ExpiresAt: &deadline,
			}); err != nil {
				return fmt.Errorf("ratelimit: flush solo->clustered: reservation %s/%s: %w", key.Group, key.UserKey, err)
			}
		}
	}
	return nil
}

// flushClusteredToSolo reads every bucket in the shared tables into a fresh
// LedgerSolo before the fast path engages (§5.6's other direction). Returns
// a populated *LedgerSolo rather than mutating one in place, so a caller can
// build it and swap it in atomically.
func flushClusteredToSolo(ctx context.Context, store Store, clock Clock) (*LedgerSolo, error) {
	solo := NewLedgerSolo(clock)

	buckets, err := store.ListLedgerBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: flush clustered->solo: list buckets: %w", err)
	}
	for _, bk := range buckets {
		rows, err := store.FlushLedgerEntriesForBucket(ctx, bk.RateLimitGroup, bk.UserKey)
		if err != nil {
			return nil, fmt.Errorf("ratelimit: flush clustered->solo: entries %s/%s: %w", bk.RateLimitGroup, bk.UserKey, err)
		}
		var entries []ledgerEntry
		reservations := make(map[uuid.UUID]reservedEntry)
		for _, r := range rows {
			if r.State == "reserved" {
				deadline := r.ConsumedAt // fallback if ExpiresAt is somehow nil
				if r.ExpiresAt != nil {
					deadline = *r.ExpiresAt
				}
				reservations[r.EntryID] = reservedEntry{issuedAt: r.ConsumedAt, deadline: deadline}
				continue
			}
			entries = append(entries, ledgerEntry{cost: r.Cost, consumedAt: r.ConsumedAt})
		}
		solo.load(bk.RateLimitGroup, bk.UserKey, int(bk.MaxTokens), bk.Window, entries, reservations)
	}
	return solo, nil
}
