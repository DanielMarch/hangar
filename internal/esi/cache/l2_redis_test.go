package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisL2ErrorDegradesToMiss — a Redis failure produces a cache miss,
// not a request error (Phase 3 exit criterion, 01_ARCHITECTURE.md §5.4's
// DECISION: "a Redis error is logged and treated as a miss").
func TestRedisL2ErrorDegradesToMiss(t *testing.T) {
	// A client pointed at a port nothing listens on: every operation
	// fails at the network level, deterministically and without needing a
	// real Redis instance in this environment.
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1, // do not let the client itself retry and slow the test down
	})
	t.Cleanup(func() { _ = client.Close() })

	l2 := NewRedisL2(client, "hangar:esi:", nil)
	ctx := context.Background()

	e, ok := l2.Get(ctx, "any-key")
	if ok {
		t.Error("Get against an unreachable Redis must report a miss (ok=false), not a hit")
	}
	if e.Body != nil {
		t.Error("a degraded miss must return a zero-value Entry")
	}

	// Set must not panic or otherwise propagate the failure — there is no
	// error return at all on this interface, by design (L2 is never
	// authoritative), so the only possible failure mode to test is "it
	// must not crash".
	l2.Set(ctx, "any-key", Entry{Body: []byte("x"), ExpiresAt: time.Now().Add(time.Minute)})

	// The Store-level integration: Get through the orchestrating Store
	// with only a broken Redis L2 configured (no L1 hit possible) must
	// still return a clean miss, never an error or panic — proving the
	// degrade-to-miss guarantee holds through the full Get path a real
	// gateway request would take, not just at the L2 interface directly.
	l1, err := NewL1(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l1.Close)
	s := &Store{L1: l1, L2: l2}
	if _, ok := s.Get(ctx, "any-key", "ttl-based"); ok {
		t.Error("Store.Get must miss cleanly when L2 (Redis) is unreachable")
	}
}
