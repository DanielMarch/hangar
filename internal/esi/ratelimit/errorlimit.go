package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hangar-project/hangar/internal/store/gen"
)

// ErrorBudgetStore is the subset of gen.Querier Governor 2 needs.
type ErrorBudgetStore interface {
	InitErrorBudget(ctx context.Context) error
	GetErrorBudget(ctx context.Context) (gen.AppEsiErrorBudget, error)
	RecordErrorAgainstBudget(ctx context.Context, errorWindow time.Duration) (gen.AppEsiErrorBudget, error)
	SetErrorBudgetPaused(ctx context.Context, paused bool) error
	// PHASE 22 (defect B-5). The resume path that does not require a
	// request to have been made. See db/queries/esi_error_budget.sql.
	ResumeErrorBudgetIfRecovered(ctx context.Context, errorWindow time.Duration, maxErrors int32, resumeAt int32) (gen.AppEsiErrorBudget, error)
}

// AlertFunc fires a critical alert. Phase 4 has no alerting catalogue to
// dispatch through yet (that lands in Phase 14); this is the seam Phase 14
// wires platform.esi.error_limited into. The default logs at Error level so
// the condition is never silent in the meantime.
type AlertFunc func(ctx context.Context, name string, attrs map[string]any)

// Governor2 is the installation-wide error-limit engine (§5.7): 100
// non-2XX/3XX responses per fixed 60-second window, cluster-shared through
// one Postgres row with no solo fast path — a row touched only on error
// responses costs nothing worth optimising. Read through a 1-second
// in-process cache; the pause/resume decision is proactive hysteresis
// (pause at `PauseAt` remaining, resume at `ResumeAt`), never reactive to a
// 420.
type Governor2 struct {
	store ErrorBudgetStore

	window   time.Duration
	max      int
	pauseAt  int
	resumeAt int

	clock Clock
	log   *slog.Logger
	alert AlertFunc

	mu        sync.Mutex
	cached    gen.AppEsiErrorBudget
	cachedAt  time.Time
	cacheTTL  time.Duration
	haveCache bool
}

// NewGovernor2 constructs Governor 2 against store, using cfg's window and
// hysteresis thresholds. alert is called on every observed 420 (a critical
// condition per §5.7); pass nil to use the default log-only alerter.
func NewGovernor2(store ErrorBudgetStore, window time.Duration, max, pauseAt, resumeAt int, clock Clock, log *slog.Logger, alert AlertFunc) *Governor2 {
	if clock == nil {
		clock = SystemClock
	}
	if log == nil {
		log = slog.Default()
	}
	if alert == nil {
		alert = func(ctx context.Context, name string, attrs map[string]any) {
			log.ErrorContext(ctx, "CRITICAL ALERT: "+name, "attrs", attrs)
		}
	}
	return &Governor2{
		store: store, window: window, max: max, pauseAt: pauseAt, resumeAt: resumeAt,
		clock: clock, log: log.With("component", "esi.ratelimit.governor2"), alert: alert,
		cacheTTL: time.Second,
	}
}

// RecordError registers one non-2XX/3XX response outcome against the
// installation-wide budget. is420 marks an actual observed 420 from ESI
// (not the proactive pause — that's a HANGAR-side decision, not something
// ESI sent), which is always a critical alert regardless of the pause
// state.
func (g *Governor2) RecordError(ctx context.Context, is420 bool) error {
	row, err := g.store.RecordErrorAgainstBudget(ctx, g.window)
	if err != nil {
		return fmt.Errorf("ratelimit: governor2: record error: %w", err)
	}
	g.setCache(row)

	remaining := g.max - int(row.ErrorCount)
	if err := g.applyHysteresis(ctx, remaining, row.Paused); err != nil {
		return err
	}

	if is420 {
		// A 420 is a global condition, never per-route, even on a
		// Governor-1-covered route (§5.7) — always critical.
		g.alert(ctx, "platform.esi.error_limited", map[string]any{
			"error_count": row.ErrorCount, "window_start": row.WindowStart,
		})
		// A 420 slipping through despite proactive pause is still
		// paused going forward — force it, in case this response
		// arrived from a race with a not-yet-applied pause.
		if !row.Paused {
			if err := g.setPaused(ctx, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyHysteresis pauses at remaining<=pauseAt and resumes at
// remaining>=resumeAt, doing nothing in the gap between the two thresholds
// — that gap is the whole point of hysteresis (§5.7: "otherwise the
// installation oscillates in and out of pause").
func (g *Governor2) applyHysteresis(ctx context.Context, remaining int, currentlyPaused bool) error {
	switch {
	case !currentlyPaused && remaining <= g.pauseAt:
		g.log.WarnContext(ctx, "governor2: proactive pause", "remaining", remaining, "threshold", g.pauseAt)
		return g.setPaused(ctx, true)
	case currentlyPaused && remaining >= g.resumeAt:
		g.log.InfoContext(ctx, "governor2: resume", "remaining", remaining, "threshold", g.resumeAt)
		return g.setPaused(ctx, false)
	default:
		return nil
	}
}

func (g *Governor2) setPaused(ctx context.Context, paused bool) error {
	if err := g.store.SetErrorBudgetPaused(ctx, paused); err != nil {
		return fmt.Errorf("ratelimit: governor2: set paused: %w", err)
	}
	g.mu.Lock()
	g.cached.Paused = paused
	g.mu.Unlock()
	return nil
}

// IsPaused reports the current pause state through a 1-second in-process
// cache (§5.7), refreshing from the database when the cache is stale or
// empty — and, when that refresh finds the installation paused, evaluating
// whether it should still be.
//
// ── PHASE 22, DEFECT B-5: WHY THE RESUME LIVES HERE ──────────────────────
//
// applyHysteresis below is reachable only from RecordError, RecordError
// only from internal/esi.Client's response path, and a response requires a
// request. So the resume condition was evaluated only in the one state
// where it can never be true, and never in the state that needs it:
//
//	paused ⇒ no request ⇒ no error recorded ⇒ resume never evaluated
//
// and the fixed window did not advance either, because
// RecordErrorAgainstBudget is what advances it. The pause was permanent
// for the life of the process. Gate 1 measured it at v1.0.0-rc1: 3h58m of
// a four-hour run with no request at N=1, 3h43m45s at N=3.
//
// IsPaused is the ONLY Governor 2 code that runs while paused —
// esi.Client.Do calls it before the ledger, before the breakers' verdict
// is acted on, before anything is sent — which makes it the one place a
// resume can be evaluated without inventing a second lifecycle. A ticker
// was the alternative and was rejected: it would need starting, stopping
// and owning by every process that builds a gateway, to do on a schedule
// what the existing 1-second cache refresh already does on demand.
//
// READING THE ROW IS NOT ENOUGH, which is why this is a second query and
// not a comparison in Go. `remaining` is derived from error_count, and
// error_count belongs to a window that may no longer apply; the resume
// decision therefore has to be made against the database's own clock, in
// one atomic statement, for the reasons that query's comment gives.
func (g *Governor2) IsPaused(ctx context.Context) (bool, error) {
	if row, ok := g.getCache(); ok {
		return row.Paused, nil
	}
	row, err := g.store.GetErrorBudget(ctx)
	if err != nil {
		return false, fmt.Errorf("ratelimit: governor2: get budget: %w", err)
	}
	if row.Paused {
		row, err = g.resumeIfRecovered(ctx, row)
		if err != nil {
			return false, err
		}
	}
	g.setCache(row)
	return row.Paused, nil
}

// resumeIfRecovered asks the database whether the pause still holds,
// returning the row as it stands afterwards. The caller has already
// established that the last read said paused; a row that comes back
// unpaused was un-paused by this statement or by a concurrent replica's,
// and either is the same answer.
func (g *Governor2) resumeIfRecovered(ctx context.Context, paused gen.AppEsiErrorBudget) (gen.AppEsiErrorBudget, error) {
	row, err := g.store.ResumeErrorBudgetIfRecovered(ctx, g.window, int32(g.max), int32(g.resumeAt))
	if err != nil {
		return paused, fmt.Errorf("ratelimit: governor2: evaluating resume: %w", err)
	}
	if !row.Paused {
		g.log.InfoContext(ctx, "governor2: resume",
			"remaining", g.max-int(row.ErrorCount), "threshold", g.resumeAt,
			"note", "the error budget window elapsed or recovered while paused; no request was needed to notice")
	}
	return row, nil
}

func (g *Governor2) getCache() (gen.AppEsiErrorBudget, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.haveCache || g.clock.Now().Sub(g.cachedAt) >= g.cacheTTL {
		return gen.AppEsiErrorBudget{}, false
	}
	return g.cached, true
}

func (g *Governor2) setCache(row gen.AppEsiErrorBudget) {
	g.mu.Lock()
	g.cached, g.cachedAt, g.haveCache = row, g.clock.Now(), true
	g.mu.Unlock()
}

// Init ensures the singleton row exists. Safe to call repeatedly (ON
// CONFLICT DO NOTHING).
func (g *Governor2) Init(ctx context.Context) error {
	return g.store.InitErrorBudget(ctx)
}
