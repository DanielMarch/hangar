//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store"
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
	// PHASE 18: seeds too. bootstrapToken now places the new user in the
	// seeded `admin` role (see its doc comment), and `hangar migrate up`
	// applies seeds as part of the same command — so a test that ran
	// migrations without them was not reproducing a real installation.
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))

	q := gen.New(pool)
	secret, err := bootstrapToken(ctx, store.New(pool), "Test Bootstrap Admin", "bootstrap")
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

	// PHASE 18 (B21, second half). This test's own header has always
	// claimed the CLI "issues a WORKING admin token ... i.e. the token as
	// handed to the operator would actually authenticate" — but it only
	// ever checked that the hash matched a row. It did not, and could not:
	// nothing authenticated by token at all, and the user had no roles, so
	// the token 403'd on every guarded route even once it could
	// authenticate. Both halves are asserted for real now.
	s := store.New(pool)

	effective, err := s.GetEffectivePermission(ctx, row.UserID, domain.SuperuserPermission)
	require.NoError(t, err,
		"the bootstrap user's permissions must be MATERIALISED — RequirePermission reads "+
			"app.effective_permission and never recomputes live, so an unmaterialised grant does not exist")
	require.True(t, effective.Permitted)

	// And the full chain: the real middleware, the real permission guard,
	// the credential exactly as printed.
	handler := middleware.ResolveAPIToken(s)(
		middleware.ResolveSession(s)(
			middleware.RequirePermission(s, "admin.sync.view")(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sync/routes", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"the secret this command prints must actually authorise an administrative route — "+
			"that is the entire purpose of a bootstrap token on a fresh installation")
}
