package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Replica heartbeat cadence (02_DATABASE_SCHEMA.md §"app.esi_replica"):
// heartbeat every 10s, a row is "live" under 30s old. Exactly one live
// replica selects `solo` mode; two or more select `clustered`. The mode
// itself is derived by Phase 4's ledger code from a COUNT() against this
// table — it is never read or cached here.
const (
	HeartbeatInterval = 10 * time.Second
	LiveThreshold     = 30 * time.Second
)

// Role is the process role recorded on the heartbeat row.
type Role string

const (
	RoleServe    Role = "serve"
	RoleWork     Role = "work"
	RoleSchedule Role = "schedule"
)

// ReplicaHeartbeat maintains this process's row in app.esi_replica.
//
// It lands in Phase 0 — rather than Phase 4, which first reads the table —
// because every process role must heartbeat from the very first release;
// retrofitting the loop into all three commands later is more disruptive
// than writing it once now. It is INERT until Phase 1a creates the table:
// every tick re-checks existence via to_regclass and simply skips when the
// table is absent, so `hangar serve` on a Phase-0-only schema neither errors
// nor blocks on a relation that doesn't exist yet.
type ReplicaHeartbeat struct {
	pool      *pgxpool.Pool
	replicaID uuid.UUID
	role      Role
	version   string
	interval  time.Duration
	log       *slog.Logger
}

// NewReplicaHeartbeat constructs a heartbeat loop for this process. version
// is the running binary's version string (main.version).
func NewReplicaHeartbeat(pool *pgxpool.Pool, role Role, version string, log *slog.Logger) *ReplicaHeartbeat {
	if log == nil {
		log = slog.Default()
	}
	return &ReplicaHeartbeat{
		pool:      pool,
		replicaID: uuid.New(),
		role:      role,
		version:   version,
		interval:  HeartbeatInterval,
		log:       log.With("component", "telemetry.replica_heartbeat", "role", string(role)),
	}
}

// Run blocks, heartbeating every interval until ctx is cancelled. Callers run
// it in its own goroutine (one per `serve` / `work` / `schedule` process).
func (h *ReplicaHeartbeat) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	h.beat(ctx) // heartbeat immediately on start; don't wait a full interval
	for {
		select {
		case <-ctx.Done():
			h.deregister(context.Background())
			return
		case <-ticker.C:
			h.beat(ctx)
		}
	}
}

func (h *ReplicaHeartbeat) beat(ctx context.Context) {
	exists, err := h.tableExists(ctx)
	if err != nil {
		h.log.WarnContext(ctx, "replica heartbeat: checking app.esi_replica existence failed", "error", err)
		return
	}
	if !exists {
		h.log.DebugContext(ctx, "replica heartbeat: app.esi_replica does not exist yet (Phase 1a); inert")
		return
	}

	const upsert = `
		INSERT INTO app.esi_replica (replica_id, role, version, started_at, last_heartbeat)
		VALUES ($1, $2, $3, now(), now())
		ON CONFLICT (replica_id) DO UPDATE SET last_heartbeat = now(), version = EXCLUDED.version
	`
	if _, err := h.pool.Exec(ctx, upsert, h.replicaID, string(h.role), h.version); err != nil {
		h.log.WarnContext(ctx, "replica heartbeat: upsert failed", "error", err)
	}
}

func (h *ReplicaHeartbeat) deregister(ctx context.Context) {
	exists, err := h.tableExists(ctx)
	if err != nil || !exists {
		return
	}
	_, _ = h.pool.Exec(ctx, `DELETE FROM app.esi_replica WHERE replica_id = $1`, h.replicaID)
}

// tableExists guards every tick on the table's existence (Phase 0 design
// note) rather than caching a one-time answer, so a heartbeat started before
// `hangar migrate up` finishes self-heals the moment 1a lands, with no
// process restart required.
func (h *ReplicaHeartbeat) tableExists(ctx context.Context) (bool, error) {
	var name *string
	err := h.pool.QueryRow(ctx, `SELECT to_regclass('app.esi_replica')::text`).Scan(&name)
	if err != nil {
		return false, fmt.Errorf("checking app.esi_replica: %w", err)
	}
	return name != nil, nil
}

// Hostname is a small helper used when composing replica log context; not
// stored on the row (§ schema has no hostname column) but useful for
// operator-facing log lines during Phase 0/1a bring-up.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
