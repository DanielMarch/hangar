//go:build integration

package telemetry_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestReplicaHeartbeatSurvivesAMissingTableWithoutSwallowingIt replaces
// TestReplicaHeartbeatInertWithoutTable, which asserted the Phase 0 design
// note that a missing app.esi_replica is a supported, inert state.
//
// ── WHY THE CONTRACT CHANGED (PHASE 20.11) ───────────────────────────────
// Migration 00006 has created the table since Phase 1a ended, so there is no
// supported schema without it, and the old test was pinning a branch whose
// reason had expired. Worse, that branch made this package disagree with the
// two other readers of the same table: GatewayCollector counts a missing
// table as a scrape failure, and Governor 1 warns and holds its mode. With
// the table gone, Governor 1 never leaves the solo mode it starts in, so
// every replica believes it alone holds the full ESI error budget — and the
// only trace was a DEBUG line.
//
// Both halves of the new contract matter, so both are asserted:
//
//  1. The loop still SURVIVES. A database problem must not take the process
//     down or wedge shutdown — that resilience was always right, and it is
//     what the old test actually measured.
//  2. It no longer stays SILENT. The failure is reported at WARN, where an
//     operator running with default log levels will see it.
func TestReplicaHeartbeatSurvivesAMissingTableWithoutSwallowingIt(t *testing.T) {
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

	// Wait for the server to actually accept connections. Without this the
	// first beat can fail with "failed to receive message: unexpected EOF"
	// instead of "relation app.esi_replica does not exist" — a WARN either
	// way, so the level assertion still passed, but the test would have been
	// measuring a connection race rather than the condition it names.
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil },
		20*time.Second, 250*time.Millisecond)

	// No migrations are run: app.esi_replica does not exist, which is exactly
	// the drifted state db.MissingTables now catches at boot.
	recorder := &levelRecorder{}
	hb := telemetry.NewReplicaHeartbeat(pool, telemetry.RoleServe, "test", slog.New(recorder))

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

	require.True(t, recorder.sawAtLeast(slog.LevelWarn),
		"a missing app.esi_replica must be reported at WARN or above; it was swallowed at DEBUG "+
			"until Phase 20.11 and that is how a dropped table went unnoticed while Governor 1 "+
			"silently held solo mode")
	require.Contains(t, recorder.text(), "esi_replica",
		"the report must name the table, or an operator cannot act on it")
}

// levelRecorder is a minimal slog.Handler that remembers what was logged.
// Enabled for every level so the test can prove the message is NOT merely a
// DEBUG line that a default logger would drop.
type levelRecorder struct {
	mu       sync.Mutex
	levels   []slog.Level
	messages []string
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *levelRecorder) Handle(_ context.Context, record slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.levels = append(r.levels, record.Level)
	message := record.Message
	record.Attrs(func(a slog.Attr) bool {
		message += " " + a.Key + "=" + a.Value.String()
		return true
	})
	r.messages = append(r.messages, message)
	return nil
}

func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *levelRecorder) WithGroup(string) slog.Handler      { return r }

func (r *levelRecorder) sawAtLeast(level slog.Level) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.levels {
		if l >= level {
			return true
		}
	}
	return false
}

func (r *levelRecorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.messages, "\n")
}

func HeartbeatTestBudget() time.Duration { return 500 * time.Millisecond }
