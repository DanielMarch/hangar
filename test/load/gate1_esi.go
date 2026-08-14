package load

// gate1_esi.go is Gate 1's driver: it runs a live installation against the
// recording proxy, scrapes the installation's own /metrics endpoints on a
// timer, and writes the seven evidence artefacts 04_RELEASE_GATES.md §1.5
// names.
//
// It does NOT decide when to run, how long for, or at what replica count —
// those come from Config, and Phase 20.8 supplies them. Nothing in this
// file starts a four-hour run.
//
// ── WHAT IS MEASURED HERE AND WHAT IS MEASURED ELSEWHERE ─────────────────
// Two independent sources, deliberately:
//
//	the PROXY answers 1.1 (breaches) and 1.7 (aggregate consumption),
//	  because only the server can say what it actually admitted;
//	the INSTALLATION's /metrics answers 1.2 (esi_420_total), 1.3
//	  (esi_ledger_divergence), 1.4 (esi_error_limit_remaining crossing the
//	  pause threshold) and 1.8 (esi_ledger_mode).
//
// A gate that read both from the same side would be measuring a system
// against its own opinion of itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config parameterises one Gate 1 run (one replica count — §1.4 requires
// two separate runs, recorded separately).
type Config struct {
	// Duration is the wall-clock length of the run. §1.1 specifies 4 hours
	// for the real gate; the integration test uses seconds.
	Duration time.Duration
	// Replicas is the replica count this run exercises, recorded in
	// environment.json. §1.4: a Gate 1 result without it is meaningless.
	Replicas int
	// MetricsURLs are the installation's /metrics endpoints, one per
	// replica.
	MetricsURLs []string
	// ScrapeInterval is how often to sample them. Divergence is reported
	// per minute (§1.5), so this must be at most a minute.
	ScrapeInterval time.Duration
	// OutputDir receives the artefacts. Empty writes nothing, which is what
	// the integration test wants.
	OutputDir string
	// ErrorLimitPauseAt mirrors HANGAR_ESI_ERROR_LIMIT_PAUSE_AT, so
	// condition 1.4 can tell "a pause fired at the configured threshold"
	// from "the budget happened to dip".
	ErrorLimitPauseAt int
	// Notes is free text recorded in environment.json — CPU, RAM, Postgres
	// settings, network latency to ESI (§0 rule 3).
	Notes map[string]string
}

// Result is one run's outcome: the eight pass conditions of §1.2, each
// with the measurement that decided it.
type Result struct {
	Replicas   int               `json:"replicas"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Conditions []ConditionResult `json:"conditions"`
	Breaches   []Breach          `json:"breaches"`
	Samples    []Sample          `json:"-"`
}

// ConditionResult is one row of §1.2's table.
type ConditionResult struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Passed      bool   `json:"passed"`
	Measurement string `json:"measurement"`
}

// Passed reports whether every condition passed. A run with no samples at
// all does NOT pass: a missing reading is never a zero (the 20.1 lesson),
// and "we scraped nothing and found no failures" is the shape of a gate
// that measured nothing.
func (r *Result) Passed() bool {
	if len(r.Samples) == 0 {
		return false
	}
	for _, c := range r.Conditions {
		if !c.Passed {
			return false
		}
	}
	return true
}

// Sample is one scrape of one replica's /metrics.
type Sample struct {
	At    time.Time
	URL   string
	Raw   string
	Value map[string]float64
	// Divergence is esi_ledger_divergence keyed by its `group` label.
	Divergence map[string]float64
	// Mode is the label of whichever esi_ledger_mode series read 1.
	Mode string
}

// Run drives one Gate 1 run. It blocks for cfg.Duration or until ctx is
// cancelled, whichever comes first.
func Run(ctx context.Context, cfg Config, proxy *Proxy) (*Result, error) {
	if cfg.ScrapeInterval <= 0 {
		cfg.ScrapeInterval = 15 * time.Second
	}
	res := &Result{Replicas: cfg.Replicas, StartedAt: time.Now()}

	deadline := time.Now().Add(cfg.Duration)
	ticker := time.NewTicker(cfg.ScrapeInterval)
	defer ticker.Stop()

	// Scrape once immediately, so a run shorter than one interval still
	// produces a reading rather than an empty artefact.
	res.Samples = append(res.Samples, scrapeAll(ctx, cfg.MetricsURLs)...)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return finish(cfg, proxy, res)
		case <-ticker.C:
			res.Samples = append(res.Samples, scrapeAll(ctx, cfg.MetricsURLs)...)
		}
	}
	return finish(cfg, proxy, res)
}

func finish(cfg Config, proxy *Proxy, res *Result) (*Result, error) {
	res.FinishedAt = time.Now()
	res.Breaches = proxy.Breaches()
	res.Conditions = evaluate(cfg, proxy, res)
	if cfg.OutputDir == "" {
		return res, nil
	}
	if err := writeEvidence(cfg, proxy, res); err != nil {
		return res, err
	}
	return res, nil
}

// evaluate applies §1.2's eight conditions. Each one names the measurement
// that decided it, because "we ran it and it looked fine is a fail"
// (§0 rule 1) applies to the harness's own output too.
func evaluate(cfg Config, proxy *Proxy, res *Result) []ConditionResult {
	var out []ConditionResult

	out = append(out, ConditionResult{
		ID: "1.1", Description: "Zero Governor 1 breaches",
		Passed:      len(res.Breaches) == 0,
		Measurement: fmt.Sprintf("proxy recorded %d requests admitted with available <= 0", len(res.Breaches)),
	})

	max420 := maxOf(res.Samples, "esi_420_total")
	out = append(out, ConditionResult{
		ID: "1.2", Description: "Zero Governor 2 breaches",
		Passed:      max420 == 0,
		Measurement: fmt.Sprintf("esi_420_total peaked at %.0f", max420),
	})

	worst, worstGroup := maxDivergence(res.Samples)
	out = append(out, ConditionResult{
		ID: "1.3", Description: "Ledger divergence <= 1 per group",
		Passed:      worst <= 1,
		Measurement: fmt.Sprintf("max(esi_ledger_divergence) = %.0f (group %q)", worst, worstGroup),
	})

	pausedBelowThreshold, lowest := crossedPauseThreshold(res.Samples, cfg.ErrorLimitPauseAt)
	out = append(out, ConditionResult{
		ID: "1.4", Description: "Proactive error-limit pause fired, and no 420 followed",
		Passed:      pausedBelowThreshold && max420 == 0,
		Measurement: fmt.Sprintf("esi_error_limit_remaining reached %.0f against a pause threshold of %d; esi_420_total = %.0f", lowest, cfg.ErrorLimitPauseAt, max420),
	})

	// 1.5 and 1.6 are throughput-shaped and answered from the proxy's own
	// request log: a scoped failure must not stop the other callers, and
	// throughput must never be zero for longer than one ttl_floor.
	byStatus, total := proxy.Served()
	out = append(out, ConditionResult{
		ID: "1.5", Description: "Failure stayed scoped",
		Passed:      byStatus[http.StatusOK] > 0,
		Measurement: fmt.Sprintf("%d requests served, %d of them 200 while adversarial conditions were active", total, byStatus[http.StatusOK]),
	})

	out = append(out, ConditionResult{
		ID: "1.6", Description: "No stall",
		Passed:      total > 0,
		Measurement: fmt.Sprintf("%d requests reached the proxy over %s", total, res.FinishedAt.Sub(res.StartedAt).Round(time.Second)),
	})

	overdrawn := 0
	for _, s := range proxy.Consumption() {
		if s.Consumed > s.MaxTokens {
			overdrawn++
		}
	}
	out = append(out, ConditionResult{
		ID: "1.7", Description: "Aggregate consumption respected at N>1",
		Passed:      overdrawn == 0,
		Measurement: fmt.Sprintf("%d proxy-side samples showed consumption above max_tokens (replicas=%d)", overdrawn, cfg.Replicas),
	})

	wantMode := "solo"
	if cfg.Replicas > 1 {
		wantMode = "clustered"
	}
	modes := observedModes(res.Samples)
	out = append(out, ConditionResult{
		ID: "1.8", Description: "Mode selection correct throughout",
		Passed:      len(modes) == 1 && modes[0] == wantMode,
		Measurement: fmt.Sprintf("esi_ledger_mode observed as %v, expected %q throughout", modes, wantMode),
	})

	// Not a numbered condition, but a run whose adversarial schedule did
	// not complete has not tested §1.3's table, and reporting a pass for it
	// would be the same class of lie as a metric that reads zero because
	// nothing increments it.
	if pending := proxy.injector.Pending(); pending > 0 {
		out = append(out, ConditionResult{
			ID: "1.3-schedule", Description: "Every adversarial condition fired",
			Passed:      false,
			Measurement: fmt.Sprintf("%d scheduled injections never fired — the run did not exercise §1.3's table", pending),
		})
	}
	return out
}

// ── metrics scraping ─────────────────────────────────────────────────────

func scrapeAll(ctx context.Context, urls []string) []Sample {
	var out []Sample
	for _, u := range urls {
		s, err := scrape(ctx, u)
		if err != nil {
			// A failed scrape produces NO sample rather than a zero-valued
			// one — the same rule the collector itself follows. A gate that
			// silently substitutes zero for "could not read" reports a pass
			// for a run it did not observe.
			continue
		}
		out = append(out, s)
	}
	return out
}

func scrape(ctx context.Context, url string) (Sample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Sample{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Sample{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Sample{}, err
	}
	return parseSample(url, string(body)), nil
}

// parseSample reads the handful of series Gate 1 cares about out of a
// Prometheus text exposition body. A full parser is not warranted: the
// gate reads six named series and a full one would be another dependency
// to keep current.
func parseSample(url, body string) Sample {
	s := Sample{At: time.Now(), URL: url, Raw: body, Value: map[string]float64{}, Divergence: map[string]float64{}}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := parseMetricLine(line)
		if !ok {
			continue
		}
		switch name {
		case "esi_ledger_divergence":
			if g := labels["group"]; g != "" {
				s.Divergence[g] = value
			}
		case "esi_ledger_mode":
			if value == 1 {
				s.Mode = labels["mode"]
			}
		default:
			// Sum label-split counters (esi_429_total has has_headers) so
			// the caller reads one number per metric name.
			s.Value[name] += value
		}
	}
	return s
}

func parseMetricLine(line string) (name string, labels map[string]string, value float64, ok bool) {
	sep := strings.LastIndex(line, " ")
	if sep < 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(line[sep+1:]), 64)
	if err != nil {
		return "", nil, 0, false
	}
	head := strings.TrimSpace(line[:sep])
	labels = map[string]string{}
	if brace := strings.Index(head, "{"); brace >= 0 {
		name = head[:brace]
		for _, pair := range strings.Split(strings.Trim(head[brace+1:], "{}"), ",") {
			k, val, found := strings.Cut(pair, "=")
			if !found {
				continue
			}
			labels[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(val), `"`)
		}
	} else {
		name = head
	}
	return name, labels, v, true
}

func maxOf(samples []Sample, metric string) float64 {
	highest := 0.0
	for _, s := range samples {
		if v, ok := s.Value[metric]; ok && v > highest {
			highest = v
		}
	}
	return highest
}

func maxDivergence(samples []Sample) (float64, string) {
	highest, group := 0.0, ""
	for _, s := range samples {
		for g, v := range s.Divergence {
			if v > highest {
				highest, group = v, g
			}
		}
	}
	return highest, group
}

// crossedPauseThreshold reports whether esi_error_limit_remaining was ever
// observed at or below the configured proactive-pause threshold, and the
// lowest value seen.
//
// It returns false when no sample carried the series at all, rather than
// treating an absent reading as "never crossed" — for Gate 1.4 the two are
// opposite conclusions, and the absent case means the budget was never
// scraped, not that it stayed healthy.
func crossedPauseThreshold(samples []Sample, threshold int) (bool, float64) {
	seen := false
	lowest := 0.0
	for _, s := range samples {
		v, ok := s.Value["esi_error_limit_remaining"]
		if !ok {
			continue
		}
		if !seen || v < lowest {
			lowest = v
		}
		seen = true
	}
	if !seen {
		return false, 0
	}
	return lowest <= float64(threshold), lowest
}

func observedModes(samples []Sample) []string {
	set := map[string]bool{}
	for _, s := range samples {
		if s.Mode != "" {
			set[s.Mode] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// ── evidence artefacts (§1.5) ────────────────────────────────────────────

func writeEvidence(cfg Config, proxy *Proxy, res *Result) error {
	if err := os.MkdirAll(cfg.OutputDir, 0o750); err != nil {
		return fmt.Errorf("load: creating evidence directory: %w", err)
	}

	env := map[string]any{
		"replicas":             cfg.Replicas,
		"started_at":           res.StartedAt,
		"finished_at":          res.FinishedAt,
		"duration":             res.FinishedAt.Sub(res.StartedAt).String(),
		"mode_observed":        observedModes(res.Samples),
		"scrape_interval":      cfg.ScrapeInterval.String(),
		"error_limit_pause_at": cfg.ErrorLimitPauseAt,
		"notes":                cfg.Notes,
	}
	if err := writeJSON(filepath.Join(cfg.OutputDir, "environment.json"), env); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(cfg.OutputDir, "breaches.json"), res.Breaches); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(cfg.OutputDir, "conditions.json"), res.Conditions); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(cfg.OutputDir, "adversarial-log.jsonl"), proxy.InjectionLog()); err != nil {
		return err
	}
	if err := writeDivergenceCSV(filepath.Join(cfg.OutputDir, "divergence.csv"), res.Samples); err != nil {
		return err
	}
	if err := writeConsumptionCSV(filepath.Join(cfg.OutputDir, "aggregate-consumption.csv"), proxy.Consumption()); err != nil {
		return err
	}
	// The final scrape verbatim, so the artefact set carries the raw
	// exposition and not only this file's interpretation of it.
	if len(res.Samples) > 0 {
		last := res.Samples[len(res.Samples)-1]
		if err := os.WriteFile(filepath.Join(cfg.OutputDir, "metrics.prom"), []byte(last.Raw), 0o600); err != nil {
			return fmt.Errorf("load: writing metrics.prom: %w", err)
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("load: encoding %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("load: writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSONL[T any](path string, rows []T) error {
	var sb strings.Builder
	for _, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("load: encoding %s: %w", filepath.Base(path), err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("load: writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

// writeDivergenceCSV emits per-group, per-minute MAXIMUM divergence — the
// shape §1.5 asks for. A minute with no reading for a group emits no row,
// rather than a zero: Gate 1.3's threshold is on a maximum, and inventing
// zeroes would drag a real excursion's average down while leaving the
// maximum technically intact but unexplainable.
func writeDivergenceCSV(path string, samples []Sample) error {
	type key struct {
		minute string
		group  string
	}
	peak := map[key]float64{}
	for _, s := range samples {
		minute := s.At.UTC().Format("2006-01-02T15:04Z")
		for g, v := range s.Divergence {
			k := key{minute: minute, group: g}
			if current, seen := peak[k]; !seen || v > current {
				peak[k] = v
			}
		}
	}
	keys := make([]key, 0, len(peak))
	for k := range peak {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].minute != keys[j].minute {
			return keys[i].minute < keys[j].minute
		}
		return keys[i].group < keys[j].group
	})

	var sb strings.Builder
	sb.WriteString("minute,group,max_divergence\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s,%s,%.0f\n", k.minute, k.group, peak[k])
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("load: writing divergence.csv: %w", err)
	}
	return nil
}

func writeConsumptionCSV(path string, samples []ConsumptionSample) error {
	var sb strings.Builder
	sb.WriteString("at,group,user_key,consumed,max_tokens\n")
	for _, s := range samples {
		fmt.Fprintf(&sb, "%s,%s,%s,%d,%d\n", s.At.UTC().Format(time.RFC3339), s.Group, s.UserKey, s.Consumed, s.MaxTokens)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("load: writing aggregate-consumption.csv: %w", err)
	}
	return nil
}
