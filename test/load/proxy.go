// Package load holds Gate 1's harness: a recording proxy that reproduces
// ESI's two governors faithfully, and the driver that runs an installation
// against it and writes the evidence artefacts 04_RELEASE_GATES.md §1.5
// requires.
//
// ── THIS PACKAGE DOES NOT RUN GATE 1 ─────────────────────────────────────
// Release-gate rule 6: instrumentation lands in a phase EARLIER than the
// one that reads it. Gate 1 is run by Phase 20.8, against a release
// candidate, at N=1 and N=3. Phase 20.2 owes the harness and evidence that
// its parts work — a gate run in the same phase as the wiring it measures
// is the tautology the split exists to prevent.
//
// ── WHY THE PROXY MUST BE A FLOATING WINDOW ──────────────────────────────
// 04_RELEASE_GATES.md §1.1 is explicit: "the proxy releases each request's
// cost exactly one window_size after that individual request. A proxy
// implementing a refill bucket would let a refill-based client pass, which
// defeats the gate." Refill is the thing 01_ARCHITECTURE.md §5.5 PROHIBITS
// the client from doing, so a lenient proxy would certify exactly the
// implementation the design rules out. bucket.admit below is therefore a
// list of (cost, at) pairs evicted by age, and never a counter that ticks
// upward with time.
package load

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── the cost table (01_ARCHITECTURE.md §5.5), from the SERVER's side ─────
//
// Deliberately restated here rather than imported from
// internal/esi/ratelimit. The proxy is the independent measurement: if the
// client's cost table were wrong, importing it would make the proxy wrong
// in exactly the same way and the gate would pass. A gate that shares its
// implementation with the thing it measures is not a measurement.
const (
	proxyCost429       = 0
	proxyCost2XX       = 2
	proxyCost3XX       = 1
	proxyCost4XXOther  = 5
	proxyCost5XX       = 0
	governor2Max       = 100
	governor2WindowDur = 60 * time.Second
)

// RouteLimit is one route's advertised Governor 1 bucket configuration, as
// the SERVER holds it — the values the proxy also echoes back in
// X-Ratelimit-Limit.
type RouteLimit struct {
	Group     string
	MaxTokens int
	Window    time.Duration
}

// RouteResolver maps an inbound request to the bucket it consumes from. A
// zero Group means "this route is not rate limited", which the proxy
// admits unconditionally.
type RouteResolver func(r *http.Request) RouteLimit

// Breach is one Governor 1 violation: a request that arrived when the
// bucket had no headroom left. breaches.json must be empty for Gate 1.1 to
// pass.
type Breach struct {
	At        time.Time `json:"at"`
	Group     string    `json:"group"`
	UserKey   string    `json:"user_key"`
	Path      string    `json:"path"`
	Available int       `json:"available"`
	MaxTokens int       `json:"max_tokens"`
}

// ConsumptionSample is one proxy-side reading of a bucket's aggregate
// consumption, for Gate 1.7's aggregate-consumption.csv at N=3: the point
// is that three replicas sharing a bucket never push the SERVER's view of
// consumption above max_tokens, which only the server can answer.
type ConsumptionSample struct {
	At        time.Time `json:"at"`
	Group     string    `json:"group"`
	UserKey   string    `json:"user_key"`
	Consumed  int       `json:"consumed"`
	MaxTokens int       `json:"max_tokens"`
}

type ledgerEntry struct {
	cost int
	at   time.Time
}

type bucket struct {
	entries []ledgerEntry
	limit   RouteLimit
}

// available computes headroom by evicting every entry whose window has
// elapsed and summing what remains. Eviction is by the individual entry's
// own timestamp — that is what makes this a floating window and not a
// refill bucket.
func (b *bucket) available(now time.Time) int {
	live := b.entries[:0]
	consumed := 0
	for _, e := range b.entries {
		if now.Sub(e.at) < b.limit.Window {
			live = append(live, e)
			consumed += e.cost
		}
	}
	b.entries = live
	return b.limit.MaxTokens - consumed
}

func (b *bucket) charge(cost int, now time.Time) {
	if cost == 0 {
		return
	}
	b.entries = append(b.entries, ledgerEntry{cost: cost, at: now})
}

// Proxy is the recording ESI simulator.
//
// It is an http.Handler rather than a real forwarding proxy: Gate 1 does
// not need CCP's data, it needs CCP's GOVERNORS, and standing between the
// installation and Tranquility for four hours at 5000 characters would be
// both slower and rude. Upstream is a stub that answers 200 with an empty
// JSON array unless an injection says otherwise.
type Proxy struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	resolver RouteResolver
	now      func() time.Time

	// Governor 2's fixed window, installation-wide.
	g2WindowStart time.Time
	g2Count       int

	breaches    []Breach
	consumption []ConsumptionSample
	injector    *Injector
	log         []InjectionRecord

	requests int
	served   map[int]int // status -> count
}

// NewProxy builds a proxy. resolver may be nil, in which case every route
// is unlimited (useful only for a smoke test); clock may be nil for the
// real one.
func NewProxy(resolver RouteResolver, injector *Injector, clock func() time.Time) *Proxy {
	if clock == nil {
		clock = time.Now
	}
	if resolver == nil {
		resolver = func(*http.Request) RouteLimit { return RouteLimit{} }
	}
	if injector == nil {
		injector = NewInjector(nil, clock)
	}
	return &Proxy{
		buckets: map[string]*bucket{}, resolver: resolver, now: clock,
		g2WindowStart: clock(), injector: injector, served: map[int]int{},
	}
}

// Server starts the proxy on a local httptest server. The caller closes it.
func (p *Proxy) Server() *httptest.Server { return httptest.NewServer(p) }

// ServeHTTP implements the whole simulation for one request.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := p.now()
	limit := p.resolver(r)
	key := bucketKey(limit.Group, userKeyOf(r))

	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++

	// ── Governor 2 first, exactly as ESI applies it ──────────────────────
	// It is installation-wide and route-agnostic: once the budget is spent
	// EVERY route answers 420, including ones that have never erred. That
	// ordering matters — a 420 must not first consume Governor 1 headroom
	// on a route the caller was entitled to use.
	if p.governor2Exceeded(now) {
		p.record420(w, now, limit)
		return
	}

	var b *bucket
	if limit.Group != "" {
		var ok bool
		b, ok = p.buckets[key]
		if !ok {
			b = &bucket{limit: limit}
			p.buckets[key] = b
		}
		b.limit = limit

		available := b.available(now)
		if available <= 0 {
			// The client sent a request it had no budget for. THIS is Gate
			// 1.1's failure: ESI answers 429, and a correct HANGAR should
			// never have got here because Governor 1 would have refused to
			// issue a reservation.
			p.breaches = append(p.breaches, Breach{
				At: now, Group: limit.Group, UserKey: userKeyOf(r),
				Path: r.URL.Path, Available: available, MaxTokens: limit.MaxTokens,
			})
			p.write(w, now, limit, b, http.StatusTooManyRequests, KindNone)
			return
		}
		p.consumption = append(p.consumption, ConsumptionSample{
			At: now, Group: limit.Group, UserKey: userKeyOf(r),
			Consumed: limit.MaxTokens - available, MaxTokens: limit.MaxTokens,
		})
	}

	kind, rec := p.injector.next(r, limit, now)
	if rec != nil {
		p.log = append(p.log, *rec)
	}

	status := http.StatusOK
	switch kind {
	case Kind429Headerless, Kind429RetryAfter:
		status = http.StatusTooManyRequests
	case Kind403Consecutive:
		status = http.StatusForbidden
	case Kind4XXBurst:
		status = http.StatusBadRequest
	case Kind5XXSustained:
		status = http.StatusInternalServerError
	}
	p.write(w, now, limit, b, status, kind)
}

// governor2Exceeded rolls the FIXED 60-second window over and reports
// whether the budget is spent. Fixed, not sliding: §5.7 specifies a fixed
// window and the client's own Governor 2 implements one, so a sliding
// proxy would disagree with a correct client at every window boundary.
func (p *Proxy) governor2Exceeded(now time.Time) bool {
	if now.Sub(p.g2WindowStart) >= governor2WindowDur {
		p.g2WindowStart = now
		p.g2Count = 0
	}
	return p.g2Count >= governor2Max
}

func (p *Proxy) record420(w http.ResponseWriter, now time.Time, limit RouteLimit) {
	p.g2Count++
	p.served[statusErrorLimited]++
	p.log = append(p.log, InjectionRecord{
		At: now, Kind: string(KindErrorBudgetExhausted), Path: "*", Status: statusErrorLimited,
		Note: "Governor 2 budget exhausted — every route answers 420 until the window rolls over",
	})
	if limit.Group != "" {
		w.Header().Set("X-Ratelimit-Limit", formatLimit(limit))
	}
	w.WriteHeader(statusErrorLimited)
	_, _ = w.Write([]byte(`{"error":"error limited"}`))
}

// statusErrorLimited is ESI's 420. Not an IANA code, so net/http has no
// constant for it.
const statusErrorLimited = 420

func (p *Proxy) write(w http.ResponseWriter, now time.Time, limit RouteLimit, b *bucket, status int, kind InjectionKind) {
	cost := serverCost(status)
	if b != nil {
		b.charge(cost, now)
	}
	if status < 200 || status >= 400 {
		p.g2Count++
	}
	p.served[status]++

	if limit.Group != "" && kind != Kind429Headerless {
		remaining := 0
		if b != nil {
			remaining = b.available(now)
			if remaining < 0 {
				remaining = 0
			}
		}
		switch kind {
		case KindServerReportsLower:
			// §1.3: "Server reports lower X-Ratelimit-Remaining than local
			// — local converges downward within one request."
			remaining = maxInt(0, remaining-serverDivergenceStep)
		case KindServerReportsHigher:
			// "...local converges upward, never above max_tokens."
			remaining = minInt(limit.MaxTokens, remaining+serverDivergenceStep)
		}
		w.Header().Set("X-Ratelimit-Limit", formatLimit(limit))
		w.Header().Set("X-Ratelimit-Remaining", strconv.Itoa(remaining))
	}
	if kind == Kind429RetryAfter {
		w.Header().Set("Retry-After", strconv.Itoa(int(RetryAfterSeconds)))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Last-Modified", now.UTC().Format(http.TimeFormat))
	w.WriteHeader(status)
	if status == http.StatusOK {
		_, _ = w.Write([]byte(`[]`))
		return
	}
	_, _ = w.Write([]byte(`{"error":"injected"}`))
}

// RetryAfterSeconds is the Retry-After the 429-with-Retry-After injection
// sends. §1.3 requires the subscription be snoozed for EXACTLY this long,
// so the number has to be one a test can assert on rather than a jittered
// value.
const RetryAfterSeconds int64 = 17

// serverDivergenceStep is how far the server's reported remaining is
// pushed away from the truth by the two reconciliation injections. Larger
// than Gate 1.3's tolerance of 1 on purpose: an injection inside the
// tolerance would pass whether or not the client reconciled at all.
const serverDivergenceStep = 7

// serverCost is the cost table applied from the server's side.
func serverCost(status int) int {
	switch {
	case status == http.StatusTooManyRequests:
		return proxyCost429
	case status >= 200 && status < 300:
		return proxyCost2XX
	case status >= 300 && status < 400:
		return proxyCost3XX
	case status >= 400 && status < 500:
		return proxyCost4XXOther
	default:
		return proxyCost5XX
	}
}

// formatLimit renders X-Ratelimit-Limit in the "<max-tokens>/<window>" form
// 01_ARCHITECTURE.md §5.5 documents, preferring the hour suffix when the
// window divides evenly — which is what CCP sends, and therefore what the
// client's parser has to survive.
func formatLimit(l RouteLimit) string {
	if l.Window%time.Hour == 0 {
		return fmt.Sprintf("%d/%dh", l.MaxTokens, int(l.Window/time.Hour))
	}
	return fmt.Sprintf("%d/%dm", l.MaxTokens, int(l.Window/time.Minute))
}

// userKeyOf reproduces §5.5's userID dimension from what the SERVER can
// see: the bearer token on an authenticated route, the source address
// otherwise. The proxy deliberately does not read any HANGAR-specific
// header — it must measure what ESI would measure.
func userKeyOf(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.RemoteAddr
}

func bucketKey(group, userKey string) string { return group + "\x00" + userKey }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── readouts ─────────────────────────────────────────────────────────────

// Breaches returns every Governor 1 violation recorded. Gate 1.1 requires
// this to be empty.
func (p *Proxy) Breaches() []Breach {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Breach(nil), p.breaches...)
}

// Consumption returns the proxy-side aggregate-consumption samples
// (Gate 1.7's artefact at N=3).
func (p *Proxy) Consumption() []ConsumptionSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ConsumptionSample(nil), p.consumption...)
}

// InjectionLog returns every injected condition and the response it
// produced (§1.5's adversarial-log.jsonl).
func (p *Proxy) InjectionLog() []InjectionRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]InjectionRecord(nil), p.log...)
}

// Served reports how many responses of each status the proxy sent, and the
// total request count.
func (p *Proxy) Served() (byStatus map[int]int, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[int]int, len(p.served))
	for k, v := range p.served {
		out[k] = v
	}
	return out, p.requests
}

// MaxConsumedFor reports the highest consumption the proxy ever observed
// for one bucket — Gate 1.7's question, answerable only from this side.
func (p *Proxy) MaxConsumedFor(group, userKey string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	highest := 0
	for _, s := range p.consumption {
		if s.Group == group && s.UserKey == userKey && s.Consumed > highest {
			highest = s.Consumed
		}
	}
	return highest
}

// SpecRouteResolver builds a RouteResolver from an ESI OpenAPI document,
// matching a concrete request path against the spec's templated paths
// segment by segment and reading each operation's `x-rate-limit`
// extension.
//
// It exists so Phase 20.8's run uses the SAME group/max/window values the
// installation itself ingested, rather than a hand-maintained second table
// that would silently diverge the first time CCP retunes a bucket — which
// would make the proxy's idea of a breach differ from ESI's, and a Gate 1
// pass mean nothing.
func SpecRouteResolver(spec []byte) (RouteResolver, error) {
	var doc struct {
		Paths map[string]map[string]struct {
			RateLimit *struct {
				Group      string `json:"group"`
				MaxTokens  int    `json:"max-tokens"`
				WindowSize string `json:"window-size"`
			} `json:"x-rate-limit"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("load: parsing spec for the route resolver: %w", err)
	}

	type templated struct {
		segments []string
		limit    RouteLimit
	}
	var routes []templated
	for path, methods := range doc.Paths {
		op, ok := methods["get"]
		if !ok || op.RateLimit == nil {
			continue
		}
		window, err := parseWindow(op.RateLimit.WindowSize)
		if err != nil {
			continue
		}
		routes = append(routes, templated{
			segments: strings.Split(strings.Trim(path, "/"), "/"),
			limit:    RouteLimit{Group: op.RateLimit.Group, MaxTokens: op.RateLimit.MaxTokens, Window: window},
		})
	}

	return func(r *http.Request) RouteLimit {
		got := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for _, route := range routes {
			if len(route.segments) != len(got) {
				continue
			}
			matched := true
			for i, seg := range route.segments {
				if strings.HasPrefix(seg, "{") {
					continue
				}
				if seg != got[i] {
					matched = false
					break
				}
			}
			if matched {
				return route.limit
			}
		}
		return RouteLimit{}
	}, nil
}

func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty window")
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, err
	}
	switch s[len(s)-1] {
	case 'm', 'M':
		return time.Duration(n) * time.Minute, nil
	case 'h', 'H':
		return time.Duration(n) * time.Hour, nil
	default:
		return 0, fmt.Errorf("unrecognised window suffix in %q", s)
	}
}
