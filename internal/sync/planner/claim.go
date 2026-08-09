package planner

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
)

// ErrLostLeadership is returned by claimOnce when leadership was lost
// between claiming rows and committing the transaction. The caller (loop.go)
// treats this as "abort, don't complete, and re-acquire" — never as "retry
// the commit" (§6.1's edge case: losing the advisory lock mid-loop must
// abort the in-flight claim, not complete it).
var ErrLostLeadership = errors.New("planner: lost leadership mid-claim")

// ClaimResult reports what one claim tick did. SubscriptionIDs is exposed
// (not just a count) so both callers and tests can verify uniqueness
// across ticks/goroutines directly, rather than trusting a count alone.
type ClaimResult struct {
	SubscriptionIDs []uuid.UUID
}

// Claimed is the number of subscriptions this tick claimed.
func (r ClaimResult) Claimed() int { return len(r.SubscriptionIDs) }

// claimOnce runs exactly one claim-and-enqueue transaction:
//
//  1. SELECT ... FOR UPDATE SKIP LOCKED the due, unblocked, unsnoozed
//     subscriptions (db/queries/sync_subscription.sql ClaimDueSubscriptions
//     — blocked_by_pin and snoozed_until are excluded in that query's
//     predicate, never filtered here after the fact).
//  2. Lease them forward (LeaseSyncSubscriptions) so the very next tick
//     can't reclaim the same rows before a Phase 7+ worker records the
//     attempt's real outcome — the claim transaction's own first line of
//     defence against a duplicate enqueue.
//  3. Enqueue one River job per claimed row, in the SAME transaction
//     (River's unique-job option on (route_id, entity_kind, entity_id) is
//     the second line of defence, not the first).
//  4. Re-confirm leadership is still held — not just trust the state from
//     loop start — immediately before committing. If it isn't, roll back:
//     the in-flight claim is aborted, never completed.
func (p *Planner) claimOnce(ctx context.Context) (ClaimResult, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("planner: begin claim tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	q := gen.New(tx)
	rows, err := q.ClaimDueSubscriptions(ctx, p.cfg.ClaimBatchSize)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("planner: claim due subscriptions: %w", err)
	}
	if len(rows) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return ClaimResult{}, fmt.Errorf("planner: commit empty claim tx: %w", err)
		}
		committed = true
		return ClaimResult{}, nil
	}

	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.SubscriptionID
	}
	leasedUntil := p.now().Add(p.cfg.ClaimLease)
	if err := q.LeaseSyncSubscriptions(ctx, leasedUntil, ids); err != nil {
		return ClaimResult{}, fmt.Errorf("planner: lease claimed subscriptions: %w", err)
	}

	for _, r := range rows {
		args := SyncJobArgs{
			SubscriptionID: r.SubscriptionID,
			EntityKind:     sync.EntityKind(r.EntityKind),
			EntityID:       r.EntityID,
			RouteID:        r.RouteID,
		}
		if _, err := p.river.InsertTx(ctx, tx, args, nil); err != nil {
			return ClaimResult{}, fmt.Errorf("planner: enqueue subscription %s: %w", r.SubscriptionID, err)
		}
	}

	if !p.leader.StillHeld(ctx) {
		return ClaimResult{}, ErrLostLeadership
	}

	if err := tx.Commit(ctx); err != nil {
		return ClaimResult{}, fmt.Errorf("planner: commit claim tx: %w", err)
	}
	committed = true
	return ClaimResult{SubscriptionIDs: ids}, nil
}
