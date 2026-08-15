// Command gate2-revocation runs Gate 2 — Revocation SLO
// (docs/04_RELEASE_GATES.md §2) — and writes the p99 report from
// app.provisioning_audit that the gate blocks on.
//
// ── WHAT THE GATE ASKS FOR, AND HOW THIS DIFFERS FROM THE HARNESS TEST ───
// §2.1: 5000 identities across 3 platforms, p99 of the revocation latency
// under 60 seconds, measured WHILE A FULL BACKGROUND RECONCILIATION IS
// SATURATING THE BULK QUEUE. That last clause is the whole gate. Queue
// starvation is the realistic failure mode, so a measurement taken on an
// idle installation would be the one reading that cannot fail.
//
// test/load's integration suite works each urgent job directly, by design:
// it is testing the MEASUREMENT and does not want to depend on River's
// poll interval. This runner does the opposite and starts a real River
// client with both queues and their real worker budgets (32 urgent, 8
// bulk), because condition 2.4 — "provision-urgent is never starved by
// provision-bulk" — is a statement about the scheduler, and the only way
// to measure it is to let the scheduler schedule.
//
//	go run ./tools/gate2-revocation -duration=1h -version=v1.0.0-rc1
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/test/load"
	"github.com/hangar-project/hangar/tools/gaterun"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gate2-revocation: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		duration   = flag.Duration("duration", time.Hour, "wall-clock length of the run (§8: ~1h)")
		identities = flag.Int("identities", 5000, "identities per platform (§2.1: 5000)")
		version    = flag.String("version", "", "release version the evidence belongs to")
		outDir     = flag.String("out", "", "evidence directory (default docs/gate-evidence/<version>/gate2)")
		binary     = flag.String("binary", gaterun.DefaultBinary(), "path to the hangar binary (for migrations)")
		force      = flag.Bool("force", false, "run against a database whose name does not look like a gate database")
		notes      = flag.String("notes", "", "free text recorded in the summary")
	)
	flag.Parse()

	if *version == "" {
		return errors.New("-version is required")
	}
	dir, err := gaterun.EvidenceDir(*outDir, *version, "gate2")
	if err != nil {
		return err
	}

	cfg, err := config.Load(config.New())
	if err != nil {
		return fmt.Errorf("loading configuration (source .env first): %w", err)
	}
	dbURL := cfg.DB.URL.Reveal()
	if err := gaterun.GuardDatabase(dbURL, *force); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := gaterun.RunBinary(ctx, *binary, append(os.Environ(), "HANGAR_DB_URL="+dbURL), "migrate", "up"); err != nil {
		return fmt.Errorf("migrating the gate database: %w", err)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connecting to the gate database: %w", err)
	}
	defer pool.Close()

	// ── §2.1's three stub platforms ───────────────────────────────────────
	// Slow on purpose. A stub that answers instantly proves nothing: the
	// Discord bucket wait is a material part of the 60-second budget, and
	// removing it would measure a platform that does not exist.
	stubs := map[string]*load.RateLimitedStub{
		"discord":   load.NewRateLimitedStub(load.DiscordLimits),
		"teamspeak": load.NewRateLimitedStub(load.TeamSpeakLimits),
		"mumble":    load.NewRateLimitedStub(load.MumbleLimits),
	}

	world, err := seedWorld(ctx, pool, stubs, *identities)
	if err != nil {
		return err
	}
	fmt.Printf("gate2: seeded %d identities across %d platforms (%d provisioning states)\n",
		*identities, len(world.platforms), world.states)

	drivers := provisioning.NewDrivers()
	for _, p := range world.platforms {
		drivers.Register(p.id.String(), stubs[p.kind])
	}

	// ── the real scheduler, with the real queue budgets ───────────────────
	workers := river.NewWorkers()
	latency := &recordingLatency{}
	river.AddWorker(workers, &provisioning.UrgentWorker{Pool: pool, Drivers: drivers, Latency: latency})
	river.AddWorker(workers, &provisioning.BulkWorker{Pool: pool, Drivers: drivers})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			provisioning.QueueUrgent: {MaxWorkers: 32},
			provisioning.QueueBulk:   {MaxWorkers: 8},
		},
		Workers: workers,
	})
	if err != nil {
		return fmt.Errorf("building the River client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("starting the River client: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	since := time.Now()
	urgent := &provisioning.Urgent{River: client}

	// ── saturate the bulk queue for the whole run (§2.4) ──────────────────
	// A full reconcile of every platform, re-enqueued as it completes, so
	// the urgent path is measured against a busy installation rather than
	// an idle one. BulkJobArgs is ByArgs-unique per platform, so this
	// cannot pile up unboundedly — it keeps exactly one full reconcile per
	// platform in flight, which is what a real installation's nightly pass
	// looks like.
	bulkCtx, stopBulk := context.WithCancel(ctx)
	defer stopBulk()
	var bulkWG sync.WaitGroup
	bulkWG.Add(1)
	go func() {
		defer bulkWG.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			for _, p := range world.platforms {
				if err := urgent.EnqueuePlatformReconcile(bulkCtx, p.id); err != nil && bulkCtx.Err() == nil {
					fmt.Printf("gate2: enqueueing bulk reconcile failed: %v\n", err)
				}
			}
			select {
			case <-bulkCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Let the bulk queue actually fill before the first revocation, so the
	// measurement starts under load rather than racing it.
	select {
	case <-ctx.Done():
	case <-time.After(15 * time.Second):
	}

	// ── the urgent revocations ────────────────────────────────────────────
	fmt.Printf("gate2: enqueueing %d urgent revocations while bulk is saturated\n", len(world.users))
	enqueued := 0
	for _, userID := range world.users {
		if ctx.Err() != nil {
			break
		}
		// eventAt is NOW — §2.2 measures from the originating
		// entitlement-reducing event, so stamping it earlier would inflate
		// the latency and stamping it later would flatter it.
		if err := urgent.HandleUserChange(ctx, pool, userID, time.Now(), "gate2_run"); err != nil {
			return fmt.Errorf("enqueueing revocation for %s: %w", userID, err)
		}
		enqueued++
	}
	fmt.Printf("gate2: %d revocations enqueued; draining for up to %s\n", enqueued, *duration)

	// ── wait for the queue to drain, bounded by -duration ─────────────────
	drained := waitForDrain(ctx, pool, *duration)
	stopBulk()
	bulkWG.Wait()

	// ── the measurement ───────────────────────────────────────────────────
	// From the TABLE, with SQL, not from
	// provisioning_revocation_latency_seconds: a histogram answers a p99 by
	// interpolating inside a bucket and 60s is a bucket boundary, and a
	// gate that read the application's own instrumentation would be asking
	// the system whether it thinks it passed. The metric is reported
	// alongside as a cross-check.
	result, err := load.MeasureGate2(ctx, pool, load.Gate2Config{
		Since: since, SLO: 60 * time.Second, Percentile: 0.99,
		MinRevocations: *identities, OutputDir: dir,
		Notes: fmt.Sprintf("%d identities across %d platforms, bulk reconciliation saturated throughout", *identities, len(world.platforms)),
	})
	if err != nil {
		return fmt.Errorf("measuring: %w", err)
	}

	conditions := append([]load.ConditionResult{}, result.Conditions...)
	conditions = append(conditions, load.ConditionResult{
		ID:          "2.4",
		Description: "provision-urgent was never starved by provision-bulk — the p99 above was measured with bulk saturated",
		Passed:      world.bulkCompleted(ctx, pool) > 0 && drained,
		Measurement: fmt.Sprintf("%d bulk reconcile jobs completed during the run; urgent queue drained: %v", world.bulkCompleted(ctx, pool), drained),
	})

	if err := writeArtefacts(dir, result, latency, stubs); err != nil {
		return err
	}

	if err := load.WriteSummary(dir, load.Summary{
		Gate: "2", Name: "Revocation SLO", Version: *version,
		StartedAt: since, FinishedAt: time.Now(),
		Headline: fmt.Sprintf("p99 of (platform_call_completed_at − event_at) over %d successful revocations: %.3fs against a 60s SLO.",
			result.SampleCount, result.Latencies.P99),
		Conditions:  conditions,
		Environment: summaryEnvironment(world, *identities, *duration),
		Artefacts: map[string]string{
			"latency-report.json": "the full distribution (p50/p95/p99/max), outcome counts and pending revocations — the blocking artefact.",
			"latencies.csv":       "every measured revocation's latency in seconds, so the quantile can be recomputed independently.",
			"platform-calls.csv":  "per-platform grant and revoke counts served by the rate-limited stubs.",
			"conditions.json":     "the per-condition verdicts in machine-readable form.",
		},
		Notes: gate2Notes(*notes),
	}); err != nil {
		return err
	}

	for _, c := range conditions {
		verdict := "FAIL"
		if c.Passed {
			verdict = "pass"
		}
		fmt.Printf("  %-12s %-5s %s\n", c.ID, verdict, c.Measurement)
	}
	if !result.Passed() || !drained {
		return fmt.Errorf("GATE 2 FAILED — see %s", filepath.Join(dir, "SUMMARY.md"))
	}
	fmt.Printf("gate2: PASS — %s\n", filepath.Join(dir, "SUMMARY.md"))
	return nil
}

// recordingLatency captures what UrgentWorker observes, so the summary can
// report the metric beside the table as a cross-check. It is never the
// verdict.
type recordingLatency struct {
	mu       sync.Mutex
	outcomes map[string]int
	seconds  []float64
}

func (r *recordingLatency) ObserveRevocation(outcome string, seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outcomes == nil {
		r.outcomes = map[string]int{}
	}
	r.outcomes[outcome]++
	r.seconds = append(r.seconds, seconds)
}

func (r *recordingLatency) snapshot() (map[string]int, []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for k, v := range r.outcomes {
		out[k] = v
	}
	return out, append([]float64(nil), r.seconds...)
}

type platform struct {
	id       uuid.UUID
	kind     string
	groupRef string
}

type gate2World struct {
	platforms []platform
	users     []uuid.UUID
	states    int
}

// bulkCompleted counts finished provision_bulk jobs — the evidence that
// condition 2.4's "with bulk saturated" was true rather than asserted.
func (w *gate2World) bulkCompleted(ctx context.Context, pool *pgxpool.Pool) int {
	var n int
	_ = pool.QueryRow(ctx,
		`SELECT count(*)::int FROM river_job WHERE kind = $1 AND state = 'completed'`,
		provisioning.KindProvisionBulk).Scan(&n)
	return n
}

// seedWorld creates the three platforms and `identities` users, each linked
// on every platform and each already holding the group that is about to be
// revoked — so every revocation is a real loss rather than a no-op diff.
func seedWorld(ctx context.Context, pool *pgxpool.Pool, stubs map[string]*load.RateLimitedStub, identities int) (*gate2World, error) {
	s := store.New(pool)
	w := &gate2World{}

	for _, kind := range []string{"discord", "teamspeak", "mumble"} {
		p, err := s.CreatePlatform(ctx, kind, "Gate2 "+kind+" "+uuid.NewString(), []byte(`{}`))
		if err != nil {
			return nil, fmt.Errorf("gate2: creating platform %s: %w", kind, err)
		}
		groupRef := "gate2-group-" + kind
		if _, err := s.CreatePlatformGroup(ctx, p.PlatformID, groupRef, "Gate2 Group"); err != nil {
			return nil, fmt.Errorf("gate2: creating group on %s: %w", kind, err)
		}
		w.platforms = append(w.platforms, platform{id: p.PlatformID, kind: kind, groupRef: groupRef})
	}

	now := time.Now()
	for i := 0; i < identities; i++ {
		u, err := s.CreateUser(ctx, fmt.Sprintf("Gate2 User %d", i))
		if err != nil {
			return nil, fmt.Errorf("gate2: creating user %d: %w", i, err)
		}
		for _, p := range w.platforms {
			identity := fmt.Sprintf("gate2-%s-%d", p.kind, i)
			if err := s.UpsertProvisioningState(ctx, gen.UpsertProvisioningStateParams{
				PlatformID: p.id, UserID: u.UserID, RemoteIdentity: &identity,
				// Desired AND actual both hold the group. There are no
				// entitlement rules, so the next recompute desires nothing
				// and the diff is a genuine revocation of a group the
				// platform really holds.
				DesiredGroups: []string{p.groupRef}, ActualGroups: []string{p.groupRef},
				LinkedAt: &now, LastReconciledAt: &now,
			}); err != nil {
				return nil, fmt.Errorf("gate2: seeding provisioning state: %w", err)
			}
			w.states++
		}
		w.users = append(w.users, u.UserID)
		if (i+1)%500 == 0 {
			fmt.Printf("gate2: seeded %d/%d identities\n", i+1, identities)
		}
	}
	return w, nil
}

// waitForDrain blocks until no provision_urgent job is left available or
// running, or until the budget expires. It reports whether the queue
// actually drained: a run whose urgent queue was still backed up when the
// clock ran out has not demonstrated the SLO, and must not be summarised as
// though it had.
func waitForDrain(ctx context.Context, pool *pgxpool.Pool, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
		var outstanding int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)::int FROM river_job
			 WHERE kind = $1 AND state IN ('available','running','retryable','scheduled')`,
			provisioning.KindProvisionUrgent).Scan(&outstanding); err != nil {
			continue
		}
		if outstanding == 0 {
			return true
		}
	}
	return false
}

func writeArtefacts(dir string, result *load.Gate2Result, latency *recordingLatency, stubs map[string]*load.RateLimitedStub) error {
	if err := gaterun.WriteJSON(filepath.Join(dir, "latency-report.json"), result); err != nil {
		return err
	}
	if err := gaterun.WriteJSON(filepath.Join(dir, "conditions.json"), result.Conditions); err != nil {
		return err
	}

	outcomes, seconds := latency.snapshot()
	var b strings.Builder
	b.WriteString("seconds\n")
	for _, s := range seconds {
		fmt.Fprintf(&b, "%.6f\n", s)
	}
	if err := os.WriteFile(filepath.Join(dir, "latencies.csv"), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("writing latencies.csv: %w", err)
	}

	var calls strings.Builder
	calls.WriteString("platform,grants,revokes,min_interval,round_trip\n")
	for kind, stub := range stubs {
		grants, revokes := stub.Counts()
		fmt.Fprintf(&calls, "%s,%d,%d,%s,%s\n", kind, grants, revokes, stub.Limits.Interval, stub.Limits.Latency)
	}
	fmt.Fprintf(&calls, "# metric cross-check, provisioning_revocation_latency_seconds outcomes: %v\n", outcomes)
	if err := os.WriteFile(filepath.Join(dir, "platform-calls.csv"), []byte(calls.String()), 0o600); err != nil {
		return fmt.Errorf("writing platform-calls.csv: %w", err)
	}
	return nil
}

func summaryEnvironment(w *gate2World, identities int, duration time.Duration) map[string]string {
	return map[string]string{
		"Identities":          fmt.Sprint(identities),
		"Platforms":           fmt.Sprintf("%d (discord, teamspeak, mumble — rate-limited stubs)", len(w.platforms)),
		"Provisioning states": fmt.Sprint(w.states),
		"Queue budgets":       "provision-urgent 32 workers, provision-bulk 8 workers",
		"Drain budget":        duration.String(),
		"Host":                fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
	}
}

func gate2Notes(extra string) string {
	notes := "The verdict comes from app.provisioning_audit with SQL, never from " +
		"provisioning_revocation_latency_seconds. A Prometheus histogram answers a p99 by interpolating " +
		"within a bucket and 60s is exactly a boundary, and a gate that took its verdict from the " +
		"application's own instrumentation would be asking the system whether it thinks it passed. The " +
		"metric's outcome counts are recorded in platform-calls.csv as a cross-check.\n\n" +
		"Only SUCCESSFUL revocations are in the distribution. A failed platform call did not remove the " +
		"group — the exposure is still open — so counting how fast it failed as a revocation latency " +
		"would be the most flattering possible reading of the case the SLO exists to bound.\n\n" +
		"Conditions 2.2 (every trigger enqueues in the mutating transaction) and 2.5 (rolling back the " +
		"mutation rolls back the job) are not evaluated here. Both are statements about code paths rather " +
		"than about a run's numbers, and both are asserted directly by " +
		"test/load/gate2_integration_test.go and internal/provisioning's own suite. Reporting them as " +
		"passed because a run completed would be inventing evidence."
	if extra != "" {
		notes += "\n\n" + extra
	}
	return notes
}
