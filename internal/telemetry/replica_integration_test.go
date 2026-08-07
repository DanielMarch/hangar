//go:build integration

package telemetry_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestReplicaHeartbeatInertWithoutTable exercises the Phase 0 design note:
// the heartbeat loop must not error, panic, or block when app.esi_replica
// does not exist yet (it is created in Phase 1a).
func TestReplicaHeartbeatInertWithoutTable(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"),
		tcpostgres.WithUsername("hangar"),
		tcpostgres.WithPassword("hangar"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	defer pool.Close()

	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleServe, "test", slog.Default())

	runCtx, cancel := context.WithTimeout(ctx, 3*HeartbeatTestBudget())
	defer cancel()

	done := make(chan struct{})
	go func() {
		hb.Run(runCtx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReplicaHeartbeat.Run did not return after context cancellation")
	}
}

func HeartbeatTestBudget() time.Duration { return 500 * time.Millisecond }
