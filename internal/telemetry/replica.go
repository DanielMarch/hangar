package telemetry

import (
	"context"
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
// than writing it once now.
//
// ── THE "INERT UNTIL PHASE 1a" PATH IS GONE (PHASE 20.11) ────────────────
// This type used to probe `to_regclass('app.esi_replica')` on EVERY tick and
// skip silently at DEBUG when the table was absent, because Phase 0 shipped
// before Phase 1a created it. Migration 00006 has created it for nineteen
// phases; there is no supported schema without it.
//
// Carrying the branch past its reason was not free. It made this file
// disagree with the two OTHER readers of the same table about what a missing
// table means — GatewayCollector.Collect counts it as a scrape failure, and
// Governor 1 logs a warning and holds its current mode — so the same
// condition was simultaneously "supported and inert", "an error", and
// "a reason to keep guessing". Only one of those can be right, and the
// evidence says it is not this one: with the table absent, Governor 1 never
// leaves the solo mode it optimistically starts in, so EVERY replica
// believes it alone holds the full ESI error budget. That is precisely the
// hazard flushSoloToClustered exists to prevent, arrived at by a different
// road and with nothing logged above DEBUG.
//
// So the special case is deleted rather than re-levelled. A missing table is
// now an ordinary failed upsert, reported at WARN like any other, and the
// condition is caught where it can actually be acted on: db.MissingTables,
// run by `hangar migrate up` and at `serve` startup. Deleting the probe also
// removes a to_regclass round-trip per process per ten seconds, forever.
//
// Note what is NOT claimed: the heartbeat does not verify the schema itself.
// A writer that checks its own table exists before every write is a writer
// that cannot distinguish "not deployed yet" from "someone dropped it", and
// that ambiguity is what let this sit unnoticed.
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
	const upsert = `
		INSERT INTO app.esi_replica (replica_id, role, version, started_at, last_heartbeat)
		VALUES ($1, $2, $3, now(), now())
		ON CONFLICT (replica_id) DO UPDATE SET last_heartbeat = now(), version = EXCLUDED.version
	`
	if _, err := h.pool.Exec(ctx, upsert, h.replicaID, string(h.role), h.version); err != nil {
		h.log.WarnContext(ctx, "replica heartbeat: upsert failed", "error", err)
	}
}

// deregister removes this process's row on clean shutdown. Its error is
// deliberately discarded: the row ages out of the live window in
// LiveThreshold anyway, so a failed delete costs at most one stale replica
// for thirty seconds, and shutdown is the wrong moment to start logging.
func (h *ReplicaHeartbeat) deregister(ctx context.Context) {
	_, _ = h.pool.Exec(ctx, `DELETE FROM app.esi_replica WHERE replica_id = $1`, h.replicaID)
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
