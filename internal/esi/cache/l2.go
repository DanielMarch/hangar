package cache

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// L2 is the second cache tier: never authoritative (01_ARCHITECTURE.md
// §5.4). Both implementations below share that contract at the interface
// level — Get returns (Entry{}, false, nil) on any failure to read,
// including a Redis error, rather than propagating an error a caller might
// mistake for "the request itself failed".
type L2 interface {
	Get(ctx context.Context, key string) (Entry, bool)
	Set(ctx context.Context, key string, e Entry)
}

// ---- Postgres L2 (app.esi_cache_entry, UNLOGGED) ----

// PostgresStore is the subset of gen.Querier the Postgres L2 needs.
type PostgresStore interface {
	GetEsiCacheEntry(ctx context.Context, cacheKey []byte) (PostgresCacheRow, error)
	UpsertEsiCacheEntry(ctx context.Context, arg PostgresCacheEntryParams) error
}

// PostgresCacheRow and PostgresCacheEntryParams mirror
// internal/store/gen's AppEsiCacheEntry / UpsertEsiCacheEntryParams
// shapes structurally, so this package does not import internal/store/gen
// directly (keeping internal/esi/cache testable without pulling in the
// generated store layer) — internal/store's own facade adapts the real
// generated types to these at the call site.
type PostgresCacheRow struct {
	Etag         *string
	LastModified *time.Time
	Body         []byte
	Status       int16
	ExpiresAt    time.Time
}

type PostgresCacheEntryParams struct {
	CacheKey     []byte
	Etag         *string
	LastModified *time.Time
	Body         []byte
	Status       int16
	ExpiresAt    time.Time
}

type postgresL2 struct {
	store PostgresStore
	log   *slog.Logger
}

// NewPostgresL2 wraps store as an L2 tier.
func NewPostgresL2(store PostgresStore, log *slog.Logger) L2 {
	if log == nil {
		log = slog.Default()
	}
	return &postgresL2{store: store, log: log}
}

func (p *postgresL2) Get(ctx context.Context, key string) (Entry, bool) {
	keyBytes, err := decodeHexKey(key)
	if err != nil {
		return Entry{}, false
	}
	row, err := p.store.GetEsiCacheEntry(ctx, keyBytes)
	if err != nil {
		// A miss (no row, or expires_at <= now() per the query's own
		// WHERE clause) and a genuine database error are both simply "no
		// entry" from L2's point of view — L2 is never authoritative, so
		// neither case should ever fail the request.
		return Entry{}, false
	}
	e := Entry{
		Body:      row.Body,
		Status:    int(row.Status),
		ExpiresAt: row.ExpiresAt,
	}
	if row.Etag != nil {
		e.ETag = *row.Etag
	}
	if row.LastModified != nil {
		e.LastModified = *row.LastModified
		e.HasLastModified = true
	}
	return e, true
}

func (p *postgresL2) Set(ctx context.Context, key string, e Entry) {
	keyBytes, err := decodeHexKey(key)
	if err != nil {
		return
	}
	params := PostgresCacheEntryParams{
		CacheKey:  keyBytes,
		Body:      e.Body,
		Status:    int16(e.Status),
		ExpiresAt: e.ExpiresAt,
	}
	if e.ETag != "" {
		params.Etag = &e.ETag
	}
	if e.HasLastModified {
		lm := e.LastModified
		params.LastModified = &lm
	}
	if err := p.store.UpsertEsiCacheEntry(ctx, params); err != nil {
		// A write failure to a never-authoritative cache costs one extra
		// revalidation later; it must not fail the request that triggered it.
		p.log.WarnContext(ctx, "esi cache: postgres L2 write failed", "error", err)
	}
}

// ---- Redis L2 (optional; SRS Principle 7 — absent by default) ----

type redisL2 struct {
	client *redis.Client
	prefix string
	log    *slog.Logger
}

// NewRedisL2 wraps client as an L2 tier. Every operation degrades to a
// miss (Get) or a silent no-op (Set) on error — "a Redis error is logged
// and treated as a miss" (01_ARCHITECTURE.md §5.4's DECISION). Redis never
// becomes authoritative, symmetrically with the Postgres implementation.
func NewRedisL2(client *redis.Client, prefix string, log *slog.Logger) L2 {
	if log == nil {
		log = slog.Default()
	}
	return &redisL2{client: client, prefix: prefix, log: log}
}

type redisEntry struct {
	ETag            string    `json:"etag,omitempty"`
	LastModified    time.Time `json:"last_modified,omitempty"`
	HasLastModified bool      `json:"has_last_modified,omitempty"`
	Body            []byte    `json:"body"`
	Status          int       `json:"status"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func (r *redisL2) Get(ctx context.Context, key string) (Entry, bool) {
	raw, err := r.client.Get(ctx, r.prefix+key).Bytes()
	if err != nil {
		// redis.Nil (key absent) and every other error (connection
		// refused, timeout, ...) are both "a miss, never a request
		// failure" — that is the entire point of the DECISION this
		// implements. Only redis.Nil is expected in steady state; anything
		// else is logged so an operator can notice a misbehaving Redis
		// without HANGAR itself ever depending on it being healthy.
		if err != redis.Nil {
			r.log.WarnContext(ctx, "esi cache: redis L2 read failed, degrading to miss", "error", err)
		}
		return Entry{}, false
	}
	var re redisEntry
	if err := json.Unmarshal(raw, &re); err != nil {
		r.log.WarnContext(ctx, "esi cache: redis L2 entry unreadable, degrading to miss", "error", err)
		return Entry{}, false
	}
	if time.Now().After(re.ExpiresAt) {
		return Entry{}, false
	}
	return Entry(re), true
}

func (r *redisL2) Set(ctx context.Context, key string, e Entry) {
	raw, err := json.Marshal(redisEntry(e))
	if err != nil {
		r.log.WarnContext(ctx, "esi cache: redis L2 entry unencodable", "error", err)
		return
	}
	ttl := time.Until(e.ExpiresAt)
	if ttl <= 0 {
		return
	}
	if err := r.client.Set(ctx, r.prefix+key, raw, ttl).Err(); err != nil {
		r.log.WarnContext(ctx, "esi cache: redis L2 write failed", "error", err)
	}
}

func decodeHexKey(key string) ([]byte, error) {
	return hex.DecodeString(key)
}
