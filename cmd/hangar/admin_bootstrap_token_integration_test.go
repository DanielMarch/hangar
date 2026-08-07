//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestBootstrapTokenIssues (roadmap exit criterion): the CLI issues a
// working admin token — an admin user is created, the token is minted
// against it, and the printed secret's hash matches the stored row (i.e.
// the token as handed to the operator would actually authenticate).
func TestBootstrapTokenIssues(t *testing.T) {
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

	q := gen.New(pool)
	secret, err := bootstrapToken(ctx, q, "Test Bootstrap Admin", "bootstrap")
	require.NoError(t, err)

	parts := strings.SplitN(secret, ".", 2)
	require.Len(t, parts, 2, "issued secret must be token_id.secret")
	tokenID, rawSecretB64 := parts[0], parts[1]

	rawSecret, err := base64.RawURLEncoding.DecodeString(rawSecretB64)
	require.NoError(t, err)
	hash := sha256.Sum256(rawSecret)

	row, err := q.GetApiTokenByHash(ctx, hash[:])
	require.NoError(t, err, "the printed secret must hash to a row GetApiTokenByHash can find — the same lookup an authenticated request would perform")
	require.Equal(t, tokenID, row.TokenID.String())
	require.NotEmpty(t, row.Permissions, "a bootstrap token must carry a non-empty permission set")

	user, err := q.GetUser(ctx, row.UserID)
	require.NoError(t, err)
	require.True(t, user.IsAdmin, "the user a bootstrap token is issued for must be an admin")
}
