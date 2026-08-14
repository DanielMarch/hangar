package esi_test

// Phase 20.2's gateway wiring, at the unit level. The end-to-end evidence
// against a faithful ESI simulation lives in test/load's integration suite
// (Gate 1's own harness); these cover the parts that are cheap to assert
// here and would otherwise only be exercised behind a build tag.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
)

// recordingLedger captures Reconcile so a test can assert on §5.5's "the
// server always wins" without needing a real bucket's internals.
type recordingLedger struct {
	ratelimit.Ledger
	group     string
	userKey   string
	maxTokens int
	remaining int
	calls     int
}

func (l *recordingLedger) Acquire(context.Context, ratelimit.AcquireRequest) (*ratelimit.Reservation, error) {
	return &ratelimit.Reservation{}, nil
}
func (l *recordingLedger) Settle(context.Context, *ratelimit.Reservation, int16, time.Time) error {
	return nil
}
func (l *recordingLedger) Reconcile(_ context.Context, group, userKey string, maxTokens, serverRemaining int) error {
	l.calls++
	l.group, l.userKey, l.maxTokens, l.remaining = group, userKey, maxTokens, serverRemaining
	return nil
}

func ledgerRequest() esi.Request {
	return esi.Request{
		Method: http.MethodGet, UpstreamPath: "/x",
		RateLimitGroup: "g", RateLimitMax: 10, RateLimitRealMax: 15,
		RateLimitWindow: time.Minute, UserKey: "hangar:1",
	}
}

// TestReconcileUsesTheServersOwnCeiling is B29's core: §5.5's bucket table
// says max_tokens is "from x-rate-limit, RECONCILED FROM X-Ratelimit-Limit",
// so when the server states a ceiling that is the one the reconciler
// converges against — not the catalogue value, and certainly not the
// call-site-reduced one.
func TestReconcileUsesTheServersOwnCeiling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ratelimit-Limit", "40/10m")
		w.Header().Set("X-Ratelimit-Remaining", "31")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ledger := &recordingLedger{}
	client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL, Ledger: ledger}

	_, err := client.Do(context.Background(), ledgerRequest())
	require.NoError(t, err)

	require.Equal(t, 1, ledger.calls, "a response carrying X-Ratelimit-Remaining must reconcile")
	require.Equal(t, "g", ledger.group)
	require.Equal(t, "hangar:1", ledger.userKey)
	require.Equal(t, 31, ledger.remaining)
	require.Equal(t, 40, ledger.maxTokens,
		"X-Ratelimit-Limit's ceiling wins over both the catalogue value and the reduced call-site one")
}

// TestReconcileFallsBackToTheUNREDUCEDCeiling is internal/sync/worker/
// reserve.go's rule enforced at the gateway: RateLimitMax may carry a
// call-site reserve (char-notification holds five tokens back for
// interactive callers) and feeding that fiction to the reconciler would
// desync the ledger from the truth it exists to import.
func TestReconcileFallsBackToTheUNREDUCEDCeiling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "7") // no X-Ratelimit-Limit
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ledger := &recordingLedger{}
	client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL, Ledger: ledger}

	_, err := client.Do(context.Background(), ledgerRequest())
	require.NoError(t, err)
	require.Equal(t, 15, ledger.maxTokens,
		"with no server ceiling, RateLimitRealMax (15) is used, never the reduced RateLimitMax (10)")
}

// TestAbsentRemainingHeaderIsNotAReadingOfZero is §5.5's headerless edge
// case at its most consequential: treating a missing header as
// remaining = 0 would inject a synthetic entry covering the whole bucket
// and stall the installation.
func TestAbsentRemainingHeaderIsNotAReadingOfZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ledger := &recordingLedger{}
	client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL, Ledger: ledger}

	_, err := client.Do(context.Background(), ledgerRequest())
	require.NoError(t, err)
	require.Zero(t, ledger.calls, "no X-Ratelimit-Remaining means no reading, which is not a reading of zero")
}

// TestEntityBreakerIsScopedToTheEntity is §5.8/B28: five consecutive 403s
// open the circuit for THAT (route, entity) pair and leave every other
// entity on the same route alone (Principle 3).
func TestEntityBreakerIsScopedToTheEntity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer doomed" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &esi.Client{
		HTTPClient:    srv.Client(),
		BaseURL:       srv.URL,
		EntityBreaker: breaker.NewEntityBreaker(time.Hour, nil),
	}
	doomed := esi.Request{Method: http.MethodGet, UpstreamPath: "/x", AccessToken: "doomed", EntityID: 1}
	healthy := esi.Request{Method: http.MethodGet, UpstreamPath: "/x", AccessToken: "fine", EntityID: 2}

	for i := 0; i < 5; i++ {
		resp, err := client.Do(context.Background(), doomed)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
	_, err := client.Do(context.Background(), doomed)
	require.ErrorIs(t, err, esi.ErrEntityBreakerOpen)

	resp, err := client.Do(context.Background(), healthy)
	require.NoError(t, err, "the route must stay live for every other entity")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestEntityBreakerIgnoresStatusesThatSayNothingAboutAuthorisation: a 5XX
// is an ESI outage and a 404 is often data. Counting either against an
// entity's 403 budget would take a corporation dark for fifteen minutes
// over something that has nothing to do with its roles.
func TestEntityBreakerIgnoresStatusesThatSayNothingAboutAuthorisation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &esi.Client{
		HTTPClient:    srv.Client(),
		BaseURL:       srv.URL,
		EntityBreaker: breaker.NewEntityBreaker(time.Hour, nil),
	}
	req := esi.Request{Method: http.MethodGet, UpstreamPath: "/x", EntityID: 9}
	for i := 0; i < 12; i++ {
		resp, err := client.Do(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	}
	// Still closed: only 403 opens it.
	_, err := client.Do(context.Background(), req)
	require.NoError(t, err)
}

// TestGlobalRoutesNeverConsultTheEntityBreaker — EntityID 0 means "no
// owner", and a global, unauthenticated route cannot 403 for an
// authorisation reason.
func TestGlobalRoutesNeverConsultTheEntityBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := &esi.Client{
		HTTPClient:    srv.Client(),
		BaseURL:       srv.URL,
		EntityBreaker: breaker.NewEntityBreaker(time.Hour, nil),
	}
	req := esi.Request{Method: http.MethodGet, UpstreamPath: "/status"} // EntityID stays 0
	for i := 0; i < 10; i++ {
		resp, err := client.Do(context.Background(), req)
		require.NoError(t, err, "an unowned route must never be refused by the entity breaker")
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
}

// TestResolvedLanguageIsSentAndKeyed is B23's other half. The resolved ESI
// language was already part of the cache key (§5.3) and was never actually
// SENT — invisible while the field was empty, and a correctness bug the
// moment internal/i18n was wired: the cache would key on a language the
// request never asked for.
func TestResolvedLanguageIsSentAndKeyed(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept-Language")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL, Language: "de", Tenant: "hangar"}
	_, err := client.Do(context.Background(), esi.Request{Method: http.MethodGet, UpstreamPath: "/x"})
	require.NoError(t, err)
	require.Equal(t, "de", got, "the resolved language must reach the wire, not only the cache key")

	// And the key really does partition on it — otherwise two locales would
	// share one cached body and the header would be decorative.
	base := cache.KeyInput{Method: "GET", Path: "/x", Tenant: "hangar", TokenSubject: "anonymous"}
	de, en := base, base
	de.ResolvedLanguage, en.ResolvedLanguage = "de", "en"
	require.NotEqual(t, cache.Key(de), cache.Key(en),
		"§5.3 puts the resolved language in the cache key; two locales must not share one entry")
}

// TestHeaderless429CarriesItsSnoozeToTheCaller: internal/esi has no idea
// what a subscription is, so §5.5's "snooze the affected subscription only"
// is expressed by returning the duration for internal/sync/worker to apply.
func TestHeaderless429CarriesItsSnoozeToTheCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL, TTLFloor: 90 * time.Second}
	resp, err := client.Do(context.Background(), esi.Request{Method: http.MethodGet, UpstreamPath: "/x"})
	require.NoError(t, err)
	require.True(t, resp.Is429Headerless)
	require.Equal(t, 90*time.Second, resp.SnoozeFor)
}
