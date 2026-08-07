//go:build integration

package cache_test

import (
	"context"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestPostgresL2RoundTrip proves the UNLOGGED app.esi_cache_entry-backed
// L2 tier (db/queries/esi_cache.sql) reads back exactly what it wrote,
// through the same store.PostgresCacheL2() adapter Store.L2 wires up in
// production, and that a re-Set of identical data is the guarded no-op
// upsert every other Phase 1/2 write goes through.
func TestPostgresL2RoundTrip(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	s := store.New(pool)
	l2 := cache.NewPostgresL2(s.PostgresCacheL2(), nil)

	key := cache.Key(cache.KeyInput{Method: "GET", Path: "/characters/2112625428", CompatibilityDate: "2026-08-04", Tenant: "tranquility", ResolvedLanguage: "en", TokenSubject: "anonymous"})

	entry := cache.Entry{
		ETag: `"v1"`, Body: []byte(`{"name":"Test"}`), Status: 200,
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Millisecond),
	}
	l2.Set(ctx, key, entry)

	got, ok := l2.Get(ctx, key)
	require.True(t, ok, "expected the entry just written to be readable back")
	require.Equal(t, entry.ETag, got.ETag)
	require.Equal(t, entry.Body, got.Body)
	require.Equal(t, entry.Status, got.Status)
	require.WithinDuration(t, entry.ExpiresAt, got.ExpiresAt, time.Second)

	// A re-Set of identical data must be a no-op that doesn't error —
	// exercising the ON CONFLICT ... WHERE ... IS DISTINCT FROM guard
	// added to db/queries/esi_cache.sql in Phase 3.
	l2.Set(ctx, key, entry)
	got2, ok := l2.Get(ctx, key)
	require.True(t, ok)
	require.Equal(t, got.Body, got2.Body)

	// An expired entry must not be returned (the query's own
	// `expires_at > now()` filter).
	expiredKey := cache.Key(cache.KeyInput{Method: "GET", Path: "/expired", CompatibilityDate: "2026-08-04", Tenant: "tranquility", ResolvedLanguage: "en", TokenSubject: "anonymous"})
	l2.Set(ctx, expiredKey, cache.Entry{Body: []byte("x"), Status: 200, ExpiresAt: time.Now().Add(-time.Minute)})
	_, ok = l2.Get(ctx, expiredKey)
	require.False(t, ok, "an expired L2 entry must not be served")
}
