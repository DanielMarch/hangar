package ratelimit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// LedgerSolo is the in-process floating-window ledger: zero database
// round-trips per request, selected automatically when exactly one replica
// is live (§5.6). It is a cost-weighted expiry ledger of (cost,
// consumed_at) entries — deliberately NOT a continuous-refill token bucket
// (§5.5's prohibition; see TestLedgerFidelityAgainstFloatingWindow).
type LedgerSolo struct {
	shards []*shard
	clock  Clock
}

var _ Ledger = (*LedgerSolo)(nil)

// NewLedgerSolo constructs an empty in-process ledger. clock defaults to
// SystemClock when nil.
func NewLedgerSolo(clock Clock) *LedgerSolo {
	if clock == nil {
		clock = SystemClock
	}
	return &LedgerSolo{shards: newShards(), clock: clock}
}

func (l *LedgerSolo) getBucket(sh *shard, req AcquireRequest) *bucket {
	key := bucketKey(req.Group, req.UserKey)
	b, ok := sh.buckets[key]
	if !ok {
		b = newBucket(req.MaxTokens, req.Window)
		sh.buckets[key] = b
		return b
	}
	// Bucket config can drift when a route's advertised max-tokens/window
	// changes (reconciled from X-Ratelimit-Limit); keep it current. This
	// never touches the ledger/reserved contents.
	//
	// req.MaxTokens, never req.admissionCeiling(): what is STORED is the
	// route's real ceiling, exactly as in the clustered ledger's bucket
	// row. A per-caller reduction written here would be read back by
	// settledAvailable() on the next Reconcile and by flush.go on the next
	// mode transition, which is how the same fiction reached
	// app.esi_ledger_bucket before 20.3.
	b.maxTokens = req.MaxTokens
	b.window = req.Window
	return b
}

// Acquire implements Ledger.
func (l *LedgerSolo) Acquire(ctx context.Context, req AcquireRequest) (*Reservation, error) {
	sh := shardFor(l.shards, bucketKey(req.Group, req.UserKey))
	sh.mu.Lock()
	defer sh.mu.Unlock()

	b := l.getBucket(sh, req)
	now := l.clock.Now()
	b.evict(now)

	if b.admissionAvailable(req.admissionCeiling()) < int(CostReserved) {
		retryAt := l.retryAt(b, now, req.Window)
		return nil, &RetryAtError{RetryAt: retryAt}
	}

	id := uuid.New()
	deadline := now.Add(req.RequestTimeout)
	b.reserve(id, now, deadline)

	return &Reservation{
		EntryID:  id,
		Group:    req.Group,
		UserKey:  req.UserKey,
		IssuedAt: now,
		Deadline: deadline,
	}, nil
}

// retryAt implements §5.5's "compute retryAt = oldestLiveEntry.consumedAt +
// window" with a fallback for the case where only reservations (whose
// eventual release time isn't knowable) are holding the budget.
func (l *LedgerSolo) retryAt(b *bucket, now time.Time, window time.Duration) time.Time {
	if t, ok := b.oldestLiveConsumedAt(); ok {
		return t.Add(window)
	}
	if t, ok := b.earliestReservationDeadline(); ok {
		return t
	}
	return now.Add(window)
}

// Settle implements Ledger.
func (l *LedgerSolo) Settle(ctx context.Context, res *Reservation, cost int16, respondedAt time.Time) error {
	key := bucketKey(res.Group, res.UserKey)
	sh := shardFor(l.shards, key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	b, ok := sh.buckets[key]
	if !ok {
		// Bucket vanished (e.g. a mode-transition flush moved state
		// elsewhere between Acquire and Settle). Nothing local to
		// settle against; the flush is responsible for having carried
		// this reservation's fate along with it.
		return nil
	}
	b.settle(res.EntryID, cost, respondedAt)
	return nil
}

// Reconcile implements Ledger.
func (l *LedgerSolo) Reconcile(ctx context.Context, group, userKey string, maxTokens int, serverRemaining int) error {
	key := bucketKey(group, userKey)
	sh := shardFor(l.shards, key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	b, ok := sh.buckets[key]
	if !ok {
		return nil // nothing acquired against this bucket yet; nothing to reconcile
	}
	now := l.clock.Now()
	b.evict(now)

	// SETTLED availability, not available(): a reservation is HANGAR's
	// prediction of a request the server's X-Ratelimit-Remaining cannot yet
	// have counted, so including it makes local look lower than the server
	// and drives the reconciler to evict real consumption. See
	// db/queries/esi_ledger.sql's SumSettledLedgerEntryCost for the
	// oscillation this produced against real ESI.
	localAtReading := b.settledAvailable()
	inject, evictTarget, needsEvict := reconcileAction(maxTokens, localAtReading, serverRemaining)

	// PHASE 20.4.1: solo mode was INVISIBLE to esi_ledger_divergence.
	//
	// Both readers of that gauge (internal/telemetry's collector and the
	// admin rate-limit board) read app.esi_ledger_bucket, and nothing on
	// the solo path has ever written it: UpsertLedgerBucket is called by
	// the CLUSTERED acquire and by a mode-transition flush, and
	// RecordServerLedgerReading by the CLUSTERED reconcile. So at exactly
	// one live replica — the mode Gate 1's own N=1 run requires (§1.4,
	// condition 1.8) — the gate's metric emitted no samples at all, and
	// test/load/gate1_esi.go's maxDivergence over zero samples is 0, which
	// is a PASS. §3.1's warning about "zero dropped on an empty run" is the
	// same failure, one gate over.
	//
	// The fix is not to write the row from here — that would put a database
	// round-trip per response into the path whose entire purpose is not
	// having one. The reading is kept in memory and read at SCRAPE time,
	// which is how the collector reads every other gauge it owns.
	localAfter := localAtReading
	switch {
	case needsEvict:
		localAfter = b.evictOldestUntil(evictTarget)
	case inject > 0:
		b.injectSynthetic(int16(inject), now)
		localAfter -= inject
	}
	b.recordReading(reading{
		serverRemaining: serverRemaining,
		localAtReading:  localAtReading,
		localAfter:      localAfter,
		observedAt:      now,
		window:          b.window,
		maxTokens:       b.maxTokens,
	})
	return nil
}

// Readings snapshots every bucket's last reconcile pair, for the metrics
// collector. Locks each shard in turn to copy, never all at once — the same
// discipline Keys() follows.
func (l *LedgerSolo) Readings() []BucketReading {
	var out []BucketReading
	for _, sh := range l.shards {
		sh.mu.Lock()
		for key, b := range sh.buckets {
			if !b.hasReading {
				continue // never reconciled: no reading is not a reading of zero
			}
			group, userKey, _ := splitBucketKey(key)
			r := b.lastReading
			out = append(out, BucketReading{
				Group: group, UserKey: userKey,
				MaxTokens:       r.maxTokens,
				Window:          r.window,
				ServerRemaining: r.serverRemaining,
				LocalAtReading:  r.localAtReading,
				LocalAfter:      r.localAfter,
				ObservedAt:      r.observedAt,
			})
		}
		sh.mu.Unlock()
	}
	return out
}

// BucketReading is one bucket's last reconciliation, in the shape both
// ledger modes can report it: the clustered ledger persists these as
// app.esi_ledger_bucket columns, the solo ledger holds them in memory, and
// cmd/hangar's metrics wiring reads whichever the active mode owns.
type BucketReading struct {
	Group     string
	UserKey   string
	MaxTokens int
	Window    time.Duration

	ServerRemaining int
	LocalAtReading  int
	LocalAfter      int
	ObservedAt      time.Time
}

// BucketKey identifies one (group, userID) bucket.
type BucketKey struct {
	Group   string
	UserKey string
}

// Keys enumerates every bucket this ledger currently holds state for, used
// by the solo->clustered flush direction to know what to push. Briefly
// locks each shard in turn to copy its key set; it does not hold every
// shard's lock at once.
func (l *LedgerSolo) Keys() []BucketKey {
	var keys []BucketKey
	for _, sh := range l.shards {
		sh.mu.Lock()
		for key := range sh.buckets {
			group, userKey, _ := splitBucketKey(key)
			keys = append(keys, BucketKey{Group: group, UserKey: userKey})
		}
		sh.mu.Unlock()
	}
	return keys
}

func splitBucketKey(key string) (group, userKey string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '\x00' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// snapshot returns every live (cost, consumedAt) entry and reservation for
// the given bucket, for the solo->clustered flush direction (flush.go).
// entriesOnly excludes reservations that have already force-expired into
// ledger entries via evict — those are indistinguishable from any other
// settled entry by design.
func (l *LedgerSolo) snapshot(group, userKey string) (entries []ledgerEntry, reservations map[uuid.UUID]reservedEntry, maxTokens int, window time.Duration) {
	key := bucketKey(group, userKey)
	sh := shardFor(l.shards, key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	b, ok := sh.buckets[key]
	if !ok {
		return nil, nil, 0, 0
	}
	b.evict(l.clock.Now())
	entries = append(entries, b.ledger...)
	reservations = make(map[uuid.UUID]reservedEntry, len(b.reserved))
	for id, r := range b.reserved {
		reservations[id] = r
	}
	return entries, reservations, b.maxTokens, b.window
}

// load replaces a bucket's contents wholesale, for the clustered->solo
// flush direction: the shared table is read into memory before the fast
// path engages, so the resulting bucket must start from that snapshot
// rather than empty (§5.6).
func (l *LedgerSolo) load(group, userKey string, maxTokens int, window time.Duration, entries []ledgerEntry, reservations map[uuid.UUID]reservedEntry) {
	key := bucketKey(group, userKey)
	sh := shardFor(l.shards, key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	b := newBucket(maxTokens, window)
	for _, e := range entries {
		b.ledger = append(b.ledger, e)
	}
	for id, r := range reservations {
		b.reserved[id] = r
	}
	// Restore the heap invariant: entries were appended, not
	// heap.Push-ed, above (cheaper for a bulk load).
	fixHeap(&b.ledger)
	sh.buckets[key] = b
}
