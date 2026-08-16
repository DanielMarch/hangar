package main

// workers.go owns the River worker pool, and it owns it for BOTH process
// roles. That is the whole point of the file.
//
// ── THE DEFECT THIS FILE CLOSES (B-6) ────────────────────────────────────
//
// `river.AddWorker` appeared in exactly one non-test file, cmd/hangar/work.go,
// and docker-compose.yml's only `hangar` service is `command: ["serve"]`.
// So on a stock installation:
//
//   - internal/sync/planner claimed due subscriptions every five seconds and
//     inserted `sync_route` jobs (claim.go);
//   - the only registered Worker for that kind was work.go's DispatchWorker;
//   - `work` was not running.
//
// Nothing ever worked them. The same held for `provision_urgent` and
// `provision_bulk`. The stack came up healthy, the migrations ran, the SPA
// served, /healthz was green — and no character, corporation or alliance
// was ever synchronised, and no platform group was ever provisioned. River
// simply accumulated rows in state `available` forever.
//
// 01_ARCHITECTURE.md §2 is not ambiguous about which side is wrong:
//
//	[DECISION] Single-process default. Gate 5 forbids operational
//	ceremony. `serve` does everything; `work`/`schedule` exist for
//	administrators who have outgrown one box.
//
// and `serve`'s own cobra Short string has always claimed an "in-process
// worker pool". THE FIX IS IN `serve`, NOT IN COMPOSE: Gate 5.5 requires
// the default profile to be exactly postgres + hangar + the one-shot
// migrate, so adding a `work` service would have failed a gate that
// currently passes on that condition, to work around a process that was
// meant to do this all along.
//
// It is the B20/B25 defect class in the largest place it can occur — a
// documented capability with no implementation in the process supposed to
// have it, invisible to every test because each one constructs the worker
// it needs — and it is the same lesson serve.go already carries twice, for
// §4.9's webhook pump and for Phase 21's housekeeping sweeper.
//
// ── WHY ONE FUNCTION AND NOT A COPY IN EACH ROLE ─────────────────────────
//
// Because a copy is how this happened. `work` grew the workers, `serve`
// grew the API, and the two drifted for six phases with every test green.
// A single assembly means a worker added for one role cannot be missing
// from the other: there is one AddWorker list, and both callers get it.
//
// ── TWO PROCESSES ON THE SAME QUEUES IS RIVER'S NORMAL MODE ──────────────
//
// A co-running `hangar work` stays exactly as valid as it was. River's
// producers claim with `FOR UPDATE SKIP LOCKED`; competing consumers on one
// queue is the topology it is built for, and is what "administrators who
// have outgrown one box" run. What changes is only that the box that has
// NOT been outgrown now works its own jobs.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/planner"
	"github.com/hangar-project/hangar/internal/sync/worker"
	"github.com/hangar-project/hangar/internal/telemetry"
)

// buildWorkerPool assembles the River client that consumes every queue
// HANGAR enqueues onto, with §9.2's budget table as its QueueConfig.
//
// The returned client is NOT started — the caller does that, after it has
// finished any wiring that needs to insert (wireRevocationTriggers), so a
// producer is never live before its own configuration is complete.
func buildWorkerPool(
	ctx context.Context,
	cfg *config.Config,
	pool *pgxpool.Pool,
	s *store.Store,
	gateway *esi.Client,
	refresher *sso.Refresher,
	revocations *telemetry.RevocationLatency,
	logger *slog.Logger,
) (*river.Client[pgx.Tx], error) {
	syncPolicy := sync.PolicyConfig{TTLFloor: cfg.ESI.TTLFloor, BackoffCap: cfg.Sync.BackoffCap}

	workers := river.NewWorkers()
	// River allows exactly one Worker per job Kind — Phase 7's
	// CharacterWorker, Phase 8's CorporationWorker and GlobalWorker and
	// Phase 20.8's AllianceWorker are all registered together behind
	// worker.DispatchWorker, which routes each "sync_route" job to the
	// matching entity_kind's worker. Each of the four still has its own
	// directly-callable Work method for tests.
	river.AddWorker(workers, &worker.DispatchWorker{
		Character: &worker.CharacterWorker{
			Pool: pool, Gateway: gateway, Tokens: refresher, Policy: syncPolicy,
		},
		Corporation: &worker.CorporationWorker{
			Pool: pool, Gateway: gateway, Tokens: refresher, Policy: syncPolicy,
			Elector: sync.DBElector{Store: s},
		},
		// PHASE 20.8 (capability #37): the fourth worker. Same elector — §6.3's
		// candidate ordering is about the character, not about what it acts
		// for; only the candidate POOL differs, and DBElector branches on the
		// entity kind for that.
		Alliance: &worker.AllianceWorker{
			Pool: pool, Gateway: gateway, Tokens: refresher, Policy: syncPolicy,
			Elector: sync.DBElector{Store: s},
		},
		Global: &worker.GlobalWorker{
			Pool: pool, Gateway: gateway, Policy: syncPolicy,
		},
	})

	// Phase 11: access provisioning's own queues. provision-urgent gets a
	// dedicated 32-worker pool per 01_ARCHITECTURE.md §9.2's budget table
	// so a nightly provision-bulk reconcile can never starve a revocation
	// — the two queues sharing a river.Client is fine (River schedules
	// per-queue independently); it is sharing a WORKER POOL that's
	// prohibited, and QueueConfig below gives each its own.
	drivers := provisioning.NewDrivers()
	if err := registerDiscordDriver(ctx, cfg, pool, drivers); err != nil {
		return nil, err
	}
	if err := registerTeamSpeakDriver(ctx, cfg, pool, drivers); err != nil {
		return nil, err
	}
	if err := registerMumbleDriver(ctx, cfg, pool, drivers, logger); err != nil {
		return nil, err
	}
	river.AddWorker(workers, &provisioning.UrgentWorker{Pool: pool, Drivers: drivers, Latency: revocations})
	river.AddWorker(workers, &provisioning.BulkWorker{Pool: pool, Drivers: drivers})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			planner.QueueSync:        {MaxWorkers: 20},
			provisioning.QueueUrgent: {MaxWorkers: 32},
			provisioning.QueueBulk:   {MaxWorkers: 8}, // matches .env.example's HANGAR_WORKER_QUEUES documented provision-bulk:8
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("hangar: building the river worker pool: %w", err)
	}
	return client, nil
}
