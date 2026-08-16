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
	// recorded counts RecordErrorAgainstBudget calls. In production that
	// query runs once per non-2XX/3XX RESPONSE, so it is the closest thing
	// this fake has to "a request was made" — which is exactly what
	// TestErrorLimitResumesWithoutARequest needs to hold still.
	recorded int
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
	s.recorded++
	now := s.clock.Now()
	if now.Sub(s.windowStart) >= errorWindow {
		s.windowStart = now
		s.errorCount = 1
	} else {
		s.errorCount++
	}
	return gen.AppEsiErrorBudget{WindowStart: s.windowStart, ErrorCount: s.errorCount, Paused: s.paused}, nil
}

// ResumeErrorBudgetIfRecovered mirrors db/queries/esi_error_budget.sql's
// statement of the same name, including the detail that decides it: every
// CASE reads the PRE-UPDATE tuple, so the resume test and the window
// rollover agree about whether the window elapsed.
func (s *fakeErrorBudgetStore) ResumeErrorBudgetIfRecovered(ctx context.Context, errorWindow time.Duration, maxErrors int32, resumeAt int32) (gen.AppEsiErrorBudget, error) {
	elapsed := s.clock.Now().Sub(s.windowStart) >= errorWindow
	recovered := s.paused && (elapsed || maxErrors-s.errorCount >= resumeAt)
	if elapsed {
		s.windowStart = s.clock.Now()
		s.errorCount = 0
	}
	if recovered {
		s.paused = false
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

// TestErrorLimitResumesWithoutARequest is defect B-5's regression test.
//
// ── WHY THE TEST ABOVE PROVED NOTHING ABOUT THIS ─────────────────────────
// It resumes by calling RecordError, and so did every other test in this
// package. RecordError is reachable in production only from
// internal/esi.Client's RESPONSE path, and while paused Client.Do returns
// ErrErrorBudgetPaused before it sends — so the branch every unit test
// exercised was the one branch production could never reach. A test that
// calls RecordError to un-pause proves the hysteresis arithmetic and says
// nothing about whether an installation can get out of a pause.
//
// So this test never calls RecordError after the pause, and asserts that
// it didn't.
func TestErrorLimitResumesWithoutARequest(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store := &fakeErrorBudgetStore{clock: clock}

	g2 := NewGovernor2(store, time.Minute, 100 /* max */, 20 /* pauseAt */, 60 /* resumeAt */, clock, nil, nil)
	require.NoError(t, g2.Init(ctx))

	for i := 0; i < 80; i++ {
		require.NoError(t, g2.RecordError(ctx, false))
	}
	paused, err := g2.IsPaused(ctx)
	require.NoError(t, err)
	require.True(t, paused, "80 errors against a max of 100 must trip the proactive pause at remaining=20")

	recordedAtPause := store.recorded

	// The pause must HOLD while the window that caused it is still running
	// — otherwise the fix has simply removed the pause.
	clock.Advance(g2.cacheTTL)
	paused, err = g2.IsPaused(ctx)
	require.NoError(t, err)
	require.True(t, paused, "the pause must hold while the window that caused it is still running")

	// The fixed 60-second window elapses. Nothing else happens: no request,
	// no response, no restart. This is the state v1.0.0-rc1 sat in for
	// 3h58m of a four-hour Gate 1 run.
	clock.Advance(time.Minute)
	paused, err = g2.IsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused, "a paused installation must resume on the clock, with no request and no restart")

	require.Equal(t, recordedAtPause, store.recorded,
		"the resume must not depend on an error having been recorded — that is the deadlock")
	require.False(t, store.paused, "the resume must be durable, not merely a cached read")
	require.Zero(t, store.errorCount,
		"an elapsed window's error_count must not be left describing a window that no longer applies")
}

// TestErrorLimitResumeKeepsHysteresis pins the half of §5.7 that the fix
// could most easily have traded away: resume at resumeAt, never at
// pauseAt. Both cases below have a window that has NOT elapsed, so the
// only branch that can fire is the remaining-versus-resumeAt one — which
// exists for the race where another replica rolled the window over between
// this replica's read and its resume evaluation.
func TestErrorLimitResumeKeepsHysteresis(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name       string
		errorCount int32
		wantPaused bool
	}{
		{"inside the hysteresis gap stays paused", 70, true}, // remaining 30: above pauseAt=20, below resumeAt=60
		{"at the resume threshold resumes", 40, false},       // remaining 60: exactly resumeAt
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			store := &fakeErrorBudgetStore{
				clock: clock, windowStart: clock.Now(), inited: true,
				errorCount: tc.errorCount, paused: true,
			}
			g2 := NewGovernor2(store, time.Minute, 100, 20, 60, clock, nil, nil)

			paused, err := g2.IsPaused(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.wantPaused, paused)
			require.Equal(t, tc.wantPaused, store.paused, "the durable row must agree with the reading")
			require.Zero(t, store.recorded, "no request is made while paused")
		})
	}
}
