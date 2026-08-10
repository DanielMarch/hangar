//go:build integration

package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
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
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))

	return pool
}

// TestRequirePermission covers the three outcomes authorize.go's own doc
// comment promises: 401 with no user in context, 403 with a user whose
// materialised permission is false (or never materialized at all — fail
// closed, not a live recompute), and success once RefreshUser has
// actually materialised an allow.
func TestRequirePermission(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	u, err := s.CreateUser(ctx, "Test User "+uuid.NewString())
	require.NoError(t, err)

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := middleware.RequirePermission(s, "characters.view")(next)

	t.Run("401 with no user in context", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.False(t, handlerCalled)
	})

	t.Run("403 for a user never materialized (fails closed, no live recompute)", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(middleware.WithUserID(ctx, u.UserID))
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.False(t, handlerCalled)
	})

	t.Run("403 for a user materialized but denied", func(t *testing.T) {
		require.NoError(t, rbac.RefreshUser(ctx, s, u.UserID)) // zero roles -> everything false
		handlerCalled = false
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(middleware.WithUserID(ctx, u.UserID))
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.False(t, handlerCalled)
	})

	t.Run("200 once a role grants the permission and materialisation ran", func(t *testing.T) {
		role, err := s.CreateRole(ctx, "view-characters-"+uuid.NewString(), nil, false)
		require.NoError(t, err)
		require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))
		require.NoError(t, rbac.AssignUserRole(ctx, pool, u.UserID, role.RoleID, uuid.NullUUID{}))

		handlerCalled = false
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(middleware.WithUserID(ctx, u.UserID))
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.True(t, handlerCalled)
	})
}
