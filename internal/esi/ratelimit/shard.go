package ratelimit

import (
	"runtime"
	"sync"
)

// FNV-1a 32-bit constants (hash/fnv's algorithm, inlined to avoid the
// per-call allocation hash.Hash's interface value costs — this runs twice
// per ledger operation, and BenchmarkLedgerSolo1MOperations budgets 2µs/op
// with the spec's own note that a preallocated heap should leave three
// orders of magnitude of headroom under that; an allocation here was most
// of the difference).
const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// numShards is NumCPU()*4 (§5.5's data-structure note), computed once at
// package init. A fixed shard count keeps each shard's mutex uncontended in
// the common case — different characters hash to different shards — without
// per-bucket locks, which would cost an allocation per bucket.
var numShards = runtime.NumCPU() * 4

// shard owns one lock and one slice of buckets. Buckets are looked up by
// the composite "group\x00userKey" key.
type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func newShards() []*shard {
	if numShards < 1 {
		numShards = 1
	}
	shards := make([]*shard, numShards)
	for i := range shards {
		shards[i] = &shard{buckets: make(map[string]*bucket)}
	}
	return shards
}

// bucketKey composes the (group, userID) key. The NUL separator can't
// appear in either component in practice (they come from route config and
// character/application IDs), so this can't collide the way a "+"- or
// ":"-joined key could.
func bucketKey(group, userKey string) string {
	return group + "\x00" + userKey
}

func shardFor(shards []*shard, key string) *shard {
	h := uint32(fnvOffset32)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= fnvPrime32
	}
	return shards[h%uint32(len(shards))]
}
