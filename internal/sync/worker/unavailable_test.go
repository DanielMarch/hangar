package worker

// PHASE 20.2. A gateway refusal is not a job failure — see unavailable.go's
// header for why returning one to River is the "do not spin" rule inverted.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/sync"
)

const testTTLFloor = 300 * time.Second

func TestClassifyRefusalCoversEveryDeliberateRefusal(t *testing.T) {
	t.Parallel()
	now := time.Now()

	cases := []struct {
		name       string
		err        error
		wantReason string
		wantUntil  time.Time
	}{
		{
			name:       "Governor 1 has no headroom — retry at the moment §5.5 computes",
			err:        &ratelimit.RetryAtError{RetryAt: now.Add(42 * time.Second)},
			wantReason: outcomeRateLimited,
			wantUntil:  now.Add(42 * time.Second),
		},
		{
			name:       "Governor 2 has paused the installation",
			err:        fmt.Errorf("wrapped: %w", esi.ErrErrorBudgetPaused),
			wantReason: outcomeErrorBudgetPaused,
			wantUntil:  now.Add(testTTLFloor),
		},
		{
			name:       "the entity's 403 breaker is open",
			err:        fmt.Errorf("wrapped: %w", esi.ErrEntityBreakerOpen),
			wantReason: outcomeEntityBreakerOpen,
			wantUntil:  now.Add(breaker.DefaultEntityProbeTTL),
		},
		{
			name:       "the route's 5XX breaker is open",
			err:        fmt.Errorf("wrapped: %w", esi.ErrBreakerOpen),
			wantReason: outcomeRouteBreakerOpen,
			wantUntil:  now.Add(breaker.DefaultRouteProbeTTL),
		},
		{
			name:       "no corporation member can act",
			err:        fmt.Errorf("wrapped: %w", sync.ErrNoEligibleActingCharacter),
			wantReason: outcomeNoActingCharacter,
			wantUntil:  now.Add(breaker.DefaultEntityProbeTTL),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := classifyRefusal(c.err, testTTLFloor, now)
			require.True(t, ok, "this is a deliberate refusal, not a failure")
			require.Equal(t, c.wantReason, r.reason)
			require.WithinDuration(t, c.wantUntil, r.until, time.Millisecond)
		})
	}
}

// TestClassifyRefusalLeavesRealFailuresAlone: a parse error, a database
// error or a transport failure must still fail the job and be retried by
// River. Swallowing those as "unavailable" would turn a broken handler into
// a permanently quiet subscription — which is precisely the class of
// silence this phase exists to remove.
func TestClassifyRefusalLeavesRealFailuresAlone(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		nil,
		errors.New("connection reset by peer"),
		fmt.Errorf("worker: unexpected status 500 from /x"),
	} {
		_, ok := classifyRefusal(err, testTTLFloor, time.Now())
		require.False(t, ok, "%v must reach River as a failure", err)
	}
}

// TestRetryAtInThePastIsFlooredToTTLFloor guards the one way a refusal can
// still spin: a RetryAt that has already elapsed by the time the worker
// reads it would snooze until a moment in the past, which the claim query
// treats as not snoozed at all.
func TestRetryAtInThePastIsFlooredToTTLFloor(t *testing.T) {
	t.Parallel()
	now := time.Now()
	r, ok := classifyRefusal(&ratelimit.RetryAtError{RetryAt: now.Add(-time.Minute)}, testTTLFloor, now)
	require.True(t, ok)
	require.True(t, r.until.After(now), "a snooze must always be in the future, or the planner reclaims immediately")
	require.WithinDuration(t, now.Add(testTTLFloor), r.until, time.Millisecond)
}
