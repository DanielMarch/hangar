package store

import (
	"context"

	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// esiCachePostgresAdapter adapts *Store (the generated gen.Queries) to
// internal/esi/cache.PostgresStore, so internal/esi/cache does not need to
// import internal/store/gen directly (keeping the cache package testable
// against a narrow interface, the same pattern internal/esi/catalogue's
// Store interface uses).
type esiCachePostgresAdapter struct {
	s *Store
}

// PostgresCacheL2 returns the Postgres-backed L2 cache tier for s.
func (s *Store) PostgresCacheL2() cache.PostgresStore {
	return &esiCachePostgresAdapter{s: s}
}

func (a *esiCachePostgresAdapter) GetEsiCacheEntry(ctx context.Context, cacheKey []byte) (cache.PostgresCacheRow, error) {
	row, err := a.s.GetEsiCacheEntry(ctx, cacheKey)
	if err != nil {
		return cache.PostgresCacheRow{}, err
	}
	return cache.PostgresCacheRow{
		Etag:         row.Etag,
		LastModified: row.LastModified,
		Body:         row.Body,
		Status:       row.Status,
		ExpiresAt:    row.ExpiresAt,
	}, nil
}

func (a *esiCachePostgresAdapter) UpsertEsiCacheEntry(ctx context.Context, arg cache.PostgresCacheEntryParams) error {
	return a.s.UpsertEsiCacheEntry(ctx, gen.UpsertEsiCacheEntryParams{
		CacheKey:     arg.CacheKey,
		Etag:         arg.Etag,
		LastModified: arg.LastModified,
		Body:         arg.Body,
		Status:       arg.Status,
		ExpiresAt:    arg.ExpiresAt,
	})
}
