package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/stretchr/testify/require"
)

// recordingLedger wraps a real ratelimit.Ledger and records every
// AcquireRequest that passes through it. It delegates rather than
// simulates: the reserve is only meaningful if the REAL ledger's
// arithmetic honours it, so the assertions below run against
// ratelimit.LedgerSolo's actual cost model (a reservation holds
// CostReserved=5, a 2XX settles at Cost2XX=2), not a stand-in.
type recordingLedger struct {
	inner ratelimit.Ledger

	mu       sync.Mutex
	requests []ratelimit.AcquireRequest
}

func (r *recordingLedger) Acquire(ctx context.Context, req ratelimit.AcquireRequest) (*ratelimit.Reservation, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return r.inner.Acquire(ctx, req)
}

func (r *recordingLedger) Settle(ctx context.Context, res *ratelimit.Reservation, cost int16, respondedAt time.Time) error {
	return r.inner.Settle(ctx, res, cost, respondedAt)
}

func (r *recordingLedger) Reconcile(ctx context.Context, group, userKey string, maxTokens, serverRemaining int) error {
	return r.inner.Reconcile(ctx, group, userKey, maxTokens, serverRemaining)
}

// maxAdmissionRequested is the largest ceiling any recorded call was
// actually ADMITTED against — AdmissionMaxTokens when the caller set one,
// MaxTokens otherwise, mirroring AcquireRequest.admissionCeiling.
func (r *recordingLedger) maxAdmissionRequested() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := 0
	for _, req := range r.requests {
		ceiling := req.MaxTokens
		if req.AdmissionMaxTokens > 0 {
			ceiling = req.AdmissionMaxTokens
		}
		if ceiling > max {
			max = ceiling
		}
	}
	return max
}

// maxStoredRequested is the largest ceiling any recorded call asked to have
// PERSISTED as the bucket's max_tokens. Phase 20.3: this must always be the
// route's real ceiling, never a reduced one, on every call including the
// background poller's — the reserve lives in the admission field.
func (r *recordingLedger) maxStoredRequested() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := 0
	for _, req := range r.requests {
		if req.MaxTokens > max {
			max = req.MaxTokens
		}
	}
	return max
}

// TestCharNotificationPollingHoldsFiveTokenReserve is Phase 14's named
// exit criterion for the tightest bucket in the entire ESI spec:
// char-notification, 15 tokens per 15 minutes.
//
// The criterion has two halves and this test asserts both:
//
//   - the background poll path never ASKS for more than 10 tokens, so it
//     structurally cannot spend the last 5 (see worker/reserve.go for why
//     the reserve is a call-site policy and needs no change to
//     internal/esi/ratelimit);
//   - an interactive caller against the SAME bucket, asking for the real
//     15, still gets budget at the exact point where the background poller
//     has run itself out.
//
// Everything is driven through the real internal/esi.Client and the real
// ratelimit.LedgerSolo, so a change to the cost model or to the acquire
// arithmetic breaks this test rather than sliding past it.
func TestCharNotificationPollingHoldsFiveTokenReserve(t *testing.T) {
	const (
		userKey   = "hangar:90000001"
		routeMax  = int32(15) // app.esi_route.rate_limit_max, from the spec
		routePath = "/characters/{character_id}/notifications"
	)
	window := 15 * time.Minute

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	ledger := &recordingLedger{inner: ratelimit.NewLedgerSolo(nil)}
	client := &esi.Client{
		HTTPClient: server.Client(),
		BaseURL:    server.URL,
		Ledger:     ledger,
	}

	// The background poll request, built exactly the way CharacterWorker's
	// doSync builds it — the route's REAL ceiling on RateLimitMax, and the
	// reserve on RateLimitAdmissionMax.
	pollRequest := esi.Request{
		Method: http.MethodGet, UpstreamPath: routePath,
		PathParams:            map[string]string{"character_id": "90000001"},
		RateLimitGroup:        CharNotificationGroup,
		RateLimitMax:          RouteRateLimitMax(routeMax),
		RateLimitAdmissionMax: BackgroundRateLimitMax(CharNotificationGroup, routeMax),
		RateLimitWindow:       window,
		UserKey:               userKey,
	}
	require.Equal(t, 15, pollRequest.RateLimitMax,
		"the STORED ceiling must be the route's real 15 even for the background poller — Phase 20.3")
	require.Equal(t, 10, pollRequest.RateLimitAdmissionMax,
		"the background poller must be ADMITTED against the route's 15 tokens minus the permanent 5-token reserve")

	ctx := context.Background()

	// Poll until the ledger refuses. With max=10 and a 2XX costing 2, the
	// poller exhausts its own reduced ceiling after five successful calls.
	// The loop bound is generous; the assertion is that it stops, not when.
	polls := 0
	for i := 0; i < 50; i++ {
		if _, err := client.Do(ctx, pollRequest); err != nil {
			require.ErrorIs(t, err, ratelimit.ErrRateLimited,
				"the only expected failure is the ledger refusing more budget")
			break
		}
		polls++
	}
	require.Greater(t, polls, 0, "the poller must be able to make progress at all")
	require.Less(t, polls, 50, "the poller must eventually be refused — it must not be able to poll forever")

	// Half one: the poll path was never ADMITTED against more than the
	// reduced max, on any call...
	require.LessOrEqual(t, ledger.maxAdmissionRequested(), 10,
		"no background acquire may ever be admitted against more than (15 - 5) tokens")

	// ...and never asked for anything but the REAL ceiling to be stored.
	// This is the Phase 20.3 half: the reserve is a per-call admission
	// policy, and a bucket row shared by every caller must not carry it.
	// Storing 10 is what made esi_ledger_divergence read a permanent 5 on
	// char-notification against Gate 1.3's tolerance of 1.
	require.Equal(t, 15, ledger.maxStoredRequested(),
		"the background poller must persist the route's REAL ceiling, never its own reduced one")

	// Half two: at the very moment the background poller is out of budget,
	// an interactive caller applying no reduction still gets in — the
	// reserved headroom is real, not notional.
	interactive, err := ledger.Acquire(ctx, ratelimit.AcquireRequest{
		Group: CharNotificationGroup, UserKey: userKey,
		MaxTokens: int(routeMax), Window: window, RequestTimeout: 30 * time.Second,
	})
	require.NoError(t, err,
		"an interactive refresh must still acquire against the real 15-token ceiling after the background poller has exhausted its own 10")
	require.NotNil(t, interactive)

	// And the background poller is still refused at that same instant —
	// otherwise the previous assertion would prove nothing about a reserve
	// (it could just be that budget had freed up).
	_, err = ledger.Acquire(ctx, ratelimit.AcquireRequest{
		Group: CharNotificationGroup, UserKey: userKey,
		MaxTokens:          pollRequest.RateLimitMax,
		AdmissionMaxTokens: pollRequest.RateLimitAdmissionMax,
		Window:             window, RequestTimeout: 30 * time.Second,
	})
	require.ErrorIs(t, err, ratelimit.ErrRateLimited,
		"the background poller must remain refused while the interactive caller succeeds — that gap IS the reserve")
}

// TestCharNotificationReserveMatchesIngestedSpec proves the numbers above
// are not magic: 15 tokens / 15 minutes and a 600-second cache age are
// what the live ingested spec declares for this route, which is also why
// §4.4's "poll at 600s" needs no new scheduling code — the existing
// cache-age-driven planner already produces that cadence.
func TestCharNotificationReserveMatchesIngestedSpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "esi", "catalogue", "embedded", "openapi.snapshot.json"))
	require.NoError(t, err)

	var spec struct {
		Paths map[string]map[string]struct {
			CacheAge  int `json:"x-cache-age"`
			RateLimit struct {
				Group      string `json:"group"`
				MaxTokens  int    `json:"max-tokens"`
				WindowSize string `json:"window-size"`
			} `json:"x-rate-limit"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &spec))

	op, ok := spec.Paths["/characters/{character_id}/notifications"]["get"]
	require.True(t, ok, "the notifications route must be present in the embedded spec snapshot")

	require.Equal(t, CharNotificationGroup, op.RateLimit.Group)
	require.Equal(t, 15, op.RateLimit.MaxTokens, "§4.4's 15-token bucket")
	require.Equal(t, "15m", op.RateLimit.WindowSize, "§4.4's 15-minute window")
	require.Equal(t, 600, op.CacheAge, "§4.4's 600s poll cadence comes from x-cache-age, not from new code")

	require.Equal(t, op.RateLimit.MaxTokens-CharNotificationReserve,
		BackgroundRateLimitMax(CharNotificationGroup, int32(op.RateLimit.MaxTokens)),
		"the background ceiling must be derived from the spec's own max-tokens, never hardcoded")

	// The reserve applies to this group and to no other. A blanket
	// reduction would quietly shrink every bucket in the catalogue — and
	// zero, not the route's own max, is what "no reduction" now means, so
	// every other group's admission ceiling falls through to the stored
	// ceiling rather than being restated per call.
	require.Equal(t, 0, BackgroundRateLimitMax("char-detail", 600))
	require.Equal(t, 0, BackgroundRateLimitMax("corp-structure", 300))

	// A ceiling at or below the reserve degrades to no reduction rather
	// than to a deadlock. Not reachable with today's spec (15 > 5).
	require.Equal(t, 0, BackgroundRateLimitMax(CharNotificationGroup, 5))
	require.Equal(t, 0, BackgroundRateLimitMax(CharNotificationGroup, 3))

	// Everything that is STORED and everything Reconcile sees must be the
	// REAL ceiling — feeding either the reduced one desyncs the local
	// ledger from ESI's server-reported truth and, before Phase 20.3,
	// showed up as a permanent esi_ledger_divergence of exactly 5.
	require.Equal(t, 15, RouteRateLimitMax(15))
}

// TestNoWorkerBypassesTheBackgroundRateLimitHelper is the regression guard
// that keeps the reserve from being undone — or misapplied — by a future
// edit. Two source-level rules, one per field:
//
//   - every esi.Request the package builds must set RateLimitAdmissionMax
//     from BackgroundRateLimitMax, or a new call site silently spends the
//     five tokens §4.4 reserves for interactive callers;
//   - no esi.Request may set RateLimitMax from BackgroundRateLimitMax.
//     That is the Phase 20.3 defect exactly: the reduced ceiling on the
//     field the ledger PERSISTS, which put a fiction in a row every caller
//     shares and made Gate 1.3 read a structural divergence of 5.
//
// Source-level rather than behavioural because what it guards against is a
// NEW call site somewhere else in the package — no behavioural test can see
// code that has not been written yet — and it is the same shape as the
// project's other invariant guards (`make check-money`'s reflection proof,
// `check-identifiers`).
func TestNoWorkerBypassesTheBackgroundRateLimitHelper(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		require.NoError(t, err)
		src := string(body)

		require.NotContains(t, src, "RateLimitMax:          BackgroundRateLimitMax(",
			"%s stores a REDUCED rate-limit ceiling as esi.Request.RateLimitMax. That field is persisted as "+
				"the ledger bucket's max_tokens and is what Reconcile and esi_ledger_divergence measure "+
				"against; a call-site reserve belongs in RateLimitAdmissionMax (see reserve.go)", name)
		require.NotContains(t, src, "RateLimitMax: BackgroundRateLimitMax(",
			"%s stores a REDUCED rate-limit ceiling as esi.Request.RateLimitMax — see reserve.go", name)

		// Any file that builds a rate-limited request at all must set both
		// halves of the pair. Counting them keeps a future edit from
		// dropping the admission field and quietly losing the reserve.
		stored := strings.Count(src, "RateLimitMax:")
		// "RateLimitMax:" is a substring of "RateLimitAdmissionMax:" only
		// in the reverse direction, so subtract nothing — the field names
		// share no prefix.
		admission := strings.Count(src, "RateLimitAdmissionMax:")
		require.Equal(t, stored, admission,
			"%s sets esi.Request.RateLimitMax %d time(s) but RateLimitAdmissionMax %d time(s) — every "+
				"rate-limited call site must pass BOTH the route's real ceiling and its admission ceiling "+
				"(BackgroundRateLimitMax returns 0, i.e. no reduction, for every group but char-notification)",
			name, stored, admission)
		checked++
	}
	require.Greater(t, checked, 0, "the guard must actually have read some source files")
}
