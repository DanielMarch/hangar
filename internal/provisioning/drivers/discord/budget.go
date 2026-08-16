package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hangar-project/hangar/internal/store/gen"
)

// InvalidBudgetStore is the subset of gen.Querier the invalid-request
// budget needs — internal/esi/ratelimit.ErrorBudgetStore's shape, against
// app.discord_invalid_budget instead of app.esi_error_budget.
type InvalidBudgetStore interface {
	InitDiscordInvalidBudget(ctx context.Context) error
	GetDiscordInvalidBudget(ctx context.Context) (gen.AppDiscordInvalidBudget, error)
	RecordInvalidAgainstDiscordBudget(ctx context.Context, invalidWindow time.Duration) (gen.AppDiscordInvalidBudget, error)
	SetDiscordInvalidBudgetPaused(ctx context.Context, paused bool) error
	// PHASE 22 (defect B-5, one driver over). The resume path that does
	// not require a request to have been made.
	ResumeDiscordInvalidBudgetIfWindowElapsed(ctx context.Context, invalidWindow time.Duration) (gen.AppDiscordInvalidBudget, error)
}

// InvalidBudget is the installation-wide Discord invalid-request-response
// (401/403/429) budget (01_ARCHITECTURE.md §9.3): 10,000 per fixed
// 10-minute window, cluster-shared through one Postgres row — the same
// mechanism as internal/esi/ratelimit.Governor2 (§5.7), reused by shape
// rather than by import (a Discord-specific type keeps this package
// dependency-free of internal/esi, which is about a wholly different
// upstream). See db/migrations/00038's comment for why "shares the
// mechanism from §5.7" (a fixed window) won out over the surrounding
// "rolling 10 minutes" prose — a genuine spec tension, resolved in favor
// of the concrete, testable, already-proven mechanism.
//
// Unlike Governor2, there is no separate resume threshold: the fixed
// window's own rollover (RecordInvalidAgainstDiscordBudget's atomic
// UPDATE) is what un-pauses, once 10 minutes have passed since the window
// that tripped the pause — matching "warn at 50%, pause at 80%" being the
// only two thresholds the roadmap names, with no third "resume at" value
// to invent.
type InvalidBudget struct {
	store InvalidBudgetStore

	window  time.Duration
	max     int
	warnAt  int // invalid_count at which a warning fires (50% of max)
	pauseAt int // invalid_count at which processing pauses (80% of max)

	clock Clock
	log   *slog.Logger

	mu        sync.Mutex
	cached    gen.AppDiscordInvalidBudget
	cachedAt  time.Time
	cacheTTL  time.Duration
	haveCache bool
}

// NewInvalidBudget constructs the budget tracker. warnPct/pausePct are
// percentages of max (e.g. 50, 80) — HANGAR_DISCORD_INVALID_WARN_PCT/
// HANGAR_DISCORD_INVALID_PAUSE_PCT.
func NewInvalidBudget(store InvalidBudgetStore, window time.Duration, max, warnPct, pausePct int, clock Clock, log *slog.Logger) *InvalidBudget {
	if clock == nil {
		clock = SystemClock
	}
	if log == nil {
		log = slog.Default()
	}
	return &InvalidBudget{
		store: store, window: window, max: max,
		warnAt:  max * warnPct / 100,
		pauseAt: max * pausePct / 100,
		clock:   clock, log: log.With("component", "provisioning.drivers.discord.budget"),
		cacheTTL: time.Second,
	}
}

// Init ensures the singleton row exists. Safe to call repeatedly.
func (b *InvalidBudget) Init(ctx context.Context) error {
	return b.store.InitDiscordInvalidBudget(ctx)
}

// ShouldCount reports whether one response outcome counts against the
// budget: 401 or 403 always count; 429 counts UNLESS Discord's
// X-RateLimit-Scope header on that response was "shared" (01_ARCHITECTURE.md
// §9.3 edge case: "Shared-resource 429s must not be charged against the
// invalid budget the same way — check X-RateLimit-Scope before
// accounting"). Any other status never counts.
func ShouldCount(statusCode int, rateLimitScope string) bool {
	switch statusCode {
	case 401, 403:
		return true
	case 429:
		return rateLimitScope != "shared"
	default:
		return false
	}
}

// RecordInvalid registers one counted response outcome and applies the
// warn/pause thresholds.
func (b *InvalidBudget) RecordInvalid(ctx context.Context) error {
	row, err := b.store.RecordInvalidAgainstDiscordBudget(ctx, b.window)
	if err != nil {
		return fmt.Errorf("discord: budget: record invalid: %w", err)
	}
	b.setCache(row)

	count := int(row.InvalidCount)
	switch {
	case count >= b.pauseAt && !row.Paused:
		b.log.WarnContext(ctx, "discord: invalid-request budget pause threshold reached", "count", count, "max", b.max, "pause_at", b.pauseAt)
		if err := b.setPaused(ctx, true); err != nil {
			return err
		}
	case count < b.pauseAt && row.Paused:
		// The window rolled over (RecordInvalidAgainstDiscordBudget resets
		// invalid_count to 1 on rollover) — un-pause now that we're
		// demonstrably back under the pause threshold.
		b.log.InfoContext(ctx, "discord: invalid-request budget window rolled over, resuming", "count", count)
		if err := b.setPaused(ctx, false); err != nil {
			return err
		}
	case count >= b.warnAt:
		b.log.WarnContext(ctx, "discord: invalid-request budget warn threshold reached", "count", count, "max", b.max, "warn_at", b.warnAt)
	}
	return nil
}

func (b *InvalidBudget) setPaused(ctx context.Context, paused bool) error {
	if err := b.store.SetDiscordInvalidBudgetPaused(ctx, paused); err != nil {
		return fmt.Errorf("discord: budget: set paused: %w", err)
	}
	b.mu.Lock()
	b.cached.Paused = paused
	b.mu.Unlock()
	return nil
}

// IsPaused reports the current pause state through a 1-second in-process
// cache, refreshing from the database when stale or empty — and, when that
// refresh finds the driver paused, asking the database whether the window
// that paused it has elapsed.
//
// ── PHASE 22: THE SAME DEADLOCK AS internal/esi/ratelimit's ──────────────
// This type was written to mirror Governor 2's mechanism, and it mirrored
// its defect too. RecordInvalid holds the only un-pause branch, it runs
// only on a counted RESPONSE, and Client.Do returns ErrInvalidBudgetPaused
// before it sends — so a paused driver made no request, counted no
// invalid, never rolled the window over, and never resumed. Discord
// provisioning went silent permanently after one burst of 401/403/429.
//
// The resume is evaluated in the database for the reasons
// db/queries/discord_invalid_budget.sql gives; unlike §5.7 there is no
// resume threshold to honour, because the window rollover IS the resume
// condition here.
func (b *InvalidBudget) IsPaused(ctx context.Context) (bool, error) {
	if row, ok := b.getCache(); ok {
		return row.Paused, nil
	}
	row, err := b.store.GetDiscordInvalidBudget(ctx)
	if err != nil {
		return false, fmt.Errorf("discord: budget: get budget: %w", err)
	}
	if row.Paused {
		row, err = b.store.ResumeDiscordInvalidBudgetIfWindowElapsed(ctx, b.window)
		if err != nil {
			return false, fmt.Errorf("discord: budget: evaluating resume: %w", err)
		}
		if !row.Paused {
			b.log.InfoContext(ctx, "discord: invalid-request budget window elapsed while paused, resuming",
				"count", row.InvalidCount, "max", b.max,
				"note", "no request was needed to notice")
		}
	}
	b.setCache(row)
	return row.Paused, nil
}

func (b *InvalidBudget) getCache() (gen.AppDiscordInvalidBudget, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.haveCache || b.clock.Now().Sub(b.cachedAt) >= b.cacheTTL {
		return gen.AppDiscordInvalidBudget{}, false
	}
	return b.cached, true
}

func (b *InvalidBudget) setCache(row gen.AppDiscordInvalidBudget) {
	b.mu.Lock()
	b.cached, b.cachedAt, b.haveCache = row, b.clock.Now(), true
	b.mu.Unlock()
}
