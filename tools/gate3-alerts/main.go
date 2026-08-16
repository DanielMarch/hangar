// Command gate3-alerts runs Gate 3 — Alert Delivery Integrity
// (docs/04_RELEASE_GATES.md §3) — and writes the accounting-identity report
// the gate blocks on.
//
// ── THE GATE, AND THE TRAP IN IT ─────────────────────────────────────────
// §3: "a 4-hour alert load test drops zero alerts", where an alert is
// dropped if it was generated and neither delivered nor dead-lettered.
//
// "Zero dropped" is trivially true of a pipeline with no producer, and that
// is not hypothetical: defect B25 was a fully wired dispatcher draining an
// outbox that nothing wrote to, and a Gate 3 run against it would have
// passed truthfully and meaninglessly. So the harness's FIRST condition is
// a sample gate, and this runner generates across all three §4.4 categories
// through their REAL seams — the sync handler's notification hook, the
// threshold evaluator, and the domain-event emitter — rather than by
// calling the emitter directly and calling that a producer.
//
// ── WHY THE RUN HAS A GENERATE PHASE AND A DRAIN PHASE ───────────────────
// The identity must hold EXACTLY at end of run, and a delivery to a channel
// that is down does not reach a terminal state until it has exhausted its
// attempts or crossed the dead-letter horizon. Generating right up to the
// final second would therefore leave deliveries pending that the pipeline
// was handling perfectly correctly — a failure the gate would report as a
// drop. So generation stops early and the remainder of the run is spent
// draining, and the runner REFUSES to start if the drain budget is too
// short for the configured retry policy to settle in. See checkSettleable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/test/load"
	"github.com/hangar-project/hangar/tools/gaterun"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gate3-alerts: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		duration    = flag.Duration("duration", 4*time.Hour, "wall-clock length of the run (§8: 4h)")
		generateFor = flag.Duration("generate-for", 0, "how long to generate alerts; the rest of the run drains (default half the duration)")
		interval    = flag.Duration("generate-interval", 30*time.Second, "spacing between generation batches")
		version     = flag.String("version", "", "release version the evidence belongs to")
		outDir      = flag.String("out", "", "evidence directory (default docs/gate-evidence/<version>/gate3)")
		binary      = flag.String("binary", gaterun.DefaultBinary(), "path to the hangar binary (for migrations)")
		force       = flag.Bool("force", false, "run against a database whose name does not look like a gate database")
		notes       = flag.String("notes", "", "free text recorded in the summary")
	)
	flag.Parse()

	if *version == "" {
		return errors.New("-version is required")
	}
	if *generateFor <= 0 {
		*generateFor = *duration / 2
	}
	if *generateFor >= *duration {
		return errors.New("-generate-for must be shorter than -duration: the remainder is the drain phase")
	}
	dir, err := gaterun.EvidenceDir(*outDir, *version, "gate3")
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

	policy := alerting.RetryPolicy{
		MaxAttempts:     cfg.Alerting.MaxAttempts,
		Base:            cfg.Alerting.RetryBase,
		Cap:             cfg.Alerting.RetryCap,
		DeadLetterAfter: cfg.Alerting.DeadLetterAfter,
	}
	drain := *duration - *generateFor
	if err := checkSettleable(policy, drain); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	env := append(os.Environ(), "HANGAR_DB_URL="+dbURL)
	if err := gaterun.RunBinary(ctx, *binary, env, "migrate", "up"); err != nil {
		return fmt.Errorf("migrating the gate database: %w", err)
	}
	// The four THRESHOLD alert types are seeded through a join against
	// app.esi_route, so they do not exist until a catalogue has been
	// ingested. Without them a third of §3.2's categories cannot fire at
	// all — and would fail as "the evaluator found nothing" rather than as
	// "the alert type does not exist", which is the confusion 20.4.1's
	// re-seed exists to prevent.
	if err := gaterun.RunBinary(ctx, *binary, env, "admin", "ingest-catalogue"); err != nil {
		return fmt.Errorf("ingesting the route catalogue: %w", err)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connecting to the gate database: %w", err)
	}
	defer pool.Close()

	w, err := newWorld(ctx, pool)
	if err != nil {
		return err
	}

	since := time.Now()
	var tally load.Gate3Tally
	emitter := &alerting.Emitter{Pool: pool, Window: cfg.Alerting.CoalesceWindow}

	// §4.4's notification seam. Driving generation through the REAL sync
	// handler rather than through the emitter is the whole point: an
	// emitter with no caller is precisely defect B25, and a gate that
	// called the emitter itself could not have detected it.
	handlers.NotificationObservedHook = func(ctx context.Context, n handlers.ObservedNotification) error {
		result, err := emitter.IngestNotification(ctx, alerting.Notification{
			Type: n.Type, NotificationID: n.NotificationID,
			Payload: n.Payload, OccurredAt: n.OccurredAt,
		})
		if err != nil {
			return err
		}
		tally.Add(result.EventsRecorded+result.EventsDeduplicated,
			result.EventsRecorded, result.EventsDeduplicated, result.DeliveriesEnqueued)
		return nil
	}
	defer func() { handlers.NotificationObservedHook = nil }()

	observer := newOutcomeCounter()
	dispatcher := &alerting.Dispatcher{
		Pool: pool, Policy: policy, ClaimSize: cfg.Alerting.ClaimSize,
		Channels: w.channelFor, Observer: observer,
		Log: logger.With("component", "alerting.dispatcher"),
	}
	evaluator := &alerting.Evaluator{
		Pool: pool, Emitter: emitter,
		Policy: alerting.ThresholdPolicy{StructureFuelWithin: 48 * time.Hour},
	}

	// The pump, running for the whole window — generation and drain alike.
	pumpCtx, stopPump := context.WithCancel(ctx)
	var pumpWG sync.WaitGroup
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		ticker := time.NewTicker(cfg.Alerting.DispatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pumpCtx.Done():
				return
			case <-ticker.C:
				if _, err := dispatcher.Tick(pumpCtx); err != nil && pumpCtx.Err() == nil {
					fmt.Printf("gate3: dispatch tick failed: %v\n", err)
				}
			}
		}
	}()

	fmt.Printf("gate3: generating for %s, then draining for %s\n", *generateFor, drain)
	if err := generate(ctx, w, emitter, evaluator, &tally, *generateFor, *interval); err != nil {
		stopPump()
		pumpWG.Wait()
		return err
	}

	fmt.Printf("gate3: generation complete (%d occurrences offered); draining for %s\n", tally.Offered, drain)
	settled := drainUntilSettled(ctx, pool, since, drain)
	stopPump()
	pumpWG.Wait()

	result, err := load.MeasureGate3(ctx, pool, load.Gate3Config{
		Since: since, MinAlerts: 1000, MinCategories: 3, OutputDir: dir,
		Notes: fmt.Sprintf("generated for %s, drained for %s", *generateFor, drain),
	}, tally)
	if err != nil {
		return fmt.Errorf("measuring: %w", err)
	}

	conditions := append([]load.ConditionResult{}, result.Conditions...)
	// Counted over the EIGHT §4.4 domains by name, not over
	// count(DISTINCT domain). This run deliberately generates an
	// unrecognised CCP type, which lands in the `unknown` domain — so a
	// distinct-count of 8 can be reached while one of the eight is missing,
	// and the first version of this condition read exactly that way.
	realDomains, missing, err := domainsCovered(ctx, pool, since)
	if err != nil {
		return err
	}
	conditions = append(conditions, load.ConditionResult{
		ID:          "3-domains",
		Description: "the run exercised all eight §4.4 domains",
		Passed:      len(missing) == 0,
		Measurement: fmt.Sprintf("%d of %d domains produced events%s", realDomains, len(catalogue.Domains),
			func() string {
				if len(missing) == 0 {
					return ""
				}
				return "; missing: " + strings.Join(missing, ", ")
			}()),
	})
	conditions = append(conditions, load.ConditionResult{
		ID:          "3.7",
		Description: "channel outages produced retries then dead-letters, never queue blockage",
		Passed:      result.Observed.DeadLettered > 0 && result.Observed.MessagesSent > 0,
		Measurement: fmt.Sprintf("%d dead-lettered from the down channels while %d messages went out on the healthy ones",
			result.Observed.DeadLettered, result.Observed.MessagesSent),
	})
	// The finding the first smoke run surfaced, kept as a measurement rather
	// than a comment: a delivery whose next_attempt_at lies beyond the end of
	// the run has not been dropped, but nothing in this run could have
	// delivered it either. It comes from the structure-fuel and
	// contract-expiry thresholds stamping OccurredAt as the EXPIRY, which
	// then becomes the coalescing bucket and therefore the delivery's due
	// time — so the warning is scheduled for the moment the thing it warns
	// about happens.
	beyond, furthest, err := scheduledBeyond(ctx, pool, since, time.Now())
	if err != nil {
		return err
	}
	conditions = append(conditions, load.ConditionResult{
		ID:          "3.1-scheduled-beyond-run",
		Description: "no delivery is scheduled to become claimable after the run ends (see the note — this is about early warnings arriving late, not about drops)",
		Passed:      beyond == 0,
		Measurement: fmt.Sprintf("%d deliveries have next_attempt_at after the end of the run%s", beyond, furthest),
	})

	conditions = append(conditions, load.ConditionResult{
		ID:          "3.4",
		Description: "coalesced events arrived as ONE message per group (no query can see this — it is read from the channel stub)",
		Passed:      w.largestRollup() > 1,
		Measurement: fmt.Sprintf("largest roll-up accepted by a channel carried %d events", w.largestRollup()),
	})

	if err := writeArtefacts(dir, result, observer, w, settled); err != nil {
		return err
	}

	if err := load.WriteSummary(dir, load.Summary{
		Gate: "3", Name: "Alert Delivery Integrity", Version: *version,
		StartedAt: since, FinishedAt: time.Now(),
		Headline: fmt.Sprintf("%d occurrences offered, %d events, %d deliveries; %d left neither delivered nor dead-lettered.",
			tally.Offered, result.Observed.Events, result.Observed.Deliveries, result.Observed.Pending),
		Conditions: conditions,
		Environment: map[string]string{
			"Generate window":   generateFor.String(),
			"Drain window":      drain.String(),
			"Coalescing window": cfg.Alerting.CoalesceWindow.String(),
			"Dispatch interval": cfg.Alerting.DispatchInterval.String(),
			"Retry policy":      fmt.Sprintf("max %d attempts, base %s, cap %s, dead-letter after %s", policy.MaxAttempts, policy.Base, policy.Cap, policy.DeadLetterAfter),
			"Channels":          fmt.Sprintf("%d stubs: healthy, transiently-failing, permanently-failing, always-down", len(w.channels)),
			"Host":              fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
		},
		Artefacts: map[string]string{
			"accounting.json": "both identities — occurrences and deliveries — with every term counted independently. The blocking artefact.",
			"conditions.json": "the per-condition verdicts in machine-readable form.",
			"channel-log.csv": "per-stub attempts, messages accepted and the largest roll-up each received (§3.4's evidence).",
		},
		Notes: gate3Notes(*notes),
	}); err != nil {
		return err
	}

	for _, c := range conditions {
		verdict := "FAIL"
		if c.Passed {
			verdict = "pass"
		}
		fmt.Printf("  %-20s %-5s %s\n", c.ID, verdict, c.Measurement)
	}
	if !result.Passed() {
		return fmt.Errorf("GATE 3 FAILED — see %s", filepath.Join(dir, "SUMMARY.md"))
	}
	fmt.Printf("gate3: PASS — %s\n", filepath.Join(dir, "SUMMARY.md"))
	return nil
}

// checkSettleable refuses a run whose drain phase cannot possibly settle
// the configured retry policy.
//
// Without this the gate would report a drop — its most serious finding —
// for a pipeline that was behaving exactly as configured and had simply not
// been given time to finish. A gate that can fail for a reason unrelated to
// the property it measures is worse than no gate.
// domainsCovered reports how many of §4.4's eight domains produced an event
// in this run, and which did not.
// scheduledBeyond counts deliveries that will not become claimable until
// after the run has ended, and reports how far out the furthest one is.
func scheduledBeyond(ctx context.Context, pool *pgxpool.Pool, since, endOfRun time.Time) (int, string, error) {
	var count int
	var furthest *time.Time
	err := pool.QueryRow(ctx, `
		SELECT count(*)::int, max(d.next_attempt_at)
		  FROM app.alert_delivery d
		  JOIN app.alert_event e ON e.event_id = d.event_id
		 WHERE e.occurred_at >= $1 AND d.state = 'pending' AND d.next_attempt_at > $2`,
		since, endOfRun).Scan(&count, &furthest)
	if err != nil {
		return 0, "", fmt.Errorf("gate3: counting deliveries scheduled beyond the run: %w", err)
	}
	if furthest == nil {
		return count, "", nil
	}
	return count, fmt.Sprintf("; furthest is %s, which is %s after the run ended",
		furthest.UTC().Format(time.RFC3339), furthest.Sub(endOfRun).Round(time.Minute)), nil
}

func domainsCovered(ctx context.Context, pool *pgxpool.Pool, since time.Time) (int, []string, error) {
	names := make([]string, 0, len(catalogue.Domains))
	for _, d := range catalogue.Domains {
		names = append(names, string(d))
	}
	rows, err := pool.Query(ctx, `
		SELECT d.domain, EXISTS (
		    SELECT 1 FROM app.alert_event e
		      JOIN app.alert_type t ON t.alert_type = e.alert_type
		     WHERE t.domain = d.domain AND e.occurred_at >= $2)
		  FROM unnest($1::text[]) AS d(domain)`, names, since)
	if err != nil {
		return 0, nil, fmt.Errorf("gate3: counting domains covered: %w", err)
	}
	defer rows.Close()

	covered := 0
	var missing []string
	for rows.Next() {
		var domain string
		var seen bool
		if err := rows.Scan(&domain, &seen); err != nil {
			return 0, nil, err
		}
		if seen {
			covered++
			continue
		}
		missing = append(missing, domain)
	}
	return covered, missing, rows.Err()
}

func checkSettleable(policy alerting.RetryPolicy, drain time.Duration) error {
	worst := policy.DeadLetterAfter
	if backoff := worstCaseBackoff(policy); backoff < worst {
		worst = backoff
	}
	if drain >= worst {
		return nil
	}
	return fmt.Errorf(
		"the drain phase (%s) is shorter than the worst case for a delivery to reach a terminal state (%s, "+
			"from max_attempts=%d base=%s cap=%s dead_letter_after=%s). A delivery still retrying at end of run "+
			"would be reported as a DROP, which would be a false failure. Either lengthen -duration, shorten "+
			"-generate-for, or set HANGAR_ALERT_DEAD_LETTER_AFTER to something the run can reach",
		drain, worst, policy.MaxAttempts, policy.Base, policy.Cap, policy.DeadLetterAfter)
}

// worstCaseBackoff is the total delay of an exhausted retry sequence.
func worstCaseBackoff(policy alerting.RetryPolicy) time.Duration {
	total := time.Duration(0)
	delay := policy.Base
	for i := 1; i < policy.MaxAttempts; i++ {
		if delay > policy.Cap {
			delay = policy.Cap
		}
		total += delay
		delay *= 2
	}
	return total
}

// drainUntilSettled ticks until nothing is left pending, or the budget
// expires. It reports whether everything settled — a run that ran out of
// drain time has not shown the identity holds, and the summary says so
// rather than reporting the pending count as a drop.
func drainUntilSettled(ctx context.Context, pool *pgxpool.Pool, since time.Time, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
		var pending int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)::int FROM app.alert_delivery d
			  JOIN app.alert_event e ON e.event_id = d.event_id
			 WHERE e.occurred_at >= $1 AND d.state = 'pending'`, since).Scan(&pending); err != nil {
			continue
		}
		if pending == 0 {
			return true
		}
	}
	return false
}

func writeArtefacts(dir string, result *load.Gate3Result, observer *outcomeCounter, w *world, settled bool) error {
	if err := gaterun.WriteJSON(filepath.Join(dir, "accounting.json"), map[string]any{
		"generated":                 result.Generated,
		"observed":                  result.Observed,
		"metric_cross_check":        observer.snapshot(),
		"settled_before_end_of_run": settled,
	}); err != nil {
		return err
	}
	if err := gaterun.WriteJSON(filepath.Join(dir, "conditions.json"), result.Conditions); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("channel,kind,behaviour,attempts,messages_accepted,largest_rollup\n")
	for _, c := range w.channels {
		largest := 0
		for _, m := range c.stub.Accepted() {
			if m.Count > largest {
				largest = m.Count
			}
		}
		fmt.Fprintf(&b, "%s,%s,%s,%d,%d,%d\n",
			c.name, c.stub.Kind(), c.behaviour, c.stub.Attempts(), len(c.stub.Accepted()), largest)
	}
	if err := os.WriteFile(filepath.Join(dir, "channel-log.csv"), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("writing channel-log.csv: %w", err)
	}
	return nil
}

// outcomeCounter is the METRIC side — alert_delivery_total. Reported as a
// cross-check against the tables, never as the verdict.
type outcomeCounter struct {
	mu        sync.Mutex
	byOutcome map[string]int
}

func newOutcomeCounter() *outcomeCounter { return &outcomeCounter{byOutcome: map[string]int{}} }

func (o *outcomeCounter) ObserveAlertDelivery(_, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.byOutcome[outcome]++
}

func (o *outcomeCounter) snapshot() map[string]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := map[string]int{}
	for k, v := range o.byOutcome {
		out[k] = v
	}
	return out
}

func gate3Notes(extra string) string {
	notes := "Every OUTCOME term is counted from app.alert_event and app.alert_delivery with SQL. The " +
		"INPUT term cannot be: an occurrence that deduplicated leaves no database trace at all — that is " +
		"what RecordAlertEvent's ON CONFLICT (dedupe_hash) DO NOTHING means — so the harness counts what " +
		"it fed in. That is not the system reporting on itself; it is the test's own tally of its own " +
		"input, and checking it against the tables is the point of the identity.\n\n" +
		"Conditions 3.3, 3.5, 3.6 and 3.8 are not evaluated from this run's row counts. Each is a " +
		"statement about a code path or a rendered string rather than about a run's totals, and each is " +
		"asserted by a test that can actually see the thing: internal/alerting's suite, catalogue's " +
		"ValidateThresholds under `make check-alert-sources`, and internal/esi/ratelimit's admission " +
		"tests. Reporting them as passed because a run completed would be inventing evidence.\n\n" +
		"§3.3's unparseable-YAML case IS exercised here — the generator feeds notification text no strict " +
		"parser accepts, and the queue must not halt on it — but what that proves from this side is that " +
		"generation continued, not that the render fell back correctly."
	if extra != "" {
		notes += "\n\n" + extra
	}
	return notes
}
