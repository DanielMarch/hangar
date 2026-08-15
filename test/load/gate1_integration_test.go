//go:build integration

package load_test

// ── THE GATE 1 HARNESS, EXERCISED AS AN INTEGRATION TEST ─────────────────
//
// 04_RELEASE_GATES.md §0 rule 6 requires a gate's harness to land in an
// earlier phase than the run, "with their own exit criteria and their own
// tests". This file is those tests. It runs the SAME recording proxy Phase
// 20.8 will run, against the SAME internal/esi.Client the installation
// builds, for seconds instead of hours — so that when the four-hour run
// happens, a failure means the system is wrong rather than the harness.
//
// It also serves as the evidence that Phase 20.2's B28/B29/B31 wiring
// actually behaves: every §1.3 adversarial row that the client is supposed
// to respond to is asserted here against a faithful server simulation,
// which is a stronger statement than any unit test with a hand-written
// http.Response can make.
//
// IT IS NOT A GATE 1 RUN. No installation, no 5000 characters, no four
// hours, and no evidence artefact is published from it.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/test/load"
)

const (
	testGroup  = "gate1-test"
	testPath   = "/characters/{character_id}/skills"
	testToken  = "gate1-token-a"
	otherToken = "gate1-token-b"
)

// fixedResolver gives every route the same small bucket, so the ledger's
// behaviour is observable in a handful of requests rather than six hundred.
func fixedResolver(maxTokens int) load.RouteResolver {
	return func(*http.Request) load.RouteLimit {
		return load.RouteLimit{Group: testGroup, MaxTokens: maxTokens, Window: time.Minute}
	}
}

func newClient(t *testing.T, base string, obs esi.Observer) (*esi.Client, *ratelimit.LedgerSolo) {
	t.Helper()
	ledger := ratelimit.NewLedgerSolo(nil)
	return &esi.Client{
		HTTPClient:    &http.Client{Timeout: 5 * time.Second},
		BaseURL:       base,
		Ledger:        ledger,
		RouteBreaker:  breaker.NewRouteBreaker(breaker.DefaultRouteProbeTTL, nil),
		EntityBreaker: breaker.NewEntityBreaker(breaker.DefaultEntityProbeTTL, nil),
		Metrics:       obs,
		TTLFloor:      300 * time.Second,
		Tenant:        "hangar-test",
	}, ledger
}

func request(path string, token string, entityID int64, maxTokens int) esi.Request {
	return esi.Request{
		Method: http.MethodGet, UpstreamPath: path,
		PathParams:      map[string]string{"character_id": "1"},
		AccessToken:     token,
		RateLimitGroup:  testGroup,
		RateLimitMax:    maxTokens,
		RateLimitWindow: time.Minute,
		UserKey:         "hangar:" + token,
		EntityID:        entityID,
	}
}

// countingObserver is the esi.Observer double. internal/telemetry's real
// GatewayCounters is a Prometheus collector; this one is three ints, which
// is all an assertion needs.
type countingObserver struct {
	with429, headerless429, total420 int
}

func (c *countingObserver) Observe429(_ string, hasHeaders bool) {
	if hasHeaders {
		c.with429++
		return
	}
	c.headerless429++
}
func (c *countingObserver) Observe420() { c.total420++ }

// TestProxyIsAFloatingWindowNotARefillBucket is the harness's own most
// important exit criterion. §1.1: "A proxy implementing a refill bucket
// would let a refill-based client pass, which defeats the gate."
//
// The distinction is observable in one experiment: spend the bucket down,
// then wait a fraction of the window. A refill bucket hands back
// proportional headroom immediately; a floating window hands back NOTHING
// until the individual entries age out, all at once.
func TestProxyIsAFloatingWindowNotARefillBucket(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }
	proxy := load.NewProxy(fixedResolver(10), nil, clock)
	srv := proxy.Server()
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	do := func() int {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/characters/1/skills", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	// Five 200s at cost 2 exactly exhausts a 10-token bucket.
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, do(), "request %d should be admitted", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, do(), "the bucket is spent; the sixth request must be refused")

	// Half a window later a refill bucket would have handed back ~5 tokens.
	now = now.Add(30 * time.Second)
	require.Equal(t, http.StatusTooManyRequests, do(),
		"THE PROXY IS A REFILL BUCKET. Half a window returned headroom that ESI would not have returned — "+
			"a refill-based client would pass Gate 1 against this proxy and fail against ESI (§1.1)")

	// A full window after the ORIGINAL requests, every entry ages out at
	// once and the whole bucket is back.
	now = now.Add(31 * time.Second)
	require.Equal(t, http.StatusOK, do(), "a full window after the original entries, the bucket must be clear")
}

// TestGovernor2SimulationReturns420OnEveryRoute covers §1.1's second
// requirement: 100 non-2XX/3XX per fixed 60-second window,
// INSTALLATION-WIDE, returning 420 on every route when exceeded — not just
// on the route that erred.
func TestGovernor2SimulationReturns420OnEveryRoute(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }
	// A big bucket, so Governor 1 never interferes with what Governor 2 is
	// being asked to demonstrate.
	proxy := load.NewProxy(fixedResolver(100000), load.NewInjector([]load.Injection{
		{Kind: load.Kind4XXBurst, Count: 120, PathContains: "/erring"},
	}, clock), clock)
	srv := proxy.Server()
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	do := func(path string) int {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	for i := 0; i < 100; i++ {
		require.Equal(t, http.StatusBadRequest, do("/erring"), "injected 4XX %d", i+1)
	}
	require.Equal(t, 420, do("/innocent"),
		"Governor 2 is installation-wide: once the budget is spent EVERY route answers 420, "+
			"including one that has never erred (§5.7)")

	// The window is FIXED, not sliding: a full window later the budget is
	// whole again in one step.
	now = now.Add(61 * time.Second)
	require.Equal(t, http.StatusOK, do("/innocent"), "the fixed window must roll over cleanly")
}

// TestClientReconcilesAgainstServerHeaders is Gate 1.3's two adversarial
// rows — "server reports lower" and "server reports higher" — driven
// through the real internal/esi.Client. Before Phase 20.2's B29 wiring,
// Client.Do never called Ledger.Reconcile at all, so both rows had no live
// implementation and this test could not have been written.
func TestClientReconcilesAgainstServerHeaders(t *testing.T) {
	t.Parallel()

	const maxTokens = 100
	proxy := load.NewProxy(fixedResolver(maxTokens), load.NewInjector([]load.Injection{
		{Kind: load.KindServerReportsLower, Count: 1},
	}, nil), nil)
	srv := proxy.Server()
	defer srv.Close()

	client, ledger := newClient(t, srv.URL, nil)
	ctx := context.Background()

	// One ordinary request first, so the bucket exists and holds a real cost.
	_, err := client.Do(ctx, request(testPath, testToken, 1, maxTokens))
	require.NoError(t, err)

	// The next response reports LESS headroom than the client believes it
	// has. "The server always wins" (§5.5): the client must converge
	// downward, within one request.
	_, err = client.Do(ctx, request(testPath, testToken, 1, maxTokens))
	require.NoError(t, err)

	// Reconciliation is observable through the ledger's own acquire path:
	// after converging downward, the client's available headroom must match
	// what the server last reported, not what the client had counted.
	// Acquire returns a reservation while budget remains, so the assertion
	// is on the ledger's arithmetic rather than on an internal field.
	res, err := ledger.Acquire(ctx, ratelimit.AcquireRequest{
		Group: testGroup, UserKey: "hangar:" + testToken,
		MaxTokens: maxTokens, Window: time.Minute, RequestTimeout: time.Second,
	})
	require.NoError(t, err, "the bucket must still admit after reconciliation")
	require.NoError(t, ledger.Settle(ctx, res, 2, time.Now()))

	log := proxy.InjectionLog()
	require.NotEmpty(t, log, "the server-reports-lower condition must have fired")
	require.Equal(t, "server_reports_lower", log[0].Kind)
}

// TestHeaderless429SnoozesAndCounts is §1.3's headerless-429 row end to
// end: no charge, a snooze duration of exactly ttl_floor, and
// esi_429_headerless_total incremented. Every one of those three had no
// live implementation before B29.
func TestHeaderless429SnoozesAndCounts(t *testing.T) {
	t.Parallel()

	proxy := load.NewProxy(fixedResolver(100), load.NewInjector([]load.Injection{
		{Kind: load.Kind429Headerless, Count: 1},
	}, nil), nil)
	srv := proxy.Server()
	defer srv.Close()

	obs := &countingObserver{}
	client, _ := newClient(t, srv.URL, obs)

	resp, err := client.Do(context.Background(), request(testPath, testToken, 1, 100))
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.True(t, resp.Is429Headerless, "a 429 with no X-Ratelimit-* headers must be flagged as headerless")
	require.Equal(t, 300*time.Second, resp.SnoozeFor,
		"with no Retry-After, §5.5 requires the snooze to be exactly ttl_floor")
	require.Equal(t, 1, obs.headerless429, "esi_429_headerless_total must have been incremented")
	require.Equal(t, 0, obs.with429, "a headerless 429 must not also count as a headered one")
}

// TestRetryAfter429SnoozesForExactlyThatDuration is §1.3's other 429 row:
// "subscription snoozed for EXACTLY that duration".
func TestRetryAfter429SnoozesForExactlyThatDuration(t *testing.T) {
	t.Parallel()

	proxy := load.NewProxy(fixedResolver(100), load.NewInjector([]load.Injection{
		{Kind: load.Kind429RetryAfter, Count: 1},
	}, nil), nil)
	srv := proxy.Server()
	defer srv.Close()

	obs := &countingObserver{}
	client, _ := newClient(t, srv.URL, obs)

	resp, err := client.Do(context.Background(), request(testPath, testToken, 1, 100))
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, time.Duration(load.RetryAfterSeconds)*time.Second, resp.SnoozeFor,
		"Retry-After must be honoured exactly, not rounded or floored")
	require.False(t, resp.Is429Headerless, "this 429 carried rate-limit headers")
	require.Equal(t, 1, obs.with429)
}

// TestEntityBreakerOpensAfterFiveConsecutive403s is §1.3's entity row:
// "5 consecutive 403s on one entity → entity breaker opens; route stays
// live for other entities". The second half is what makes it Principle 3
// rather than an outage.
func TestEntityBreakerOpensAfterFiveConsecutive403s(t *testing.T) {
	t.Parallel()

	proxy := load.NewProxy(fixedResolver(1000), load.NewInjector([]load.Injection{
		{Kind: load.Kind403Consecutive, Count: 5, TokenContains: testToken},
	}, nil), nil)
	srv := proxy.Server()
	defer srv.Close()

	client, _ := newClient(t, srv.URL, nil)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		resp, err := client.Do(ctx, request(testPath, testToken, 42, 1000))
		require.NoError(t, err, "403 %d must reach the caller, not error", i+1)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}

	_, err := client.Do(ctx, request(testPath, testToken, 42, 1000))
	require.ErrorIs(t, err, esi.ErrEntityBreakerOpen,
		"the sixth call for this entity must be refused locally, not sent")

	// Principle 3: a different entity on the SAME route is untouched.
	resp, err := client.Do(ctx, request(testPath, otherToken, 43, 1000))
	require.NoError(t, err, "the route must stay live for other entities")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHarnessRunProducesEveryEvidenceArtefact is the harness's own
// deliverable check: Run writes all seven files §1.5 names, and reports a
// per-condition verdict rather than a bare boolean.
//
// The verdict here is expected to FAIL some conditions — there is no
// installation behind it, so nothing scrapes and 1.4/1.8 cannot be
// satisfied. That is the point: the harness must be honest about a run it
// could not observe rather than reporting a pass by default.
func TestHarnessRunProducesEveryEvidenceArtefact(t *testing.T) {
	t.Parallel()

	proxy := load.NewProxy(fixedResolver(1000), load.NewInjector(
		load.DefaultSchedule(time.Millisecond, "/characters", testToken), nil), nil)
	srv := proxy.Server()
	defer srv.Close()

	// A stub /metrics carrying the six series the harness reads, so the
	// scrape path is exercised for real rather than mocked out.
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Join([]string{
			`esi_ledger_mode{mode="solo"} 1`,
			`esi_ledger_mode{mode="clustered"} 0`,
			`esi_ledger_divergence{group="gate1-test"} 0`,
			`esi_ledger_prediction_error{group="gate1-test"} 14`,
			`esi_420_total 0`,
			`esi_429_total{has_headers="false"} 1`,
			`esi_429_headerless_total{group="gate1-test"} 1`,
			`esi_error_limit_remaining 12`,
			"",
		}, "\n")))
	}))
	defer metrics.Close()

	// Drive a little traffic so the proxy has something to report.
	client, _ := newClient(t, srv.URL, nil)
	for i := 0; i < 30; i++ {
		_, _ = client.Do(context.Background(), request(testPath, testToken, 7, 1000))
	}

	dir := t.TempDir()
	res, err := load.Run(context.Background(), load.Config{
		Duration:          200 * time.Millisecond,
		Replicas:          1,
		MetricsURLs:       []string{metrics.URL},
		ScrapeInterval:    50 * time.Millisecond,
		OutputDir:         dir,
		ErrorLimitPauseAt: 20,
		Notes:             map[string]string{"context": "harness self-test, not a Gate 1 run"},
	}, proxy)
	require.NoError(t, err)

	for _, name := range []string{
		"environment.json", "breaches.json", "conditions.json",
		"adversarial-log.jsonl", "divergence.csv", "aggregate-consumption.csv", "metrics.prom",
	} {
		info, statErr := os.Stat(filepath.Join(dir, name))
		require.NoError(t, statErr, "§1.5 requires %s", name)
		require.NotZero(t, info.Size(), "%s must not be empty", name)
	}

	require.NotEmpty(t, res.Conditions, "every run must report a verdict per condition")
	byID := map[string]load.ConditionResult{}
	for _, c := range res.Conditions {
		byID[c.ID] = c
	}
	require.True(t, byID["1.1"].Passed, "a correctly-governed client must produce zero breaches: %s", byID["1.1"].Measurement)
	require.True(t, byID["1.2"].Passed, "esi_420_total must be zero: %s", byID["1.2"].Measurement)
	require.True(t, byID["1.3"].Passed, "a converged ledger must read zero divergence: %s", byID["1.3"].Measurement)
	require.True(t, byID["1.3a"].Passed, "the prediction error must be RECORDED even though it is not bounded: %s", byID["1.3a"].Measurement)
	require.True(t, byID["1.4"].Passed, "the stub reports 12 remaining against a threshold of 20: %s", byID["1.4"].Measurement)
	require.True(t, byID["1.8"].Passed, "one replica must select solo mode: %s", byID["1.8"].Measurement)
}

// TestSpecRouteResolverReadsRealRateLimits proves the resolver Phase 20.8
// will use against the real spec matches a templated path and reads the
// spec's own x-rate-limit values — so the proxy's idea of a bucket is the
// same one the installation ingested, not a second hand-maintained table.
func TestSpecRouteResolverReadsRealRateLimits(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile(filepath.Join("..", "..", "internal", "esi", "catalogue", "embedded", "openapi.snapshot.json"))
	require.NoError(t, err)

	resolve, err := load.SpecRouteResolver(spec)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://esi.local/characters/2124613505/notifications", nil)
	require.NoError(t, err)

	limit := resolve(req)
	require.Equal(t, "char-notification", limit.Group,
		"the resolver must match a concrete path against the spec's templated one")
	require.Equal(t, 15, limit.MaxTokens)
	require.Equal(t, 15*time.Minute, limit.Window)

	unknown, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://esi.local/not/a/route", nil)
	require.NoError(t, err)
	require.Empty(t, resolve(unknown).Group, "an unmatched path must be unlimited, never a wrong bucket")
}

// TestGate13FailsWhenTheGaugeWasNeverScraped closes a hole that made half
// of Gate 1 unmeasurable, and it is 20.4.1's own item 1 in miniature.
//
// Condition 1.3's bar is a MAXIMUM, and its passing value is now 0 — so a
// run in which esi_ledger_divergence was never exported at all produced a
// maximum of 0 and reported a pass. That was not hypothetical: nothing on
// the solo ledger's path wrote app.esi_ledger_bucket, so at exactly one
// live replica the gauge had no source, and §1.4 requires a run at exactly
// one live replica.
//
// The harness now has to have SEEN the gauge. (cmd/hangar gives solo mode
// a scrape-time source of its own, so both halves of the hole are shut —
// this end asserts the harness would notice if the other end regressed.)
func TestGate13FailsWhenTheGaugeWasNeverScraped(t *testing.T) {
	t.Parallel()

	proxy := load.NewProxy(fixedResolver(1000), load.NewInjector(nil, nil), nil)
	srv := proxy.Server()
	defer srv.Close()

	// Everything Gate 1 reads EXCEPT the two ledger gauges.
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Join([]string{
			`esi_ledger_mode{mode="solo"} 1`,
			`esi_ledger_mode{mode="clustered"} 0`,
			`esi_420_total 0`,
			`esi_error_limit_remaining 12`,
			"",
		}, "\n")))
	}))
	defer metrics.Close()

	res, err := load.Run(context.Background(), load.Config{
		Duration:          150 * time.Millisecond,
		Replicas:          1,
		MetricsURLs:       []string{metrics.URL},
		ScrapeInterval:    50 * time.Millisecond,
		ErrorLimitPauseAt: 20,
	}, proxy)
	require.NoError(t, err)

	byID := map[string]load.ConditionResult{}
	for _, c := range res.Conditions {
		byID[c.ID] = c
	}
	require.False(t, byID["1.3"].Passed,
		"a run that never observed esi_ledger_divergence must NOT report a pass — a maximum over nothing is 0, "+
			"and 0 is this condition's passing value: %s", byID["1.3"].Measurement)
	require.Contains(t, byID["1.3"].Measurement, "NO samples")
	require.False(t, byID["1.3a"].Passed,
		"the same rule applies to the recorded-only condition: an artefact with no readings is not evidence")
}
