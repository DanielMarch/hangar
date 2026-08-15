// Command gate1-load runs Gate 1 — ESI Load Stability
// (docs/04_RELEASE_GATES.md §1) — and writes the evidence artefacts §1.5
// requires.
//
// ── WHY THIS COMMAND EXISTS ──────────────────────────────────────────────
// The harness (test/load) has been complete since Phase 20.2 and has its
// own integration suite proving every part of it works. Nothing invoked it.
// It was reachable only from a Go test at 150-200ms durations, so the
// four-hour run 04_RELEASE_GATES.md §8 blocks the release on had never
// happened, and could not have been made to happen without writing this
// program first. A gate you cannot invoke is a gate that will not be run.
//
// ── WHAT ONE INVOCATION IS ───────────────────────────────────────────────
// One replica count. §1.4 requires TWO separately recorded results — N=1
// for the solo ledger and N=3 for the clustered one — so a full Gate 1 is
// two invocations, writing n1/ and n3/ beside each other.
//
//	go run ./tools/gate1-load -replicas=1 -duration=4h -version=v1.0.0-rc1
//	go run ./tools/gate1-load -replicas=3 -duration=4h -version=v1.0.0-rc1
//
// ── WHAT IT DRIVES, AND WHAT IT MEASURES ─────────────────────────────────
// It starts the recording proxy, seeds an installation of 5000 characters,
// starts N `hangar work` replicas pointed at the proxy, and hosts the real
// sync planner to enqueue their work. It then scrapes each replica's
// /metrics on a timer and asks test/load's harness for a verdict. The
// measurement is deliberately two-sided: the PROXY answers conditions 1.1
// and 1.7 because only the server can say what it admitted, and the
// INSTALLATION's metrics answer 1.2, 1.3, 1.4 and 1.8.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/sync/planner"
	"github.com/hangar-project/hangar/test/load"
	"github.com/hangar-project/hangar/tools/gaterun"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

func newRequest(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gate1-load: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		duration         = flag.Duration("duration", 4*time.Hour, "wall-clock length of the run (§1.1: 4h)")
		replicas         = flag.Int("replicas", 1, "replica count — §1.4 requires separate runs at 1 and 3")
		characters       = flag.Int("characters", 5000, "characters to seed (§1.1: 5000)")
		version          = flag.String("version", "", "release version the evidence belongs to, e.g. v1.0.0-rc1")
		outDir           = flag.String("out", "", "evidence directory (default docs/gate-evidence/<version>/gate1/n<replicas>)")
		binary           = flag.String("binary", gaterun.DefaultBinary(), "path to the hangar binary under test")
		scrape           = flag.Duration("scrape-interval", 15*time.Second, "how often to scrape each replica's /metrics")
		basePort         = flag.Int("metrics-base-port", 9400, "first metrics port; each replica takes the next")
		injectionSpacing = flag.Duration("injection-spacing", 2*time.Minute,
			"spacing between §1.3's adversarial conditions. They are compressed into the dense opening of the run rather than spread across it — see adversarialSchedule")
		force = flag.Bool("force", false, "run against a database whose name does not look like a gate database")
		notes = flag.String("notes", "", "free text recorded in environment.json")
	)
	flag.Parse()

	if *version == "" {
		return errors.New("-version is required: the evidence directory is named for the release it belongs to")
	}
	if *replicas < 1 {
		return errors.New("-replicas must be at least 1")
	}
	dir, err := gaterun.EvidenceDir(*outDir, *version, filepath.Join("gate1", fmt.Sprintf("n%d", *replicas)))
	if err != nil {
		return err
	}

	// The same loader the binary uses, so the gate runs against the
	// installation's real configuration rather than a second reading of the
	// environment that could disagree with it.
	cfg, err := config.Load(config.New())
	if err != nil {
		return fmt.Errorf("loading configuration (source .env first): %w", err)
	}
	dbURL := cfg.DB.URL.Reveal()
	if err := gaterun.GuardDatabase(dbURL, *force); err != nil {
		return err
	}
	if _, err := os.Stat(*binary); err != nil {
		return fmt.Errorf("hangar binary %q not found — build it first with `make build`: %w", *binary, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── the recording proxy, plus the two stubs an installation needs to
	// reach steady state without touching anything of CCP's ────────────────
	spec, _, err := catalogue.LoadEmbeddedSnapshot()
	if err != nil {
		return fmt.Errorf("loading the embedded spec snapshot: %w", err)
	}
	resolver, err := load.SpecRouteResolver(spec)
	if err != nil {
		return fmt.Errorf("building the route resolver: %w", err)
	}

	injector := load.NewInjector(adversarialSchedule(*injectionSpacing), nil)
	proxy := load.NewProxy(resolver, injector, nil)

	esiServer := httptest.NewServer(specServingProxy(proxy, spec))
	defer esiServer.Close()
	ssoServer := httptest.NewServer(http.HandlerFunc(stubSSOToken))
	defer ssoServer.Close()
	fmt.Printf("gate1: recording proxy at %s, stub SSO at %s\n", esiServer.URL, ssoServer.URL)

	// ── the installation ──────────────────────────────────────────────────
	env := replicaEnv(dbURL, esiServer.URL, ssoServer.URL)
	if err := gaterun.RunBinary(ctx, *binary, env, "migrate", "up"); err != nil {
		return fmt.Errorf("migrating the gate database: %w", err)
	}
	if err := gaterun.RunBinary(ctx, *binary, env, "admin", "ingest-catalogue"); err != nil {
		return fmt.Errorf("ingesting the route catalogue: %w", err)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connecting to the gate database: %w", err)
	}
	defer pool.Close()

	keyring, err := crypto.NewKeyring(cfg.Crypto)
	if err != nil {
		return fmt.Errorf("building the keyring: %w", err)
	}
	world, err := seedWorld(ctx, pool, keyring, *characters)
	if err != nil {
		return err
	}

	// A run inherits nothing from the last one. app.esi_replica in
	// particular must hold exactly the replicas this run starts, or mode
	// selection reads a count that includes the corpses of the previous
	// run — which is the difference between solo and clustered at N=1.
	if _, err := pool.Exec(ctx, `DELETE FROM app.esi_replica`); err != nil {
		return fmt.Errorf("clearing the replica registry: %w", err)
	}

	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return fmt.Errorf("creating the log directory: %w", err)
	}
	fleet := newFleet(*binary, logDir, *basePort, env)
	defer fleet.stopAll()
	if err := fleet.start(ctx, *replicas); err != nil {
		return err
	}

	// The planner, hosted here rather than run as a process — see
	// replicas.go's header for why a heartbeating planner would make the
	// N=1 run select clustered mode and silently invalidate half the gate.
	stopPlanner, err := startPlanner(ctx, dbURL, pool, planner.Config{
		ClaimInterval:  cfg.Sync.PlannerInterval,
		ClaimBatchSize: cfg.Sync.ClaimBatchSize,
		ClaimLease:     cfg.Sync.ClaimLease,
	}, logger)
	if err != nil {
		return fmt.Errorf("starting the planner: %w", err)
	}
	defer stopPlanner()

	// ── §1.4's mid-run transition ─────────────────────────────────────────
	// Once per run, kill one replica and restart it. Scheduled at the
	// halfway mark so both sides of the transition carry enough samples to
	// be readable in divergence.csv.
	transitionAt := *duration / 2
	transitionDone := make(chan struct{})
	go func() {
		defer close(transitionDone)
		select {
		case <-ctx.Done():
			return
		case <-time.After(transitionAt):
		}
		victim := *replicas - 1
		fleet.note("pre_transition", fmt.Sprintf("about to kill replica %d of %d", victim, *replicas))
		if err := fleet.kill(victim); err != nil {
			logger.Error("gate1: killing a replica failed", "error", err)
			return
		}
		// Long enough for the dead replica's registration to age out of
		// telemetry.LiveThreshold (30s) and for the survivors to observe
		// the new count.
		select {
		case <-ctx.Done():
			return
		case <-time.After(90 * time.Second):
		}
		if err := fleet.restart(ctx, victim); err != nil {
			logger.Error("gate1: restarting a replica failed", "error", err)
		}
	}()

	// ── the measurement ───────────────────────────────────────────────────
	fmt.Printf("gate1: running for %s at N=%d — evidence to %s\n", *duration, *replicas, dir)
	started := time.Now()
	result, err := load.Run(ctx, load.Config{
		Duration:          *duration,
		Replicas:          *replicas,
		MetricsURLs:       fleet.metricsURLs(),
		ScrapeInterval:    *scrape,
		OutputDir:         dir,
		ErrorLimitPauseAt: cfg.ESI.ErrorLimitPauseAt,
		Notes:             environmentNotes(cfg, world, *characters, *notes),
	}, proxy)
	if err != nil {
		return fmt.Errorf("running the harness: %w", err)
	}
	<-transitionDone

	if err := fleet.writeTransitionLog(dir); err != nil {
		return err
	}

	// §1.4's transition is a pass condition of the run, not an optional
	// extra: a run that never killed a replica has not demonstrated that
	// mode selection follows the registry, and must not report that it has.
	killed, restarted := fleet.transitionCount()
	conditions := append([]load.ConditionResult{}, result.Conditions...)
	conditions = append(conditions, load.ConditionResult{
		ID:          "1.4-transition",
		Description: "the mid-run replica kill and restart happened (§1.4)",
		Passed:      killed == 1 && restarted == 1,
		Measurement: fmt.Sprintf("%d killed, %d restarted; recorded in transition-log.jsonl", killed, restarted),
	})

	if err := load.WriteSummary(dir, load.Summary{
		Gate: "1", Name: "ESI Load Stability", Version: *version,
		StartedAt: started, FinishedAt: time.Now(),
		Headline: fmt.Sprintf(
			"%s at N=%d against the recording proxy: %d characters, %d enabled sync subscriptions.",
			duration.String(), *replicas, world.Characters, world.Subscriptions),
		Conditions:  conditions,
		Environment: summaryEnvironment(cfg, world, *replicas, *duration),
		Artefacts: map[string]string{
			"breaches.json":             "Governor 1 violations recorded by the proxy. §1.2 condition 1.1 requires this to be EMPTY.",
			"divergence.csv":            "per-minute, per-group max ledger divergence (the post-reconciliation residual, bound 0) beside max prediction error (recorded, unbounded).",
			"aggregate-consumption.csv": "proxy-side view of total consumption per bucket — condition 1.7's evidence at N>1.",
			"adversarial-log.jsonl":     "every §1.3 condition the proxy injected and the response it produced.",
			"transition-log.jsonl":      "§1.4's mid-run replica kill and restart.",
			"conditions.json":           "the per-condition verdicts in machine-readable form.",
			"environment.json":          "§0 rule 3's environment record.",
			"metrics.prom":              "the final Prometheus scrape, verbatim.",
			"logs/":                     "each replica's stdout and stderr for the whole run.",
		},
		Notes: summaryNotes(*replicas),
	}); err != nil {
		return err
	}

	for _, c := range conditions {
		verdict := "FAIL"
		if c.Passed {
			verdict = "pass"
		}
		fmt.Printf("  %-16s %-5s %s\n", c.ID, verdict, c.Measurement)
	}

	if !result.Passed() || killed != 1 || restarted != 1 {
		return fmt.Errorf("GATE 1 FAILED at N=%d — see %s", *replicas, filepath.Join(dir, "SUMMARY.md"))
	}
	fmt.Printf("gate1: PASS at N=%d — %s\n", *replicas, filepath.Join(dir, "SUMMARY.md"))
	return nil
}

// adversarialSchedule is §1.3's table, scaled for a real run.
//
// ── WHY NOT load.DefaultSchedule SPREAD ACROSS THE DURATION ──────────────
// Two measurements from the smoke runs, both of which would have wasted a
// four-hour run:
//
// FIRST, an injection only fires when a MATCHING REQUEST ARRIVES, and this
// installation's traffic is not uniform. The planner claims every due
// subscription at once, the workers burst through them, and then everything
// backs off to its TTL — at least `ttl_floor` (300s) and usually the route's
// own cache age. A 120-second run at 3 characters served all 224 of its
// requests in the first seconds and then went silent, so injections placed
// at duration/16 intervals landed in dead air: ONE of 22 fired. Spreading
// the table across four hours would put most of it in the quiet stretches
// between claim waves. It is compressed into the dense opening instead, and
// the remaining hours measure steady-state stability, which is what the
// long run is actually for.
//
// SECOND, DefaultSchedule cannot satisfy condition 1.4 at any duration.
// §1.3's table has a row this schedule adds — "error budget driven to the
// threshold ⇒ proactive pause BEFORE any 420" — and DefaultSchedule has no
// entry that drives the budget anywhere: its whole table produces about
// twenty errors across the run, against a Governor 2 budget of 100 per
// 60-second window. `esi_error_limit_remaining` never fell below 95 in
// either smoke run, so 1.4 reported FAIL for a system that was behaving
// correctly and had simply never been asked the question.
//
// The burst below asks it: 85 consecutive 4XX inside one window drives the
// remaining budget to ~15, under the pause threshold of 20. A correct
// installation pauses proactively and NO 420 follows (condition 1.2 stays
// green); an incorrect one spends the last of the budget and starts
// collecting 420s. That is the entire point of the condition, and it is
// worth being explicit that this run is the first time it has been asked.
//
// Composing the schedule is the CALLER's job by the harness's own design —
// test/load "does NOT decide when to run, how long for, or at what replica
// count", and an injection's `After` is a timing decision. The KINDS are
// all the harness's own; nothing new is invented here.
func adversarialSchedule(spacing time.Duration) []load.Injection {
	at := func(n int) time.Duration { return time.Duration(n) * spacing }
	const characterRoutes = "/characters"
	return []load.Injection{
		{After: at(1), Kind: load.Kind4XXBurst, Count: 3, PathContains: characterRoutes},
		{After: at(2), Kind: load.Kind429Headerless, Count: 1, PathContains: characterRoutes},
		{After: at(3), Kind: load.Kind429RetryAfter, Count: 1, PathContains: characterRoutes},
		{After: at(4), Kind: load.KindServerReportsLower, Count: 1, PathContains: characterRoutes},
		{After: at(5), Kind: load.KindServerReportsHigher, Count: 1, PathContains: characterRoutes},
		// Ten is the route breaker's threshold (§5.8) — the smallest burst
		// that proves it OPENS rather than that it counts.
		{After: at(6), Kind: load.Kind5XXSustained, Count: 10, PathContains: characterRoutes},
		// Five is the entity breaker's threshold, scoped to ONE caller so
		// condition 1.5 has siblings to compare against.
		{After: at(7), Kind: load.Kind403Consecutive, Count: 5, TokenContains: gate1InjectedToken},
		// §1.3's error-budget row. Last, because it deliberately drives the
		// installation into a proactive pause and everything after it would
		// be measuring the recovery rather than the condition it scheduled.
		{After: at(9), Kind: load.Kind4XXBurst, Count: 85},
	}
}

// gate1InjectedToken scopes the entity-breaker injection to ONE character,
// so condition 1.5 ("failure stayed scoped") has siblings to compare
// against. It is the bearer token the anchor character presents, derived
// through the same mapping the stub uses.
var gate1InjectedToken = accessTokenFor(anchorRefreshToken)

// specServingProxy answers ESI's two discovery endpoints from the embedded
// snapshot and hands everything else to the recording proxy.
//
// Without this the installation could not build a route catalogue at all
// while pointed away from the real ESI, and the proxy would answer the spec
// request with the same empty JSON array it answers every data route with.
// Serving the snapshot is also what makes the proxy's rate limits and the
// installation's ingested ones the SAME values, which §1.1's resolver
// comment requires.
func specServingProxy(proxy *load.Proxy, spec []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case catalogue.CompatibilityDatesPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(compatibilityDates(spec))
			return
		case catalogue.OpenAPIPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(spec)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// compatibilityDates answers /meta/compatibility-dates with the one date
// the embedded snapshot was captured at, so D_max discovery resolves to the
// spec actually being served rather than to a date with no document behind
// it.
//
// The shape is the OBJECT ESI returns and internal/esi/catalogue decodes,
// `{"compatibility_dates": [...]}`. A bare array decodes to an error and
// Boot falls back to the embedded snapshot — silently, because an offline
// boot is a supported outcome. The installation would still run (the
// snapshot IS this spec), but the run's claim that the catalogue and the
// proxy come from the same server would be false.
func compatibilityDates(_ []byte) []byte {
	_, meta, err := catalogue.LoadEmbeddedSnapshot()
	if err != nil || meta.DMax == "" {
		return []byte(`{"compatibility_dates":[]}`)
	}
	out, err := json.Marshal(map[string][]string{"compatibility_dates": {meta.DMax}})
	if err != nil {
		return []byte(`{"compatibility_dates":[]}`)
	}
	return out
}

// stubSSOToken is the refresh-token exchange.
//
// internal/sso.EnsureAccessToken exchanges the stored refresh token on
// EVERY authenticated request, so 5000 characters against the real EVE SSO
// is both impossible and rude. The stub is not a JWT: the refresher only
// inspects claims when a Verifier is configured and treats a verification
// failure as "keep the stored owner hash", so an opaque token exercises the
// same path without minting RSA keys for a rate-limit test.
//
// ── WHY THE ACCESS TOKEN IS DERIVED FROM THE REFRESH TOKEN ───────────────
// This is the difference between a Gate 1 run and four wasted hours, and it
// is not obvious.
//
// HANGAR partitions its Governor 1 ledger by UserKey, which the workers set
// to `hangar:<character_id>` — one bucket per character. The PROXY cannot
// see that: it is a server, and it partitions the way ESI does, by the
// bearer token on the request (proxy.go's userKeyOf).
//
// A stub returning one constant access token would therefore collapse 5000
// characters into ONE bucket on the proxy's side while HANGAR correctly
// believed it had 5000 — and the proxy would record Governor 1 breaches for
// a client that never overdrew anything. Gate 1.1 would fail, breaches.json
// would be full, and the failure would be entirely an artefact of the
// harness.
//
// So each character's access token is derived from its own refresh token,
// which seedWorld made unique per character, and the refresh token is
// returned unrotated so that identity is stable for the whole run. The
// persistence path still runs on every exchange — the refresher writes what
// it is handed — it simply writes the same value back.
func stubSSOToken(w http.ResponseWriter, r *http.Request) {
	refreshToken := ""
	if err := r.ParseForm(); err == nil {
		refreshToken = r.PostForm.Get("refresh_token")
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":1199,"refresh_token":%q}`,
		accessTokenFor(refreshToken), refreshToken)
}

// accessTokenFor is the one definition of the stub's refresh-token ->
// access-token mapping.
//
// It has two callers that MUST agree: the stub above, and the constant the
// adversarial schedule targets. §1.3's entity-breaker row is "5 consecutive
// 403s on ONE ENTITY", and the injector selects that entity by matching the
// bearer token the proxy sees — so if this mapping and the anchor character's
// expected token ever disagreed, the injection would match nothing, the
// schedule would not complete, and the harness would fail the run for a
// condition it never exercised. Deriving both from this function makes that
// disagreement impossible to write.
func accessTokenFor(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return "gate1-access-" + hex.EncodeToString(sum[:12])
}

// replicaEnv is the environment every replica inherits: this process's own,
// with the gate's overrides applied last so they win.
func replicaEnv(dbURL, esiURL, ssoURL string) []string {
	return append(os.Environ(),
		"HANGAR_DB_URL="+dbURL,
		"HANGAR_ESI_BASE_URL="+esiURL,
		"HANGAR_SSO_TOKEN_URL="+ssoURL,
		// The gate measures the ledger, not the alert pipeline. Leaving the
		// evaluator on would add threshold queries to every tick for no
		// measurement.
		"HANGAR_ALERT_THRESHOLD_INTERVAL=0",
	)
}

// startPlanner runs the REAL planner in this process, with no heartbeat.
func startPlanner(ctx context.Context, connString string, pool *pgxpool.Pool, cfg planner.Config, logger *slog.Logger) (func(), error) {
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, err
	}
	cfg.ConnString = connString
	p := planner.New(pool, riverClient, cfg, logger)

	plannerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := p.Run(plannerCtx); err != nil && plannerCtx.Err() == nil {
			logger.Error("gate1: sync planner exited unexpectedly", "error", err)
		}
	}()
	return func() { cancel(); <-done }, nil
}

func environmentNotes(cfg *config.Config, world seedResult, characters int, extra string) map[string]string {
	notes := map[string]string{
		"host_os":               runtime.GOOS,
		"host_arch":             runtime.GOARCH,
		"cpus":                  fmt.Sprint(runtime.NumCPU()),
		"characters_seeded":     fmt.Sprint(characters),
		"scopes_per_character":  fmt.Sprint(world.Scopes),
		"sync_subscriptions":    fmt.Sprint(world.Subscriptions),
		"ttl_floor":             cfg.ESI.TTLFloor.String(),
		"error_limit_max":       fmt.Sprint(cfg.ESI.ErrorLimitMax),
		"error_limit_pause_at":  fmt.Sprint(cfg.ESI.ErrorLimitPauseAt),
		"error_limit_resume_at": fmt.Sprint(cfg.ESI.ErrorLimitResumeAt),
		"planner_interval":      cfg.Sync.PlannerInterval.String(),
		"claim_batch_size":      fmt.Sprint(cfg.Sync.ClaimBatchSize),
		"proxy":                 "test/load recording proxy — floating-window Governor 1, fixed-window Governor 2",
	}
	if extra != "" {
		notes["notes"] = extra
	}
	return notes
}

func summaryEnvironment(cfg *config.Config, world seedResult, replicas int, duration time.Duration) map[string]string {
	return map[string]string{
		"Replicas":           fmt.Sprint(replicas),
		"Characters":         fmt.Sprint(world.Characters),
		"Sync subscriptions": fmt.Sprint(world.Subscriptions),
		"Host":               fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
		"Requested duration": duration.String(),
		"TTL floor":          cfg.ESI.TTLFloor.String(),
		"Error budget":       fmt.Sprintf("%d per %s, pause at %d, resume at %d", cfg.ESI.ErrorLimitMax, cfg.ESI.ErrorLimitWindow, cfg.ESI.ErrorLimitPauseAt, cfg.ESI.ErrorLimitResumeAt),
	}
}

func summaryNotes(replicas int) string {
	common := "The proxy is an independent measurement, not a mirror of the client: it restates §5.5's " +
		"cost table from the server's side rather than importing internal/esi/ratelimit's, because a gate " +
		"that shares its implementation with the thing it measures is not a measurement.\n\n" +
		"The sync planner runs inside the runner rather than as a `hangar serve` or `hangar schedule` " +
		"process. That is not a convenience: every role writes a heartbeat into app.esi_replica and " +
		"CountLiveReplicas counts rows regardless of role, so a planner process would make this run report " +
		"one more live replica than it has — and at N=1 that is the difference between the solo path §1.4 " +
		"requires and the clustered one."
	if replicas == 1 {
		return common + "\n\nAt N=1 this run exercises the SOLO ledger. Condition 1.7 is trivially " +
			"satisfied here (there is no aggregate to overdraw) and is answered properly by the N=3 run."
	}
	return common + "\n\nAt N=3 this run exercises the CLUSTERED ledger: the shared-ledger transaction, " +
		"condition 1.7's aggregate budget across three replicas sharing one bucket, and acquire latency " +
		"under real contention. §1.4 requires both results; neither alone is sufficient."
}
