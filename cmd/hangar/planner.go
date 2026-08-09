package main

import (
	"context"
	"log/slog"

	"github.com/hangar-project/hangar/internal/sync/planner"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// startPlanner builds and runs Phase 6's leader-elected sync planner in its
// own goroutine, returning a stop function. Shared by `hangar schedule`
// (whose entire job is running the planner) and `hangar serve` (§2's
// "Single-process default": serve does everything, so a one-box
// installation doesn't have to run `schedule` separately).
//
// The River client here is deliberately insert-only (no Queues, no
// Workers, never Started) — Phase 6 only enqueues "sync_route" jobs;
// working them is Phase 7+'s route handlers. See
// internal/sync/planner.New's doc comment.
func startPlanner(ctx context.Context, connString string, pool *pgxpool.Pool, syncCfg planner.Config, logger *slog.Logger) (stop func(), err error) {
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, err
	}

	syncCfg.ConnString = connString
	p := planner.New(pool, riverClient, syncCfg, logger)

	plannerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := p.Run(plannerCtx); err != nil && plannerCtx.Err() == nil {
			logger.ErrorContext(ctx, "hangar: sync planner exited unexpectedly", "error", err)
		}
	}()

	return func() {
		cancel()
		<-done
	}, nil
}
