package planner

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
)

// advisoryLockName is the pg_try_advisory_lock key HANGAR's single planner
// leader election uses (01_ARCHITECTURE.md §6.1: "Leader election via
// pg_try_advisory_lock on a dedicated connection"). hashtext() runs
// Postgres-side against this literal — never reimplemented in Go, which
// could silently drift from Postgres's own hash function (the same reason
// internal/sso/refresh.go's advisory-lock key is built the same way).
const advisoryLockName = "hangar.planner"

// Leader holds HANGAR's planner leadership lock on a dedicated connection —
// never one drawn from the general pool. That distinction is the whole
// mechanism: losing THIS SPECIFIC connection (process crash, network
// partition, Postgres restart) is what makes failover automatic, because
// Postgres releases a session-scoped advisory lock the instant its owning
// session's connection goes away (§6.1: "Losing the connection releases
// the lock, so failover is automatic"). A lock acquired on a pooled
// connection couldn't offer this — the pool could hand that same backend
// to an unrelated caller the moment it's returned.
type Leader struct {
	// mu guards conn. *pgx.Conn is explicitly not safe for concurrent use
	// by multiple goroutines — production's Planner.Run is a strictly
	// sequential loop that never calls StillHeld/Release concurrently
	// with itself, but StillHeld is exported precisely so other callers
	// (a health/status endpoint, a test exercising the claim path from
	// several goroutines at once) can observe leadership too, and doing
	// so must not corrupt the one connection leadership lives on.
	mu   sync.Mutex
	conn *pgx.Conn
}

// TryAcquireLeader attempts to become leader on a brand-new dedicated
// connection. ok is false (with a nil *Leader and nil error) when another
// instance currently holds the lock — the normal, expected steady state
// for every non-leader replica, not a failure.
func TryAcquireLeader(ctx context.Context, connString string) (*Leader, bool, error) {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return nil, false, fmt.Errorf("planner: dedicated leader connection: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, advisoryLockName).Scan(&acquired); err != nil {
		_ = conn.Close(ctx)
		return nil, false, fmt.Errorf("planner: pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		_ = conn.Close(ctx)
		return nil, false, nil
	}
	return &Leader{conn: conn}, true, nil
}

// StillHeld reports whether this Leader's lock is still valid. The lock is
// scoped to Leader.conn's Postgres session, so checking connection health
// IS checking lock health — there is no separate "is the lock still mine"
// server-side state to query that isn't already implied by the connection
// being alive and un-superseded. A lightweight round trip is sufficient and
// is what claim.go re-checks immediately before committing a claim
// transaction (§6.1's edge case: losing leadership mid-loop must abort the
// in-flight claim, not complete it).
func (l *Leader) StillHeld(ctx context.Context) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return false
	}
	return l.conn.Ping(ctx) == nil
}

// Release closes the dedicated connection, which releases the advisory
// lock as a side effect (session-scoped locks die with the session — no
// separate pg_advisory_unlock call is needed or more correct). Safe to
// call on a nil Leader or more than once.
func (l *Leader) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil
	}
	err := l.conn.Close(ctx)
	l.conn = nil
	return err
}
