package ratelimit

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRateLimitLimit(t *testing.T) {
	max, window, err := ParseRateLimitLimit("20/1m")
	require.NoError(t, err)
	require.Equal(t, 20, max)
	require.Equal(t, time.Minute, window)

	max, window, err = ParseRateLimitLimit("100/24h")
	require.NoError(t, err)
	require.Equal(t, 100, max)
	require.Equal(t, 24*time.Hour, window)

	_, _, err = ParseRateLimitLimit("garbage")
	require.Error(t, err)
	_, _, err = ParseRateLimitLimit("20/1s")
	require.Error(t, err, "only m/h suffixes are documented")
}

// Test429ExemptionOverrides4XXCost (roadmap exit criterion): a 429 charges
// 0, not the general "other 4XX" cost of 5 — the 429 row in the cost table
// takes precedence over the 4XX row beneath it.
func Test429ExemptionOverrides4XXCost(t *testing.T) {
	require.Equal(t, Cost429, ClassifyCost(http.StatusTooManyRequests, false))
	require.NotEqual(t, Cost4XXOther, ClassifyCost(http.StatusTooManyRequests, false))
	require.Equal(t, Cost4XXOther, ClassifyCost(http.StatusForbidden, false))
}

// TestTransportErrorChargesWorstCase (roadmap exit criterion): a timeout —
// no response at all — charges the worst case (5), the same as an "other
// 4XX", because the server may have processed the request even though the
// client never learned the outcome.
func TestTransportErrorChargesWorstCase(t *testing.T) {
	require.Equal(t, CostTransportError, ClassifyCost(0, true))
	require.EqualValues(t, 5, ClassifyCost(0, true))
}

func TestClassifyCostTable(t *testing.T) {
	cases := []struct {
		status int
		want   int16
	}{
		{200, Cost2XX}, {204, Cost2XX}, {299, Cost2XX},
		{301, Cost3XX}, {304, Cost3XX},
		{400, Cost4XXOther}, {404, Cost4XXOther}, {403, Cost4XXOther},
		{429, Cost429},
		{500, Cost5XX}, {503, Cost5XX},
	}
	for _, c := range cases {
		require.Equal(t, c.want, ClassifyCost(c.status, false), "status %d", c.status)
	}
}

// Test429SnoozesExactlyRetryAfter (roadmap exit criterion): the affected
// subscription snoozes for exactly Retry-After, no more, no less.
func Test429SnoozesExactlyRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "42")
	h.Set("X-Ratelimit-Remaining", "10")
	out := ClassifyResponse(http.StatusTooManyRequests, h, false, 5*time.Minute)
	require.Equal(t, Cost429, out.Cost)
	require.Equal(t, 42*time.Second, out.SnoozeFor)
	require.False(t, out.Is429Headerless, "headers were present")
}

// TestReconcilerHandlesHeaderless429 (roadmap exit criterion): a 429 with
// no rate-limit headers at all charges nothing, snoozes ttl_floor, and is
// flagged headerless — never inferred as remaining=0, never ignored.
func TestReconcilerHandlesHeaderless429(t *testing.T) {
	ttlFloor := 5 * time.Minute
	out := ClassifyResponse(http.StatusTooManyRequests, http.Header{}, false, ttlFloor)
	require.Equal(t, Cost429, out.Cost, "a headerless 429 must still charge nothing")
	require.Equal(t, ttlFloor, out.SnoozeFor, "must snooze ttl_floor, not spin")
	require.True(t, out.Is429Headerless)
	require.False(t, out.ServerRemainingOK, "no remaining reading must never be fabricated")
}

func TestParseRemaining(t *testing.T) {
	n, ok := ParseRemaining("17")
	require.True(t, ok)
	require.Equal(t, 17, n)

	_, ok = ParseRemaining("")
	require.False(t, ok, "absent header must not be confused with remaining=0")

	_, ok = ParseRemaining("not-a-number")
	require.False(t, ok)
}

func TestClassifyResponseGovernor2Population(t *testing.T) {
	require.True(t, ClassifyResponse(500, http.Header{}, false, time.Minute).IsErrorForGovernor2)
	require.True(t, ClassifyResponse(404, http.Header{}, false, time.Minute).IsErrorForGovernor2)
	require.True(t, ClassifyResponse(429, http.Header{}, false, time.Minute).IsErrorForGovernor2, "429s count toward Governor 2 too")
	require.False(t, ClassifyResponse(200, http.Header{}, false, time.Minute).IsErrorForGovernor2)
	require.False(t, ClassifyResponse(304, http.Header{}, false, time.Minute).IsErrorForGovernor2)
	require.True(t, ClassifyResponse(0, nil, true, time.Minute).IsErrorForGovernor2, "a transport error counts as an error")
}
