package ratelimit

import (
	"container/heap"
	"time"

	"github.com/google/uuid"
)

// ledgerEntry is one settled/synthetic (cost, consumedAt) row in the
// in-process ledger (01_ARCHITECTURE.md §5.5's State table).
type ledgerEntry struct {
	cost       int16
	consumedAt time.Time
}

// entryHeap is a min-heap ordered by consumedAt — the oldest entry is
// always at index 0. It must be a heap, not a deque: settles arrive out of
// order (a slow request issued first can complete after a fast one issued
// second), so append order is not consumedAt order (§5.5).
type entryHeap []ledgerEntry

func (h entryHeap) Len() int            { return len(h) }
func (h entryHeap) Less(i, j int) bool  { return h[i].consumedAt.Before(h[j].consumedAt) }
func (h entryHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x interface{}) { *h = append(*h, x.(ledgerEntry)) }
func (h *entryHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// reservedEntry is one in-flight predictive reservation (§5.5's "reserved"
// slice), charged CostReserved (5) against availability until it settles or
// expires. issuedAt is carried (not just deadline) because a reservation
// that gets flushed to the shared table mid-flight (a solo->clustered mode
// transition) needs its consumed_at — the issue time, per the schema's
// convention for the 'reserved' state — not just its expiry.
type reservedEntry struct {
	issuedAt time.Time
	deadline time.Time
}

// bucket is the per-(group, userID) in-process ledger state. Not
// goroutine-safe on its own — callers hold the owning shard's mutex for the
// duration of any bucket access.
type bucket struct {
	maxTokens int
	window    time.Duration

	ledger entryHeap
	// reserved is keyed by entry ID so Settle can find and remove its
	// reservation directly rather than scanning.
	reserved map[uuid.UUID]reservedEntry

	// PHASE 20.4.1: the reconciler's own arithmetic, kept so that solo mode
	// can be MEASURED. These are the in-memory counterpart of
	// app.esi_ledger_bucket's server_remaining / local_remaining_at_reading
	// / local_remaining_after_reading — see reading below for why the solo
	// path could not be measured at all before this.
	lastReading reading
	hasReading  bool
}

// reading is one reconcile's before/after pair, as the solo ledger holds it.
// The clustered ledger stores the same three numbers as bucket-row columns;
// this struct exists so the two modes report the SAME metric rather than
// one of them reporting nothing.
type reading struct {
	serverRemaining int
	localAtReading  int
	localAfter      int
	observedAt      time.Time
	window          time.Duration
	maxTokens       int
}

func newBucket(maxTokens int, window time.Duration) *bucket {
	return &bucket{
		maxTokens: maxTokens,
		window:    window,
		// Preallocated so steady state allocates nothing (§5.5's
		// data-structure note): sized max_tokens+8, since live cost
		// can't exceed max_tokens by more than one worst-case
		// overdraw's worth of slack.
		ledger:   make(entryHeap, 0, maxTokens+8),
		reserved: make(map[uuid.UUID]reservedEntry, 8),
	}
}

// evict lazily retires everything no longer live, as of now:
//   - ledger entries whose window has elapsed are popped and discarded.
//   - reservations past their deadline are NOT discarded for free — each is
//     converted into a ledger entry charged the worst case, stamped at its
//     deadline, per §5.5's edge case ("must expire at the request timeout
//     and be charged the worst case — never silently reclaimed for free").
//     That entry then ages out of the window on the normal schedule above.
func (b *bucket) evict(now time.Time) {
	for id, r := range b.reserved {
		if !r.deadline.After(now) {
			delete(b.reserved, id)
			heap.Push(&b.ledger, ledgerEntry{cost: CostReserved, consumedAt: r.deadline})
		}
	}
	cutoff := now.Add(-b.window)
	for b.ledger.Len() > 0 && !b.ledger[0].consumedAt.After(cutoff) {
		heap.Pop(&b.ledger)
	}
}

// liveCost sums every currently-live cost: settled/synthetic ledger entries
// plus in-flight reservations (each counted at the worst case). Call evict
// first so this only sees live state.
func (b *bucket) liveCost() int {
	total := 0
	for _, e := range b.ledger {
		total += int(e.cost)
	}
	total += len(b.reserved) * int(CostReserved)
	return total
}

// available is max_tokens minus liveCost, per §5.5's definition. It can go
// negative when a run of 4XX responses has overdrawn the window — that is
// intentional (the predictive-reservation test proves it), so callers must
// clamp for display purposes only, never for the acquire decision itself.
func (b *bucket) available() int {
	return b.maxTokens - b.liveCost()
}

// admissionAvailable is available() measured against a caller-supplied
// admission ceiling — the solo counterpart of acquireLedgerEntrySQL's
// `least(locked.max_tokens, $4)`, and it clamps for the same reason: a
// caller may hold tokens back from itself, never grant itself more than the
// bucket has. ceiling <= 0 means "no reduction".
//
// This is the ONLY place a per-caller ceiling may be applied. b.maxTokens
// must stay the route's real ceiling, because settledAvailable() —
// Reconcile's view — is measured against it, and a fiction there desyncs
// the ledger from the server reading it exists to import. See
// AcquireRequest.AdmissionMaxTokens.
func (b *bucket) admissionAvailable(ceiling int) int {
	if ceiling <= 0 || ceiling > b.maxTokens {
		return b.available()
	}
	return ceiling - b.liveCost()
}

// settledCost sums only the live SETTLED/SYNTHETIC entries, excluding
// in-flight reservations. It is the reconciliation view (§5.5's "the server
// always wins"), never the acquire view: acquire must count reservations,
// which is the entire purpose of predictive reservation, while
// reconciliation must not, because the server's reading cannot include a
// request that has not finished. Mixing the two produced a permanent
// divergence on a live installation — see
// db/queries/esi_ledger.sql's SumSettledLedgerEntryCost.
func (b *bucket) settledCost() int {
	total := 0
	for _, e := range b.ledger {
		total += int(e.cost)
	}
	return total
}

// settledAvailable is available() over settled consumption only.
func (b *bucket) settledAvailable() int {
	return b.maxTokens - b.settledCost()
}

// oldestLiveConsumedAt returns the smallest consumedAt among live
// settled/synthetic ledger entries (reservations excluded — their release
// time isn't knowable in advance). ok is false when the ledger is empty.
func (b *bucket) oldestLiveConsumedAt() (t time.Time, ok bool) {
	if b.ledger.Len() == 0 {
		return time.Time{}, false
	}
	return b.ledger[0].consumedAt, true
}

// earliestReservationDeadline is the fallback retryAt source when the
// ledger is empty but reservations alone exhaust the budget.
func (b *bucket) earliestReservationDeadline() (t time.Time, ok bool) {
	for _, r := range b.reserved {
		if !ok || r.deadline.Before(t) {
			t, ok = r.deadline, true
		}
	}
	return t, ok
}

// reserve records a new in-flight reservation and returns its ID.
func (b *bucket) reserve(id uuid.UUID, issuedAt, deadline time.Time) {
	b.reserved[id] = reservedEntry{issuedAt: issuedAt, deadline: deadline}
}

// settle removes the reservation (if still present — it may already have
// been force-expired by evict, in which case its worst-case charge is
// already in the ledger and settling again would double-count) and records
// the observed cost at the response timestamp.
func (b *bucket) settle(id uuid.UUID, cost int16, respondedAt time.Time) {
	if _, ok := b.reserved[id]; !ok {
		// Already force-expired by evict (charged worst case there);
		// settling again must not double-charge.
		return
	}
	delete(b.reserved, id)
	heap.Push(&b.ledger, ledgerEntry{cost: cost, consumedAt: respondedAt})
}

// injectSynthetic adds a reconciliation-driven synthetic entry, expiring a
// full window from now (§5.5).
func (b *bucket) injectSynthetic(cost int16, now time.Time) {
	if cost <= 0 {
		return
	}
	heap.Push(&b.ledger, ledgerEntry{cost: cost, consumedAt: now})
}

// evictOldestUntil forgives oldest ledger entries (never reservations)
// until SETTLED availability reaches target, or the ledger is exhausted,
// and returns the availability it achieved. Settled, for the same reason
// Reconcile compares against settled: the target came from the server, and
// the server has not counted the in-flight reservations.
//
// PHASE 20.4.1: the boundary entry is REDUCED, not popped — the solo
// counterpart of ReduceLedgerEntryCost, and it must stay identical to it,
// because TestSoloAndClusteredConvergeIdentically compares the two ledgers
// against the same script. Mutating cost in place is safe: the heap is
// ordered by consumedAt, which this does not touch.
func (b *bucket) evictOldestUntil(target int) int {
	for {
		available := b.settledAvailable()
		deficit := target - available
		if deficit <= 0 || b.ledger.Len() == 0 {
			return available
		}
		head := b.ledger[0]
		if int(head.cost) <= deficit {
			heap.Pop(&b.ledger)
			continue
		}
		b.ledger[0].cost -= int16(deficit)
		return target
	}
}

// recordReading stores the reconciler's before/after pair so solo mode is
// visible to esi_ledger_divergence and esi_ledger_prediction_error.
func (b *bucket) recordReading(r reading) {
	b.lastReading, b.hasReading = r, true
}

// fixHeap restores the heap invariant after entries have been appended
// directly (a bulk load), cheaper than heap.Push-ing one at a time.
func fixHeap(h *entryHeap) {
	heap.Init(h)
}
