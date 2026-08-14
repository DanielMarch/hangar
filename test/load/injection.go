package load

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// InjectionKind enumerates 04_RELEASE_GATES.md §1.3's adversarial
// conditions. Every row of that table that the PROXY can produce is here;
// the two rows it cannot (killing and restarting a replica) belong to the
// harness's process control, not to a response, and are recorded in
// transition-log.jsonl instead.
type InjectionKind string

const (
	KindNone                 InjectionKind = ""
	Kind4XXBurst             InjectionKind = "burst_4xx"
	Kind429Headerless        InjectionKind = "429_headerless"
	Kind429RetryAfter        InjectionKind = "429_retry_after"
	KindServerReportsLower   InjectionKind = "server_reports_lower"
	KindServerReportsHigher  InjectionKind = "server_reports_higher"
	Kind5XXSustained         InjectionKind = "sustained_5xx"
	Kind403Consecutive       InjectionKind = "consecutive_403"
	KindErrorBudgetExhausted InjectionKind = "error_budget_exhausted"
)

// Injection schedules one adversarial condition.
//
// Count is how many CONSECUTIVE matching requests the condition applies
// to, which is the whole point for two of the rows: "sustained 5XX on one
// route" must be at least ten to open the route breaker, and "5 consecutive
// 403s on one entity" must be exactly five to open the entity breaker.
// Injecting one and hoping is how a breaker test passes without a breaker.
type Injection struct {
	// After is the delay from the injector's start at which this condition
	// becomes eligible.
	After time.Duration
	Kind  InjectionKind
	// PathContains narrows the condition to one route family; empty means
	// any route. §1.3's rows are explicitly scoped ("on one group", "on one
	// route", "on one entity") because Gate 1.5 measures that SIBLINGS were
	// unaffected, and a condition applied everywhere cannot show that.
	PathContains string
	// TokenContains narrows it to one caller — the "one entity" dimension.
	TokenContains string
	Count         int
}

// InjectionRecord is one line of §1.5's adversarial-log.jsonl: what was
// injected, against what, and what the proxy answered.
type InjectionRecord struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Path   string    `json:"path"`
	Group  string    `json:"group,omitempty"`
	Status int       `json:"status,omitempty"`
	Note   string    `json:"note,omitempty"`
}

// Injector applies a schedule of adversarial conditions.
type Injector struct {
	mu       sync.Mutex
	start    time.Time
	now      func() time.Time
	schedule []*pending
}

type pending struct {
	spec      Injection
	remaining int
}

// NewInjector builds an injector over a schedule. clock may be nil.
func NewInjector(schedule []Injection, clock func() time.Time) *Injector {
	if clock == nil {
		clock = time.Now
	}
	inj := &Injector{now: clock, start: clock()}
	for _, s := range schedule {
		count := s.Count
		if count < 1 {
			count = 1
		}
		inj.schedule = append(inj.schedule, &pending{spec: s, remaining: count})
	}
	return inj
}

// Reset restarts the schedule's clock. Used by the harness between the N=1
// and N=3 runs so both see the same sequence at the same offsets.
func (i *Injector) Reset() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.start = i.now()
	for _, p := range i.schedule {
		if p.remaining == 0 {
			p.remaining = maxInt(1, p.spec.Count)
		}
	}
}

// next returns the condition to apply to this request, if any, and the log
// record for it. The FIRST eligible entry in schedule order wins, so a
// schedule is read as a sequence rather than a set — which is what lets
// "burst of 4XX, then a 429, then reconciliation" be expressed at all.
func (i *Injector) next(r *http.Request, limit RouteLimit, now time.Time) (InjectionKind, *InjectionRecord) {
	i.mu.Lock()
	defer i.mu.Unlock()

	elapsed := now.Sub(i.start)
	for _, p := range i.schedule {
		if p.remaining <= 0 || elapsed < p.spec.After {
			continue
		}
		if p.spec.PathContains != "" && !strings.Contains(r.URL.Path, p.spec.PathContains) {
			continue
		}
		if p.spec.TokenContains != "" && !strings.Contains(userKeyOf(r), p.spec.TokenContains) {
			continue
		}
		p.remaining--
		return p.spec.Kind, &InjectionRecord{
			At: now, Kind: string(p.spec.Kind), Path: r.URL.Path, Group: limit.Group,
		}
	}
	return KindNone, nil
}

// Pending reports how many injections have not yet fired — the harness
// fails a run whose schedule did not complete, because an adversarial
// condition that never fired is a condition the gate did not test.
func (i *Injector) Pending() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	total := 0
	for _, p := range i.schedule {
		total += p.remaining
	}
	return total
}

// DefaultSchedule is §1.3's table, ordered so each condition has room to
// land before the next begins. Offsets are relative and deliberately
// coarse; Phase 20.8 scales them across the 4-hour run.
//
// entityToken/routePath scope the two breaker conditions to ONE caller and
// ONE route respectively, so Gate 1.5 ("failure stayed scoped") has
// siblings to compare against.
func DefaultSchedule(spacing time.Duration, routePath, entityToken string) []Injection {
	at := func(n int) time.Duration { return time.Duration(n) * spacing }
	return []Injection{
		{After: at(1), Kind: Kind4XXBurst, Count: 3, PathContains: routePath},
		{After: at(2), Kind: Kind429Headerless, Count: 1, PathContains: routePath},
		{After: at(3), Kind: Kind429RetryAfter, Count: 1, PathContains: routePath},
		{After: at(4), Kind: KindServerReportsLower, Count: 1, PathContains: routePath},
		{After: at(5), Kind: KindServerReportsHigher, Count: 1, PathContains: routePath},
		// Ten is the route breaker's threshold (§5.8), so this is the
		// smallest burst that proves it opens rather than that it counts.
		{After: at(6), Kind: Kind5XXSustained, Count: 10, PathContains: routePath},
		// Five is the entity breaker's threshold, scoped to one token.
		{After: at(7), Kind: Kind403Consecutive, Count: 5, TokenContains: entityToken},
	}
}
