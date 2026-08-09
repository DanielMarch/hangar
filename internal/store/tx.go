package store

import (
	"context"

	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
)

// Pool is the pgx handle WithTx needs: Begin to open the transaction, plus
// the plain gen.DBTX surface (unused by WithTx itself, but kept so a
// *pgxpool.Pool satisfies this with no adapter — the same narrow-interface
// convention internal/sso.RefreshPool and internal/esi/ratelimit.ClusterPool
// use).
type Pool interface {
	gen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// WithTx runs fn inside one transaction, committing on a nil return and
// rolling back otherwise (including on panic, via the deferred Rollback
// no-op-after-commit pattern used throughout this codebase — e.g.
// internal/sso/refresh.go). Phase 7's per-domain sync handlers use this so
// a domain's delete-stale-then-upsert sequence is atomic: a torn write
// (some rows updated, the prune half failed) must never be observable.
func WithTx(ctx context.Context, pool Pool, fn func(ctx context.Context, s *Store) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	if err := fn(ctx, New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
