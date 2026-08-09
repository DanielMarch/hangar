// Package planner implements Phase 6's leader-elected claim loop:
// pg_try_advisory_lock leadership on a dedicated connection, a 5-second
// SELECT ... FOR UPDATE SKIP LOCKED claim, and a transactional River
// enqueue (01_ARCHITECTURE.md §6.1).
package planner

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// DefaultClaimInterval, DefaultClaimBatchSize, and DefaultClaimLease mirror
// .env.example's SYNC ENGINE defaults (internal/config.SyncConfig) and
// apply only when a caller constructs a Config with a zero value, so tests
// that only care about a couple of fields don't have to fill in every one.
const (
	DefaultClaimInterval  = 5 * time.Second
	DefaultClaimBatchSize = int32(500)
	DefaultClaimLease     = 2 * time.Minute
)

// Config bundles the planner's tunables. internal/config.SyncConfig maps
// onto this 1:1 (see cmd/hangar/planner.go's wiring); it is kept separate
// from config.SyncConfig so this package has no dependency on
// internal/config.
type Config struct {
	// ConnString is used for the dedicated advisory-lock connection —
	// deliberately separate from the pool used for claim transactions
	// (§6.1: "on a dedicated connection, not one drawn from the general
	// pool").
	ConnString     string
	ClaimInterval  time.Duration
	ClaimBatchSize int32
	ClaimLease     time.Duration
}

func (c Config) withDefaults() Config {
	if c.ClaimInterval <= 0 {
		c.ClaimInterval = DefaultClaimInterval
	}
	if c.ClaimBatchSize <= 0 {
		c.ClaimBatchSize = DefaultClaimBatchSize
	}
	if c.ClaimLease <= 0 {
		c.ClaimLease = DefaultClaimLease
	}
	return c
}

// Planner is the leader-elected claim loop. Exactly one Planner across the
// whole installation is ever actively claiming at a time — arbitrated by
// Postgres's advisory lock, not by anything in this type — so it is always
// safe to run one Planner per `schedule`/`serve` process.
type Planner struct {
	pool  *pgxpool.Pool
	river *river.Client[pgx.Tx]
	cfg   Config
	log   *slog.Logger

	leader *Leader
	nowFn  func() time.Time

	// OnClaim, if set, fires after every successful (non-empty, non-lost-
	// leadership) claim tick. It exists for metrics/observability hooks and
	// for tests that need to observe claim timing directly (e.g. asserting
	// the claim-lease interval invariant) rather than inferring it from
	// database state after the fact.
	OnClaim func(ClaimResult)
}

// New constructs a Planner. pool is the general connection pool claim
// transactions run on; riverClient must be a client with QueueSync among
// its InsertTx-reachable queues (an insert-only client — one built with no
// Queues/Workers — is sufficient; Phase 6 only enqueues, it never works
// the "sync_route" kind).
func New(pool *pgxpool.Pool, riverClient *river.Client[pgx.Tx], cfg Config, log *slog.Logger) *Planner {
	if log == nil {
		log = slog.Default()
	}
	return &Planner{
		pool:  pool,
		river: riverClient,
		cfg:   cfg.withDefaults(),
		log:   log.With("component", "sync.planner"),
		nowFn: time.Now,
	}
}

func (p *Planner) now() time.Time { return p.nowFn() }

// Run blocks until ctx is cancelled, alternating between "not leader — try
// to acquire" and "leader — claim every ClaimInterval". It always returns a
// non-nil error only when ctx itself ends; a lost race for leadership is
// not an error, just the normal steady state for every non-leader replica.
func (p *Planner) Run(ctx context.Context) error {
	defer func() {
		if p.leader != nil {
			_ = p.leader.Release(context.Background())
			p.leader = nil
		}
	}()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if p.leader == nil {
			leader, ok, err := TryAcquireLeader(ctx, p.cfg.ConnString)
			if err != nil {
				p.log.WarnContext(ctx, "acquiring leadership failed", "error", err)
			} else if ok {
				p.leader = leader
				p.log.InfoContext(ctx, "acquired planner leadership")
			}
			// !ok, err == nil: another instance holds the lock — normal.
			if p.leader == nil {
				if !p.sleep(ctx) {
					return ctx.Err()
				}
				continue
			}
		}

		result, err := p.claimOnce(ctx)
		switch {
		case errors.Is(err, ErrLostLeadership):
			p.log.WarnContext(ctx, "lost leadership mid-claim; aborted in-flight claim")
			_ = p.leader.Release(context.Background())
			p.leader = nil
		case err != nil:
			p.log.ErrorContext(ctx, "claim tick failed", "error", err)
		case result.Claimed() > 0:
			p.log.DebugContext(ctx, "claimed subscriptions", "count", result.Claimed())
			if p.OnClaim != nil {
				p.OnClaim(result)
			}
		}

		if !p.sleep(ctx) {
			return ctx.Err()
		}
	}
}

func (p *Planner) sleep(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(p.cfg.ClaimInterval):
		return true
	}
}
