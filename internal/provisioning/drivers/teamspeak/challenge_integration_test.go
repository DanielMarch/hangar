//go:build integration

package teamspeak_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/teamspeak"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
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

	return pool
}

// TestChallengeTokenSingleUse (roadmap exit criterion): verified then
// immediately consumed; second redemption fails.
func TestChallengeTokenSingleUse(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	u, err := s.CreateUser(ctx, "Test User "+uuid.NewString())
	require.NoError(t, err)

	challenge, err := teamspeak.IssueChallenge(ctx, s, u.UserID, time.Hour)
	require.NoError(t, err)
	require.False(t, challenge.ExpiresAt.IsZero())
	require.Nil(t, challenge.ConsumedAt)

	redeemed, err := teamspeak.RedeemChallenge(ctx, s, challenge.Token, "base64clientuid==")
	require.NoError(t, err)
	require.NotNil(t, redeemed.ConsumedAt)
	require.Equal(t, "base64clientuid==", *redeemed.ClientUniqueIdentifier)

	// Second redemption — same token, even a DIFFERENT uid — must fail.
	_, err = teamspeak.RedeemChallenge(ctx, s, challenge.Token, "a-different-uid==")
	require.ErrorIs(t, err, teamspeak.ErrChallengeAlreadyConsumed)

	// The row itself still reflects only the FIRST redemption — a failed
	// second attempt must never overwrite it.
	row, err := s.GetTeamspeakChallenge(ctx, challenge.Token)
	require.NoError(t, err)
	require.Equal(t, "base64clientuid==", *row.ClientUniqueIdentifier)
}

// TestChallengeTokenExpires: an expired, never-consumed token also fails
// redemption — expiry and consumption are both single-shot gates.
func TestChallengeTokenExpires(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	u, err := s.CreateUser(ctx, "Test User "+uuid.NewString())
	require.NoError(t, err)

	challenge, err := teamspeak.IssueChallenge(ctx, s, u.UserID, -time.Minute) // already expired
	require.NoError(t, err)

	_, err = teamspeak.RedeemChallenge(ctx, s, challenge.Token, "uid==")
	require.ErrorIs(t, err, teamspeak.ErrChallengeAlreadyConsumed)
}

// TestChallengeTokenUnknownFails: redeeming a token that was never issued
// fails the same way as an already-consumed one (no distinguishing
// timing/behavior — challenge.go's own doc comment).
func TestChallengeTokenUnknownFails(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	_, err := teamspeak.RedeemChallenge(ctx, s, "never-issued-token", "uid==")
	require.ErrorIs(t, err, teamspeak.ErrChallengeAlreadyConsumed)
}
