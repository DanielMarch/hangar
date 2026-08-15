package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/housekeeping"
	"github.com/hangar-project/hangar/internal/store"
)

// buildHousekeeper assembles the retention sweeper, or returns a named
// error if its configuration is unsafe.
//
// The floor check is HERE rather than in internal/config/validate.go
// because that package deliberately imports none of the subsystems it
// configures, and restating housekeeping.MinReplicaRetention as a literal
// there would be a second copy of a constant whose entire job is to prevent
// a Governor 1 breach. This is still fail-fast at boot: runServe returns
// the error before the listener starts, so the process exits non-zero with
// a message naming the variable, per internal/config/validate.go's
// contract.
func buildHousekeeper(cfg *config.Config, s *store.Store) (*housekeeping.Sweeper, error) {
	if !housekeeping.SafeReplicaRetention(cfg.Housekeeping.ReplicaRetention) {
		return nil, fmt.Errorf(
			"HANGAR_REPLICA_RETENTION: %s is below the %s floor. app.esi_replica is the registry that "+
				"selects solo vs clustered ledger mode, and deleting a live replica's row because its "+
				"heartbeat was late makes every replica believe it is alone and spend the whole rate-limit "+
				"bucket (liveness threshold is %s)",
			cfg.Housekeeping.ReplicaRetention, housekeeping.MinReplicaRetention, housekeeping.LivenessThreshold)
	}
	return &housekeeping.Sweeper{Store: s, ReplicaRetention: cfg.Housekeeping.ReplicaRetention}, nil
}

// runHousekeeper sweeps on a timer until ctx is cancelled.
//
// ── WHY `serve` RUNS THIS AND NOT `work` ─────────────────────────────────
// `work` looks like the natural home — it is the background-job role, and
// every other periodic sweep in the system is registered alongside its
// River pool. It is the wrong answer here, and the reason is written in
// runServe two comments above the webhook dispatcher: THE STOCK
// docker-compose RUNS ONE HANGAR SERVICE, AND ITS COMMAND IS `serve`.
// There is no `work` service in it. Anything wired only into `work`
// therefore does not run on a default installation at all.
//
// That is not a hypothetical. It is exactly how §4.9's webhook outbox
// shipped write-only — rbac's mutations wrote app.outbox_event faithfully
// and the only process that drained it was one the default deployment
// never started. Wiring a retention job the same way would reproduce that
// defect in the same release that fixes it, and the symptom would again be
// a table growing on every installation while the code that empties it
// passes its tests.
//
// Every deployment runs `serve`: it is the API, the SPA and the planner.
// So `serve` is the process that can promise the sweep happens. Running it
// on several `serve` replicas at once is safe and needs no lock — three
// unconditional DELETEs of already-unreachable rows are idempotent, and
// unlike the planner there is no work to divide, only rows to remove.
func runHousekeeper(ctx context.Context, sweeper *housekeeping.Sweeper, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		logger.Info("housekeeping: disabled (HANGAR_HOUSEKEEPING_INTERVAL=0) — expired sessions, " +
			"expired ESI cache entries and dead replica registrations will not be deleted by this process")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Sweep once at boot as well as on the tick. A restart is the moment an
	// installation is most likely to be holding rows from a process that
	// died without cleaning up after itself, and with an hourly interval a
	// tick-only loop would leave them for an hour after every deploy.
	sweep(ctx, sweeper, logger)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(ctx, sweeper, logger)
		}
	}
}

func sweep(ctx context.Context, sweeper *housekeeping.Sweeper, logger *slog.Logger) {
	result, err := sweeper.Tick(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		// Logged with what DID get deleted: Tick sweeps sessions first and
		// returns partial progress alongside the error, so "the pass
		// failed" and "nothing was deleted" stay distinguishable.
		logger.Error("housekeeping: sweep failed", "error", err,
			"sessions_deleted", result.Sessions,
			"cache_entries_deleted", result.CacheEntries,
			"replicas_deleted", result.Replicas)
		return
	}
	if result.Total() > 0 {
		logger.Info("housekeeping: sweep",
			"sessions_deleted", result.Sessions,
			"cache_entries_deleted", result.CacheEntries,
			"replicas_deleted", result.Replicas)
	}
}
