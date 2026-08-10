//go:build integration

package v1_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
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

func TestHandleMumbleAuthAllowsLinkedUser(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	platform, err := s.CreatePlatform(ctx, "mumble", "Test Mumble", []byte(`{}`))
	require.NoError(t, err)
	u, err := s.CreateUser(ctx, "Test Pilot "+uuid.NewString())
	require.NoError(t, err)

	remoteIdentity := "cert-hash-abc"
	require.NoError(t, s.UpsertProvisioningState(ctx, gen.UpsertProvisioningStateParams{
		PlatformID: platform.PlatformID, UserID: u.UserID, RemoteIdentity: &remoteIdentity,
		DesiredGroups: []string{}, ActualGroups: []string{},
	}))

	secret := "test-shared-secret"
	body, err := json.Marshal(v1.MumbleAuthRequest{CertificateHash: remoteIdentity})
	require.NoError(t, err)
	signature := sign(secret, body)

	result, err := v1.HandleMumbleAuth(ctx, s, secret, platform.PlatformID, body, signature, "192.0.2.1")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.Equal(t, u.DisplayName, result.Name)
}

func TestHandleMumbleAuthDeniesUnlinkedCertificate(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	platform, err := s.CreatePlatform(ctx, "mumble", "Test Mumble "+uuid.NewString(), []byte(`{}`))
	require.NoError(t, err)

	secret := "test-shared-secret"
	body, err := json.Marshal(v1.MumbleAuthRequest{CertificateHash: "never-linked"})
	require.NoError(t, err)
	signature := sign(secret, body)

	result, err := v1.HandleMumbleAuth(ctx, s, secret, platform.PlatformID, body, signature, "192.0.2.2")
	require.NoError(t, err)
	require.False(t, result.Allowed)
}

func TestHandleMumbleAuthRejectsBadSignature(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	platform, err := s.CreatePlatform(ctx, "mumble", "Test Mumble "+uuid.NewString(), []byte(`{}`))
	require.NoError(t, err)

	body, err := json.Marshal(v1.MumbleAuthRequest{CertificateHash: "x"})
	require.NoError(t, err)

	_, err = v1.HandleMumbleAuth(ctx, s, "real-secret", platform.PlatformID, body, "0000", "192.0.2.3")
	require.ErrorIs(t, err, v1.ErrMumbleAuthBadSignature)
}
