//go:build integration

package cache_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/cache"
)

// ── DEFECT B34: THE OPTIONAL REDIS L2 TIER ───────────────────────────────
//
// The gate is NOT "Redis works". It is 01_ARCHITECTURE.md §5.4 and
// Principle 7 together: HANGAR must be fully correct when the tier is
// ABSENT, when it is PRESENT AND COLD, when it is PRESENT AND WARM, and
// when it DIES MID-SUITE — and no caller may be able to tell which, from
// the data it acts on.
//
// ── WHAT "IDENTICAL RESULTS" MEANS HERE, MEASURED ────────────────────────
// Writing this test established something worth stating plainly, because it
// is not what the phrase suggests at first reading: HANGAR's L2 tier does
// not save REQUESTS. esi.Client.Do always makes the conditional call — the
// validators come from app.sync_subscription, not from the cache — and the
// cache is consulted in exactly one place, to replay a stored BODY when the
// server answers 304 (client.go's `status == 304 && condSent` branch).
//
// So the identical things are: the status sequence, every 200's bytes, and
// the fact that nothing ever errors. The one permitted difference is whether
// a 304 carries a replayed body — and no caller reads it, because
// internal/sync/worker's 304 branch never invokes a handler. Client.Do's own
// comment says so: "fall through and return the 304 with an empty body; the
// caller's next full sync cycle will re-fetch."
//
// That is why a Redis outage is a MISS and not a fall-through to Postgres,
// and why the choice costs nothing: the tier was never on the correctness
// path to begin with.

// observation is what a caller saw, per request.
type observation struct {
	status       int
	body         string
	replayedBody bool // a 304 that carried bytes — the only tier-visible effect
}

// runScenario drives five requests through a gateway whose only difference
// between runs is the L2 tier it was given.
//
//  1. /cached           — cold, expect 200 and a body
//  2. /cached           — L1 is warm, conditional, expect 304 + L1 replay
//  3. /nostore          — must never be written to any tier
//  4. /cached, fresh L1 — ONLY an L2 can replay here
//  5. /nostore, again   — still a full 200, never a replay
func runScenario(t *testing.T, upstreamURL string, hits *int64, l2 cache.L2) ([]observation, int64) {
	t.Helper()
	start := atomic.LoadInt64(hits)

	newClient := func() (*esi.Client, func()) {
		l1, err := cache.NewL1(1 << 20)
		require.NoError(t, err)
		return &esi.Client{
			HTTPClient: http.DefaultClient, BaseURL: upstreamURL,
			Cache: &cache.Store{L1: l1, L2: l2}, Tenant: "hangar",
		}, l1.Close
	}

	client, closeL1 := newClient()
	defer closeL1()
	ctx := context.Background()
	var seen []observation

	// validators mimics what app.sync_subscription stores between polls: the
	// ETag the last successful fetch returned. It is deliberately held HERE,
	// outside the cache, because that is where production holds it — the
	// whole point being that conditional-request state does not live in an
	// optional tier.
	var etag string
	do := func(path, mode string) {
		var v *cache.Validators
		if etag != "" && path == "/cached" {
			v = &cache.Validators{ETag: etag}
		}
		resp, err := client.Do(ctx, esi.Request{
			Method: http.MethodGet, UpstreamPath: path, CacheMode: mode, Validators: v,
		})
		require.NoError(t, err, "a request must never fail because of a cache tier")
		if resp.StatusCode == http.StatusOK && path == "/cached" {
			etag = resp.ETag
		}
		seen = append(seen, observation{
			status: resp.StatusCode, body: string(resp.Body),
			replayedBody: resp.StatusCode == http.StatusNotModified && len(resp.Body) > 0,
		})
	}

	do("/cached", "ttl-based")
	do("/cached", "ttl-based")
	do("/nostore", "not-cached")

	closeL1()
	client, closeFresh := newClient()
	defer closeFresh()
	do("/cached", "ttl-based")
	do("/nostore", "not-cached")

	return seen, atomic.LoadInt64(hits) - start
}

func upstreamStub(t *testing.T) (string, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		const tag = `"v1"`
		w.Header().Set("ETag", tag)
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func startRedis(t *testing.T) (*redis.Client, testcontainers.Container) {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:8-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr:         host + ":" + port.Port(),
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	require.Eventually(t, func() bool { return client.Ping(ctx).Err() == nil }, 30*time.Second, 250*time.Millisecond)
	return client, container
}

// statusesAndPayloads reduces a run to the two things that must be
// identical: what the server said, and what bytes a caller was given to ACT
// on. A 304's replayed body is deliberately excluded — no caller reads it.
func statusesAndPayloads(seen []observation) []string {
	out := make([]string, len(seen))
	for i, o := range seen {
		payload := o.body
		if o.status == http.StatusNotModified {
			payload = "<304: no caller reads this>"
		}
		out[i] = fmt.Sprintf("%d %s", o.status, payload)
	}
	return out
}

func replays(seen []observation) int {
	n := 0
	for _, o := range seen {
		if o.replayedBody {
			n++
		}
	}
	return n
}

// TestSameResultsWithTheTierAbsentColdWarmAndDead is the whole gate in one
// test: four configurations, one expected observable outcome.
func TestSameResultsWithTheTierAbsentColdWarmAndDead(t *testing.T) {
	upstream, hits := upstreamStub(t)
	redisClient, container := startRedis(t)
	l2 := cache.NewRedisL2(redisClient, "hangar:esi:", nil)

	// 1. ABSENT — Principle 7's default deployment. No L2 at all.
	absent, absentUpstream := runScenario(t, upstream, hits, nil)

	// 2. ENABLED BUT EMPTY — the `cache` profile has just come up. Reachable
	//    and holding nothing, which is a different state from absent and is
	//    the one every cold start actually has.
	require.NoError(t, redisClient.FlushAll(context.Background()).Err())
	empty, err := redisClient.Keys(context.Background(), "hangar:esi:*").Result()
	require.NoError(t, err)
	require.Empty(t, empty, "the enabled-but-empty configuration must actually start empty")
	cold, coldUpstream := runScenario(t, upstream, hits, l2)

	// 3. ENABLED AND WARM — the previous run left entries behind.
	warm, warmUpstream := runScenario(t, upstream, hits, l2)

	// 4. DEAD MID-SUITE — killed with the tier still configured and still
	//    holding entries. Every operation must degrade to a miss.
	require.NoError(t, container.Terminate(context.Background()))
	dead, deadUpstream := runScenario(t, upstream, hits, l2)

	want := statusesAndPayloads(absent)
	for name, got := range map[string][]observation{"cold": cold, "warm": warm, "dead": dead} {
		require.Equalf(t, want, statusesAndPayloads(got),
			"%s: a caller was served something different than with the tier absent — the L2 tier is not allowed to be authoritative", name)
	}

	// The upstream call count is identical too, and that is not an accident:
	// HANGAR always makes the conditional request. A tier that changed this
	// number would be short-circuiting a revalidation, which §5.4 forbids.
	require.Equal(t, absentUpstream, coldUpstream)
	require.Equal(t, absentUpstream, warmUpstream)
	require.Equal(t, absentUpstream, deadUpstream)

	// The ONE permitted difference, asserted so an inert tier fails.
	//
	// COLD AND WARM AGREE, and that is the finding rather than a slack
	// assertion: a tier that starts empty fills itself on request 1 and is
	// warm by request 4, so "enabled-but-empty" is a property of the START of
	// a run and not of the run. That the two runs are indistinguishable by
	// the end is exactly the guarantee Principle 7 asks for — a cold tier
	// costs one replay, once, and then behaves like a warm one.
	require.Equal(t, 1, replays(absent), "only the L1 replay is possible with no L2")
	require.Equal(t, 1, replays(dead), "an unreachable tier must miss, not replay, and not fail")
	require.Equal(t, 2, replays(cold), "a tier that starts empty must fill itself and replay request 4")
	require.Equal(t, 2, replays(warm), "a warm tier that never replays anything is an inert tier — B34 itself")
}

// TestTheNoStoreContractHoldsOnRedisToo — a not-cached route writes to
// neither tier. Asserted against Redis directly rather than through Store,
// because "Store.Get misses" is also true of a tier that DID write the entry
// and is merely being asked not to read it, and those are different bugs.
func TestTheNoStoreContractHoldsOnRedisToo(t *testing.T) {
	upstream, hits := upstreamStub(t)
	redisClient, container := startRedis(t)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	require.NoError(t, redisClient.FlushAll(context.Background()).Err())
	runScenario(t, upstream, hits, cache.NewRedisL2(redisClient, "hangar:esi:", nil))

	keys, err := redisClient.Keys(context.Background(), "hangar:esi:*").Result()
	require.NoError(t, err)
	require.Len(t, keys, 1,
		"exactly one key expected (the ttl-based route); a second means the not-cached route was stored, and §5.4's no-store contract is L2-tier-agnostic")
}
