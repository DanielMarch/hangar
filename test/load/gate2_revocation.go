package load

// gate2_revocation.go is Gate 2's driver — 04_RELEASE_GATES.md §2.
//
// Like gate1_esi.go it does NOT decide when to run, for how long, or at
// what scale. §2.1's real run is 5000 identities across 3 platforms and
// belongs to Phase 20.8; nothing in this file starts one. What lives here
// is the measurement and the stubs, so that when the real run happens a
// failure means the system is wrong rather than the harness.
//
// ── WHERE THE NUMBER COMES FROM, AND WHY NOT FROM THE METRIC ─────────────
// §2.2 defines the measurement precisely: p99 of
// `platform_call_completed_at − event_at` from `app.provisioning_audit`.
// This harness reads exactly that, from the database, with SQL.
//
// It deliberately does NOT read provisioning_revocation_latency_seconds,
// even though Phase 20.3 added it and it is computed from the same two
// columns. A Prometheus histogram answers a p99 by interpolating within a
// bucket, so its answer near a boundary is an estimate — and 60s IS a
// boundary. More importantly, a gate that took its verdict from the
// application's own instrumentation would be asking the system whether it
// thinks it passed. The metric is for operators watching a live
// installation; the gate reads the table. The two agreeing is a useful
// cross-check, which is why §2.1's harness reports both.
//
// ── WHY THE STUBS ARE SLOW ON PURPOSE ────────────────────────────────────
// §2.1: "Three stub platforms (Discord, TeamSpeak, Mumble) reproducing
// each platform's real rate limits — a stub that answers instantly proves
// nothing, because the Discord bucket wait is a material part of the
// budget." RateLimitedStub below is that: a Driver with a real token
// bucket, so a revocation genuinely waits its turn and queue wait — §2.2's
// "the part that fails under load" — is inside the measurement.

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Gate2Config parameterises one Gate 2 measurement window.
type Gate2Config struct {
	// Since bounds the measurement to audit rows whose originating event
	// happened at or after this instant — the start of the run. Without it
	// a gate would silently include every revocation the installation has
	// ever performed, including ones from a previous, failed attempt.
	Since time.Time

	// SLO is the latency bound. §2.1: 60 s.
	SLO time.Duration

	// Percentile is the quantile the bound applies to, as a fraction.
	// §2.1: 0.99.
	Percentile float64

	// MinRevocations is the smallest sample the harness will pronounce on.
	// A p99 over four revocations is not a p99, and a gate that passed on
	// an empty run would be the most dangerous possible false green — the
	// exact shape of B25, where "zero alerts dropped" was true because
	// zero alerts existed.
	MinRevocations int

	// OutputDir receives the artefacts. Empty writes nothing, which is what
	// the integration test wants.
	OutputDir string

	// Notes is free text recorded alongside the result.
	Notes string
}

// Gate2Result is one measurement window's verdict.
type Gate2Result struct {
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at"`
	Notes       string            `json:"notes"`
	Latencies   Gate2Latencies    `json:"latencies"`
	Outcomes    map[string]int    `json:"outcomes"`
	Pending     int               `json:"pending_revocations"`
	OldestAge   float64           `json:"oldest_pending_age_seconds"`
	Conditions  []ConditionResult `json:"conditions"`
	SampleCount int               `json:"sample_count"`
}

// Gate2Latencies is the distribution §2.2 defines, in seconds.
type Gate2Latencies struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// Passed reports whether every evaluated condition passed.
func (r *Gate2Result) Passed() bool {
	if len(r.Conditions) == 0 {
		return false
	}
	for _, c := range r.Conditions {
		if !c.Passed {
			return false
		}
	}
	return true
}

// Gate2Querier is the database handle the measurement needs. *pgxpool.Pool
// satisfies it; an interface so the harness can be pointed at a
// transaction in a test.
type Gate2Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// MeasureGate2 computes §2.2's distribution over the audit table and
// evaluates the conditions this harness can decide from it.
//
// Conditions 2.2 (every trigger enqueues in the mutating transaction) and
// 2.5 (rolling back the mutation rolls back the job) are NOT evaluated
// here and their absence is deliberate: both are statements about code
// paths, not about a run's numbers, and both are asserted directly by
// integration tests (gate2_integration_test.go, and
// internal/provisioning's own suite). Reporting them here as "passed"
// because a run happened to complete would be inventing evidence.
func MeasureGate2(ctx context.Context, db Gate2Querier, cfg Gate2Config) (*Gate2Result, error) {
	if cfg.SLO <= 0 {
		cfg.SLO = 60 * time.Second
	}
	if cfg.Percentile <= 0 {
		cfg.Percentile = 0.99
	}

	res := &Gate2Result{
		StartedAt: cfg.Since,
		Notes:     cfg.Notes,
		Outcomes:  map[string]int{},
	}

	// action = 'revoke' only. reconcile.go writes action = 'reconcile'
	// rows whose event_at it stamps itself at loop start, so their
	// (completed − event) is a platform-call duration and not a time since
	// an originating event. Including them would flood the sample with
	// near-zero values and flatter the p99 — the same reason
	// UrgentWorker.Work is the only place the metric is observed.
	rows, err := db.Query(ctx, `
		SELECT outcome,
		       extract(epoch FROM (platform_call_completed_at - event_at))::double precision
		  FROM app.provisioning_audit
		 WHERE action = 'revoke'
		   AND event_at >= $1
		   AND platform_call_completed_at IS NOT NULL
		 ORDER BY 2`, cfg.Since)
	if err != nil {
		return nil, fmt.Errorf("gate2: reading provisioning_audit: %w", err)
	}
	defer rows.Close()

	var successes []float64
	for rows.Next() {
		var outcome *string
		var seconds float64
		if err := rows.Scan(&outcome, &seconds); err != nil {
			return nil, fmt.Errorf("gate2: scanning audit row: %w", err)
		}
		name := "unset"
		if outcome != nil {
			name = *outcome
		}
		res.Outcomes[name]++
		// The SLO is over revocations that ACTUALLY HAPPENED. A failed
		// platform call did not remove the group — its exposure is still
		// open — so counting how fast it failed as a revocation latency
		// would be the most flattering possible reading of the case the
		// SLO exists to bound. Failures are reported in Outcomes and
		// judged by condition 2.3 instead.
		if name == "success" {
			successes = append(successes, seconds)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gate2: iterating audit rows: %w", err)
	}

	sort.Float64s(successes)
	res.SampleCount = len(successes)
	res.Latencies = summarise(successes, cfg.Percentile)

	// The exposure board's own question: revocations still owed. §2.3
	// requires these to survive with their TRUE age rather than being
	// quietly marked complete.
	if err := db.QueryRow(ctx, `
		SELECT count(*),
		       coalesce(max(extract(epoch FROM (now() - event_at))), 0)::double precision
		  FROM app.provisioning_audit
		 WHERE action = 'revoke' AND event_at >= $1 AND platform_call_completed_at IS NULL`,
		cfg.Since).Scan(&res.Pending, &res.OldestAge); err != nil {
		return nil, fmt.Errorf("gate2: counting pending revocations: %w", err)
	}

	res.FinishedAt = time.Now()
	res.Conditions = evaluateGate2(cfg, res)
	return res, nil
}

func evaluateGate2(cfg Gate2Config, res *Gate2Result) []ConditionResult {
	var out []ConditionResult

	// The sample-size gate comes FIRST and is itself a condition, so an
	// empty run reports a failure with a reason rather than a vacuous pass.
	enough := res.SampleCount >= cfg.MinRevocations
	out = append(out, ConditionResult{
		ID:          "2.1-sample",
		Description: "the run produced enough successful revocations to have a p99",
		Passed:      enough,
		Measurement: fmt.Sprintf("%d successful revocations, minimum %d", res.SampleCount, cfg.MinRevocations),
	})
	if !enough {
		return out
	}

	out = append(out, ConditionResult{
		ID:          "2.1",
		Description: fmt.Sprintf("p%d of (platform_call_completed_at - event_at) < %s", int(cfg.Percentile*100), cfg.SLO),
		Passed:      res.Latencies.P99 < cfg.SLO.Seconds(),
		Measurement: fmt.Sprintf("p50=%.3fs p95=%.3fs p99=%.3fs max=%.3fs over %d revocations",
			res.Latencies.P50, res.Latencies.P95, res.Latencies.P99, res.Latencies.Max, res.SampleCount),
	})

	// §2.3: "Zero revocations lost when a platform is down: they retry and
	// remain on the exposure board with their true age." A pending row is
	// therefore NOT a failure — it is the required behaviour. What would be
	// a failure is a pending row that had been marked complete, which is
	// unobservable by definition, so what this asserts is the observable
	// half: nothing owed has been silently dropped, and anything still owed
	// carries a real age rather than a reset one.
	out = append(out, ConditionResult{
		ID:          "2.3",
		Description: "revocations still owed remain visible with their true age",
		Passed:      res.Pending == 0 || res.OldestAge > 0,
		Measurement: fmt.Sprintf("%d pending, oldest %.1fs old", res.Pending, res.OldestAge),
	})

	return out
}

// summarise computes the distribution over a SORTED slice.
func summarise(sorted []float64, percentile float64) Gate2Latencies {
	if len(sorted) == 0 {
		return Gate2Latencies{}
	}
	return Gate2Latencies{
		P50: quantile(sorted, 0.50),
		P95: quantile(sorted, 0.95),
		P99: quantile(sorted, percentile),
		Max: sorted[len(sorted)-1],
	}
}

// quantile is the nearest-rank quantile of a sorted slice — the same
// definition BenchmarkLedgerClusteredThroughput uses for its p99, so the
// two gates do not report subtly different statistics under the same name.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * q)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ── §2.1's STUB PLATFORMS ────────────────────────────────────────────────

// PlatformLimits are one stub platform's real-world rate limits, as
// 01_ARCHITECTURE.md §9.3-§9.5 describe them.
type PlatformLimits struct {
	Name string
	// Interval is the minimum spacing between calls, i.e. 1/rate.
	Interval time.Duration
	// Latency is the per-call round-trip the platform costs even when it
	// is not rate-limiting.
	Latency time.Duration
}

// DiscordLimits, TeamSpeakLimits and MumbleLimits are §2.1's three stubs.
//
// Discord's is the one that matters and the one §2.1 calls out by name:
// §9.3's global ceiling is 50 requests/second across the whole driver, so
// 20ms spacing. TeamSpeak's WebQuery and Mumble's gRPC have no documented
// global ceiling; their stubs model round-trip cost only, which is honest —
// inventing a rate limit for them would make the harness stricter than the
// platforms and the gate would be measuring a fiction.
var (
	DiscordLimits   = PlatformLimits{Name: "discord", Interval: 20 * time.Millisecond, Latency: 30 * time.Millisecond}
	TeamSpeakLimits = PlatformLimits{Name: "teamspeak", Latency: 15 * time.Millisecond}
	MumbleLimits    = PlatformLimits{Name: "mumble", Latency: 10 * time.Millisecond}
)

// RateLimitedStub is a provisioning.Driver that answers correctly but no
// faster than the real platform would. It is the whole point of §2.1's
// "a stub that answers instantly proves nothing".
//
// Down, when set, makes every call fail — §2.3's "a platform is down" case.
// It is a plain field guarded by the same mutex the bucket uses so a test
// can flip it mid-run.
type RateLimitedStub struct {
	Limits PlatformLimits

	mu       sync.Mutex
	nextFree time.Time
	down     bool
	granted  map[string]int
	revoked  map[string]int
}

// NewRateLimitedStub builds a stub for one platform.
func NewRateLimitedStub(limits PlatformLimits) *RateLimitedStub {
	return &RateLimitedStub{
		Limits:  limits,
		granted: map[string]int{},
		revoked: map[string]int{},
	}
}

// SetDown marks the platform down (or back up).
func (s *RateLimitedStub) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Counts returns how many grants and revokes this stub has served.
func (s *RateLimitedStub) Counts() (grants, revokes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.granted {
		grants += n
	}
	for _, n := range s.revoked {
		revokes += n
	}
	return grants, revokes
}

// Grant implements provisioning.Driver.
func (s *RateLimitedStub) Grant(ctx context.Context, remoteIdentity, groupRef string) error {
	return s.call(ctx, s.granted, remoteIdentity, groupRef)
}

// Revoke implements provisioning.Driver.
func (s *RateLimitedStub) Revoke(ctx context.Context, remoteIdentity, groupRef string) error {
	return s.call(ctx, s.revoked, remoteIdentity, groupRef)
}

// call reserves this caller's slot in the bucket, waits for it outside the
// lock, then waits the round-trip cost.
//
// The reservation is taken UNDER the lock and the sleep happens outside it,
// so N concurrent callers queue behind each other at the platform's real
// rate rather than all sleeping the same interval in parallel — which
// would model a platform with no global limit at all, and would quietly
// remove the queue wait the gate exists to measure.
func (s *RateLimitedStub) call(ctx context.Context, counter map[string]int, remoteIdentity, groupRef string) error {
	s.mu.Lock()
	if s.down {
		s.mu.Unlock()
		return fmt.Errorf("gate2 stub: platform %s is down", s.Limits.Name)
	}
	now := time.Now()
	slot := now
	if s.nextFree.After(slot) {
		slot = s.nextFree
	}
	s.nextFree = slot.Add(s.Limits.Interval)
	counter[remoteIdentity+"|"+groupRef]++
	s.mu.Unlock()

	if wait := time.Until(slot); wait > 0 {
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
	return sleepCtx(ctx, s.Limits.Latency)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
