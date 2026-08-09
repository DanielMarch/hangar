package esi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/stretchr/testify/require"
)

// TestDataLevel404DoesNotTripBreaker (roadmap exit criterion): a 404 on
// /characters/{id}/ship — the roadmap's own example, "a docked character"
// — must be recorded as data, not a failure, and must never trip the
// circuit breaker. RouteBreaker opens after 10 consecutive 5XX; this sends
// 15 consecutive 404s (more than that threshold) through the real Do()
// pipeline and asserts the breaker never leaves StateClosed.
func TestDataLevel404DoesNotTripBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Character does not have a ship"}`))
	}))
	defer srv.Close()

	routeBreaker := breaker.NewRouteBreaker(time.Minute, nil)
	client := &esi.Client{
		HTTPClient:   srv.Client(),
		BaseURL:      srv.URL,
		RouteBreaker: routeBreaker,
	}

	const upstreamPath = "/characters/{character_id}/ship"
	for i := 0; i < 15; i++ {
		resp, err := client.Do(context.Background(), esi.Request{
			Method: http.MethodGet, UpstreamPath: upstreamPath,
			PathParams: map[string]string{"character_id": "2112625428"},
		})
		require.NoError(t, err, "iteration %d: Do must not itself error on a 404 — it's a legitimate response", i)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Equal(t, breaker.StateClosed, routeBreaker.State(upstreamPath),
			"iteration %d: breaker must stay closed — a 404 is data, not a failure", i)
	}
}

// TestBreakerStillOpensOn5xx is the control: the same pipeline DOES trip
// the breaker on real 5XX failures, so TestDataLevel404DoesNotTripBreaker
// above is proof the 404 case is specifically exempted, not that nothing
// ever opens this breaker.
func TestBreakerStillOpensOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	routeBreaker := breaker.NewRouteBreaker(time.Minute, nil)
	client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL, RouteBreaker: routeBreaker}

	const upstreamPath = "/characters/{character_id}/skills"
	for i := 0; i < 10; i++ {
		_, err := client.Do(context.Background(), esi.Request{
			Method: http.MethodGet, UpstreamPath: upstreamPath,
			PathParams: map[string]string{"character_id": "2112625428"},
		})
		require.NoError(t, err)
	}
	require.Equal(t, breaker.StateOpen, routeBreaker.State(upstreamPath), "10 consecutive 5XX responses must open the breaker")
}
