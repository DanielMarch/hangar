//go:build integration

package sso_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	poolCfg, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)
	poolCfg.MaxConns = 60
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	return pool
}

// TestConcurrentRefreshSerialisedByAdvisoryLock (roadmap exit criterion):
// 50 concurrent refreshes produce exactly one rotation.
func TestConcurrentRefreshSerialisedByAdvisoryLock(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	q := gen.New(pool)

	sso1 := newFakeSSOServer(t, "test-client-id")
	defer sso1.close()
	kr := testKeyring(t)

	user, err := q.CreateUser(ctx, "Test User")
	require.NoError(t, err)
	const characterID int64 = 2112625428
	_, err = q.UpsertCharacter(ctx, gen.UpsertCharacterParams{
		CharacterID: characterID, UserID: uuid.NullUUID{UUID: user.UserID, Valid: true}, Name: "Test Character", OwnerHash: "owner-hash-1",
	})
	require.NoError(t, err)

	sealed, err := crypto.Seal(kr, characterID, []byte("initial-refresh-token"))
	require.NoError(t, err)
	require.NoError(t, q.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID: characterID, KeyVersion: int32(sealed.KeyVersion), WrappedDek: sealed.WrappedDEK,
		Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext, OwnerHash: "owner-hash-1",
	}))
	// Force the stored token to look stale so every one of the 50
	// concurrent callers below independently decides "this needs a
	// refresh" the instant it acquires the lock — the scenario the
	// advisory lock + freshness-recheck must coalesce into one rotation.
	_, err = pool.Exec(ctx, `UPDATE app.character_token SET last_refreshed_at = now() - interval '1 hour' WHERE character_id = $1`, characterID)
	require.NoError(t, err)

	refresher := &sso.Refresher{
		Pool: pool,
		OAuth: sso.OAuthConfig{
			ClientID: sso1.clientID, ClientSecret: "test-secret",
			TokenURL: sso1.srv.URL + "/v2/oauth/token", HTTPClient: sso1.srv.Client(),
		},
		Keyring: kr,
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = refresher.RefreshCharacterToken(ctx, characterID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "attempt %d", i)
	}
	require.Equal(t, 1, sso1.callCount(), "50 concurrent refreshes must produce exactly one real SSO token exchange")

	tok, err := q.GetCharacterToken(ctx, characterID)
	require.NoError(t, err)
	require.True(t, tok.Valid)
}

// TestRefreshInvalidGrantInvalidatesAndDoesNotRetry proves §7.3's
// do-not-retry rule: an invalid_grant response marks the token invalid
// and is surfaced as a non-retryable error.
func TestRefreshInvalidGrantInvalidatesAndDoesNotRetry(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	q := gen.New(pool)

	sso1 := newFakeSSOServer(t, "test-client-id")
	defer sso1.close()
	kr := testKeyring(t)

	user, err := q.CreateUser(ctx, "Test User")
	require.NoError(t, err)
	const characterID int64 = 90000001
	_, err = q.UpsertCharacter(ctx, gen.UpsertCharacterParams{
		CharacterID: characterID, UserID: uuid.NullUUID{UUID: user.UserID, Valid: true}, Name: "Another Character", OwnerHash: "owner-hash-1",
	})
	require.NoError(t, err)
	sealed, err := crypto.Seal(kr, characterID, []byte("stale-refresh-token"))
	require.NoError(t, err)
	require.NoError(t, q.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID: characterID, KeyVersion: int32(sealed.KeyVersion), WrappedDek: sealed.WrappedDEK,
		Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext, OwnerHash: "owner-hash-1",
	}))
	_, err = pool.Exec(ctx, `UPDATE app.character_token SET last_refreshed_at = now() - interval '1 hour' WHERE character_id = $1`, characterID)
	require.NoError(t, err)

	sso1.forceNextGrantError("invalid_grant")

	var notified []int64
	refresher := &sso.Refresher{
		Pool: pool,
		OAuth: sso.OAuthConfig{
			ClientID: sso1.clientID, ClientSecret: "test-secret",
			TokenURL: sso1.srv.URL + "/v2/oauth/token", HTTPClient: sso1.srv.Client(),
		},
		Keyring:        kr,
		OnInvalidGrant: func(ctx context.Context, characterID int64) { notified = append(notified, characterID) },
	}

	err = refresher.RefreshCharacterToken(ctx, characterID)
	require.Error(t, err)
	require.True(t, sso.IsInvalidGrant(err))
	require.Contains(t, notified, characterID)

	tok, err := q.GetCharacterToken(ctx, characterID)
	require.NoError(t, err)
	require.False(t, tok.Valid)
	require.NotNil(t, tok.InvalidReason)
	require.Equal(t, "invalid_grant", *tok.InvalidReason)
}
