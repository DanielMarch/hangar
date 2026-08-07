package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/stretchr/testify/require"
)

// fakeErrorBudgetStore mirrors db/queries/esi_error_budget.sql's
// RecordErrorAgainstBudget fixed-window semantics in memory, driven by the
// same fakeClock the test controls, so Governor 2 is exercised without a
// database.
type fakeErrorBudgetStore struct {
	clock       *fakeClock
	windowStart time.Time
	errorCount  int32
	paused      bool
	inited      bool
}

func (s *fakeErrorBudgetStore) InitErrorBudget(ctx context.Context) error {
	if !s.inited {
		s.windowStart = s.clock.Now()
		s.inited = true
	}
	return nil
}

func (s *fakeErrorBudgetStore) GetErrorBudget(ctx context.Context) (gen.AppEsiErrorBudget, error) {
	return gen.AppEsiErrorBudget{WindowStart: s.windowStart, ErrorCount: s.errorCount, Paused: s.paused}, nil
}

func (s *fakeErrorBudgetStore) RecordErrorAgainstBudget(ctx context.Context, errorWindow time.Duration) (gen.AppEsiErrorBudget, error) {
	now := s.clock.Now()
	if now.Sub(s.windowStart) >= errorWindow {
		s.windowStart = now
		s.errorCount = 1
	} else {
		s.errorCount++
	}
	return gen.AppEsiErrorBudget{WindowStart: s.windowStart, ErrorCount: s.errorCount, Paused: s.paused}, nil
}

func (s *fakeErrorBudgetStore) SetErrorBudgetPaused(ctx context.Context, paused bool) error {
	s.paused = paused
	return nil
}

// TestErrorLimitProactivePause (roadmap exit criterion): pause fires at the
// configured remaining threshold BEFORE a 420 is observed; resume uses the
// higher threshold; an observed 420 triggers a global pause and a critical
// alert.
func TestErrorLimitProactivePause(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store := &fakeErrorBudgetStore{clock: clock}

	var alerts []string
	g2 := NewGovernor2(store, time.Minute, 100 /* max */, 20 /* pauseAt */, 60, /* resumeAt */
		clock, nil, func(ctx context.Context, name string, attrs map[string]any) { alerts = append(alerts, name) })
	require.NoError(t, g2.Init(ctx))

	// Drive the error count up to remaining=21 (still above pauseAt=20):
	// no pause yet.
	for i := 0; i < 79; i++ {
		require.NoError(t, g2.RecordError(ctx, false))
	}
	paused, err := g2.IsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused, "must not pause before the threshold")

	// One more error brings remaining to 20 — exactly the pause
	// threshold — and must pause proactively, with no 420 ever observed.
	require.NoError(t, g2.RecordError(ctx, false))
	paused, err = g2.IsPaused(ctx)
	require.NoError(t, err)
	require.True(t, paused, "must pause proactively at the configured remaining threshold")
	require.Empty(t, alerts, "a proactive pause is not itself a critical alert — only an observed 420 is")

	// Errors continue to accumulate while paused (Governor 2 itself
	// doesn't stop counting; it's the caller's job to stop issuing
	// requests once paused). Resume must not fire until remaining climbs
	// back up to 60, not merely above 20 — the hysteresis gap.
	require.NoError(t, g2.RecordError(ctx, false)) // remaining=19, still paused
	paused, _ = g2.IsPaused(ctx)
	require.True(t, paused)

	// Roll the fixed window over and let it refill implicitly by
	// starting a fresh window with zero errors, simulating recovery.
	clock.Advance(2 * time.Minute)
	// The window resets on the NEXT recorded error/read; force a reset
	// by reading through RecordError once (cost of one error), then
	// verify remaining is high enough to resume — 100-1=99 >= 60.
	require.NoError(t, g2.RecordError(ctx, false))
	paused, err = g2.IsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused, "must resume once remaining reaches the (higher) resume threshold")

	// A real 420 is always a critical alert, and forces a pause
	// regardless of the counted remaining.
	require.NoError(t, g2.RecordError(ctx, true))
	require.Contains(t, alerts, "platform.esi.error_limited")
	paused, _ = g2.IsPaused(ctx)
	require.True(t, paused, "an observed 420 must pause even if hysteresis math alone wouldn't have")
}
