package cache

import (
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

// Entry is one cached ESI response, held identically in L1 and L2.
type Entry struct {
	ETag            string
	LastModified    time.Time
	HasLastModified bool
	Body            []byte
	Status          int
	ExpiresAt       time.Time
}

// entryOverheadBytes is a fixed per-entry cost added on top of body size,
// so a swarm of tiny (near-zero-body) responses — 304s cache almost
// nothing, and many small JSON objects are only a few hundred bytes —
// still counts for something against MaxCost. Without it, ristretto's
// admission policy would treat thousands of near-zero-cost entries as
// free, which is not the intended cost model ("cost-weighted by
// serialised body size" per 01_ARCHITECTURE.md §5.4 assumes a body is
// usually present, but every entry still occupies real map/counter space).
const entryOverheadBytes = 128

// L1 is the in-process ristretto tier.
type L1 struct {
	c *ristretto.Cache[string, Entry]
}

// NewL1 builds an L1 cache with the given cost budget in bytes.
// NumCounters follows ristretto's own guidance (~10x the expected item
// count); assuming a rough average entry size of 2KB, that is
// maxCostBytes/200, floored at a sane minimum so a tiny test budget still
// gets useful admission-policy accuracy.
func NewL1(maxCostBytes int64) (*L1, error) {
	numCounters := maxCostBytes / 200
	if numCounters < 1000 {
		numCounters = 1000
	}
	c, err := ristretto.NewCache(&ristretto.Config[string, Entry]{
		NumCounters: numCounters,
		MaxCost:     maxCostBytes,
		BufferItems: 64,
	})
	if err != nil {
		return nil, err
	}
	return &L1{c: c}, nil
}

// Get returns the cached entry for key, applying its own ExpiresAt on top
// of whatever TTL ristretto itself is tracking — belt and braces, since
// ristretto's TTL sweep is periodic, not immediate.
func (l *L1) Get(key string) (Entry, bool) {
	e, ok := l.c.Get(key)
	if !ok {
		return Entry{}, false
	}
	if time.Now().After(e.ExpiresAt) {
		return Entry{}, false
	}
	return e, true
}

// Set admits e into L1, cost-weighted by its serialised body size plus a
// fixed per-entry overhead, expiring at e.ExpiresAt.
func (l *L1) Set(key string, e Entry) {
	cost := int64(len(e.Body)) + entryOverheadBytes
	ttl := time.Until(e.ExpiresAt)
	if ttl <= 0 {
		return // already expired; do not admit
	}
	l.c.SetWithTTL(key, e, cost, ttl)
}

// Del evicts key from L1, e.g. after a torn-page-set discard makes a
// partially-written entry unsafe to serve.
func (l *L1) Del(key string) {
	l.c.Del(key)
}

// Wait blocks until ristretto's async admission buffer has drained — set
// operations are not immediately visible to Get without it. Production
// code never needs this (a brief window where a just-written entry isn't
// yet visible in L1 just means one extra L2/upstream round trip, which is
// correctness-neutral); tests that assert an immediate Get-after-Set do.
func (l *L1) Wait() {
	l.c.Wait()
}

// Close releases ristretto's background goroutines.
func (l *L1) Close() {
	l.c.Close()
}
