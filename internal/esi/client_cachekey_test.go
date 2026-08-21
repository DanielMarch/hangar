package esi_test

// ── THE ESI CACHE KEY OMITTED THE COMPATIBILITY DATE (Phase 20.3) ────────
//
// 01_ARCHITECTURE.md §5.3's key formula is
//
//	sha256(method ‖ normalized_path ‖ sorted_query ‖ compatibility_date ‖
//	       tenant ‖ resolved_esi_language ‖ token_subject)
//
// and cache.KeyInput has declared CompatibilityDate since Phase 3.
// esi.Client.cacheKey never populated it, so every cached body was keyed as
// though the pin did not exist and advancing the pin — the one operation a
// pin exists for, because it changes what ESI returns — invalidated not one
// entry. Found by reading §5.3 against the code during Phase 20.2 and
// deferred to 20.3 only because B23 was already invalidating the whole
// cache once in that release.
//
// ── WHAT "INVALIDATED" MEANS HERE, PRECISELY ─────────────────────────────
// HANGAR's cache is NOT a TTL short-circuit: Client.Do always makes the
// request. The cache backs §5.4's CONDITIONAL revalidation — an ETag goes
// out, a 304 comes back, and the stored body is replayed instead of being
// re-downloaded. So the observable consequence of the key is exactly this:
// after the pin advances, a 304 must find NO body to replay, because the
// body it would have replayed was fetched under a different ESI contract.
// These tests assert that, not a request count.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/cache"
)

const cachedETag = `"pin-test-etag"`

// pinServer answers 200 with a body and an ETag when no validator is sent,
// and 304 when the matching ETag comes back — a faithful conditional
// server, which is what makes the replay path reachable at all.
func pinServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == cachedETag {
			w.Header().Set("ETag", cachedETag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", cachedETag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"body":"fetched-under-a-pin"}`))
	}))
}

func pinnedClient(t *testing.T, baseURL string, pin func() (string, error)) (*esi.Client, *cache.L1) {
	t.Helper()
	l1, err := cache.NewL1(1 << 20)
	require.NoError(t, err)
	return &esi.Client{
		HTTPClient:       &http.Client{Timeout: 5 * time.Second},
		BaseURL:          baseURL,
		Cache:            &cache.Store{L1: l1},
		Tenant:           "hangar-test",
		CompatibilityPin: pin,
	}, l1
}

func pinRequest(validators *cache.Validators) esi.Request {
	return esi.Request{
		Method: http.MethodGet, UpstreamPath: "/characters/{character_id}/skills",
		PathParams: map[string]string{"character_id": "1"},
		CacheMode:  "cacheable",
		Validators: validators,
	}
}

// TestAdvancingTheCompatibilityPinInvalidatesTheCache is the defect, stated
// as the behaviour it broke.
func TestAdvancingTheCompatibilityPinInvalidatesTheCache(t *testing.T) {
	srv := pinServer(t)
	defer srv.Close()

	current := &atomic.Pointer[string]{}
	initial := "2026-01-01"
	current.Store(&initial)
	client, l1 := pinnedClient(t, srv.URL, func() (string, error) { return *current.Load(), nil })
	ctx := context.Background()

	// A full fetch under the first pin stores the body.
	first, err := client.Do(ctx, pinRequest(nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, first.StatusCode)
	require.Equal(t, cachedETag, first.ETag)
	l1.Wait() // ristretto's Set is asynchronous

	// A conditional request under the SAME pin gets a 304 and replays the
	// stored body.
	replayed, err := client.Do(ctx, pinRequest(&cache.Validators{ETag: cachedETag}))
	require.NoError(t, err)
	require.True(t, replayed.NotModified)
	require.True(t, replayed.FromCache, "under the same pin the 304 must replay the stored body")
	require.Equal(t, first.Body, replayed.Body)

	// Advance the pin. Every stored body was fetched under the previous ESI
	// contract, and none of them may be replayed under the new one.
	advanced := "2026-06-01"
	current.Store(&advanced)

	afterAdvance, err := client.Do(ctx, pinRequest(&cache.Validators{ETag: cachedETag}))
	require.NoError(t, err)
	require.Equal(t, http.StatusNotModified, afterAdvance.StatusCode)
	require.False(t, afterAdvance.FromCache,
		"advancing the compatibility pin must invalidate every cached body — replaying one fetched "+
			"under the previous pin is exactly what the pin exists to prevent")
	require.Empty(t, afterAdvance.Body)
	// NotModified is deliberately NOT asserted true here. Do's 304 branch
	// sets it only when it actually replays a body; with nothing to replay
	// it falls through to the ordinary path, which returns the bare 304 and
	// leaves the flag false. That is Do's documented fall-through ("the
	// caller's next full sync cycle will re-fetch when the ETag inevitably
	// misses again"), and asserting the opposite here would pin a shape
	// this test has no business fixing.

	// Rolling the pin BACK finds the original entry intact. That is correct
	// rather than a leak: the key is a function of the pin, so a rollback
	// re-uses precisely the bodies that were valid under it.
	current.Store(&initial)
	rolledBack, err := client.Do(ctx, pinRequest(&cache.Validators{ETag: cachedETag}))
	require.NoError(t, err)
	require.True(t, rolledBack.FromCache)
	require.Equal(t, first.Body, rolledBack.Body)
}

// TestAnUnreadablePinDegradesRatherThanFails pins the failure direction.
// app.setting being briefly unreadable must not turn every ESI request into
// an error; it costs cache precision, not availability.
func TestAnUnreadablePinDegradesRatherThanFails(t *testing.T) {
	srv := pinServer(t)
	defer srv.Close()

	client, _ := pinnedClient(t, srv.URL, func() (string, error) { return "", context.DeadlineExceeded })
	resp, err := client.Do(context.Background(), pinRequest(nil))
	require.NoError(t, err, "an unreadable pin must not fail the request")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestNoPinKeysUnderTheEmptyString keeps the zero value working: a Client
// built without a pin source — which is every unit test in this package,
// and every caller written before the field existed — must cache and replay
// exactly as it did before.
func TestNoPinKeysUnderTheEmptyString(t *testing.T) {
	srv := pinServer(t)
	defer srv.Close()

	client, l1 := pinnedClient(t, srv.URL, nil)
	ctx := context.Background()

	first, err := client.Do(ctx, pinRequest(nil))
	require.NoError(t, err)
	l1.Wait()

	replayed, err := client.Do(ctx, pinRequest(&cache.Validators{ETag: cachedETag}))
	require.NoError(t, err)
	require.True(t, replayed.FromCache, "a Client with no pin source must still cache and replay")
	require.Equal(t, first.Body, replayed.Body)
}

// ── PHASE 23 (N-5): THE FAN-OUT CACHE-KEY COLLISION ──────────────────────
//
// cacheKey hashed the TEMPLATED upstream path and never PathParams, so
// every item of a detail fan-out shared one cache key. This test is written
// the way the defect would actually surface: two characters' skills, each
// with its own ETag, and the assertion that a 304 for one never replays the
// other's body.
//
// It fails against the pre-fix key — character 2's conditional request
// finds character 1's body under the shared key and replays it, so the
// caller receives one character's data labelled as another's.
func TestFanOutItemsDoNotShareACacheKey(t *testing.T) {
	// A faithful per-item conditional server: each character has its own
	// body and its own ETag, which is exactly the shape that makes the
	// collision reachable.
	bodies := map[string]string{
		"/characters/1/skills": `{"character":1}`,
		"/characters/2/skills": `{"character":2}`,
	}
	etags := map[string]string{
		"/characters/1/skills": `"etag-character-1"`,
		"/characters/2/skills": `"etag-character-2"`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := etags[r.URL.Path]
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bodies[r.URL.Path]))
	}))
	defer srv.Close()

	client, l1 := pinnedClient(t, srv.URL, nil)
	ctx := context.Background()

	itemRequest := func(id string, validators *cache.Validators) esi.Request {
		return esi.Request{
			Method: http.MethodGet, UpstreamPath: "/characters/{character_id}/skills",
			PathParams: map[string]string{"character_id": id},
			CacheMode:  "cacheable",
			Validators: validators,
		}
	}

	// Fan out over both items, storing each body under its own key.
	first, err := client.Do(ctx, itemRequest("1", nil))
	require.NoError(t, err)
	require.Equal(t, `{"character":1}`, string(first.Body))
	second, err := client.Do(ctx, itemRequest("2", nil))
	require.NoError(t, err)
	require.Equal(t, `{"character":2}`, string(second.Body))
	l1.Wait()

	// Now condition each item on ITS OWN ETag — the per-item revalidation
	// that makes this defect live. Each must replay its own body.
	for _, id := range []string{"1", "2"} {
		replayed, err := client.Do(ctx, itemRequest(id, &cache.Validators{ETag: etags["/characters/"+id+"/skills"]}))
		require.NoError(t, err)
		require.True(t, replayed.FromCache, "character %s's 304 must find character %s's stored body", id, id)
		require.Equal(t, bodies["/characters/"+id+"/skills"], string(replayed.Body),
			"a 304 for character %s replayed another character's body — the fan-out shares one cache key", id)
	}
}

// TestTheCacheKeyDistinguishesEveryPathParam is the narrower statement, and
// the one that would catch a partial fix: it asserts the key changes when
// ANY path parameter changes, not merely when the first one does.
func TestTheCacheKeyDistinguishesEveryPathParam(t *testing.T) {
	srv := pinServer(t)
	defer srv.Close()

	client, l1 := pinnedClient(t, srv.URL, nil)
	ctx := context.Background()

	multi := func(corp, division string) esi.Request {
		return esi.Request{
			Method:       http.MethodGet,
			UpstreamPath: "/corporations/{corporation_id}/wallets/{division}/journal",
			PathParams:   map[string]string{"corporation_id": corp, "division": division},
			CacheMode:    "cacheable",
		}
	}

	// Store a body for one (corporation, division) pair.
	_, err := client.Do(ctx, multi("98000001", "1"))
	require.NoError(t, err)
	l1.Wait()

	// A different DIVISION of the same corporation is a different resource
	// and must not find that entry. Its 304 has nothing to replay.
	other := multi("98000001", "2")
	other.Validators = &cache.Validators{ETag: cachedETag}
	resp, err := client.Do(ctx, other)
	require.NoError(t, err)
	require.False(t, resp.FromCache,
		"division 2 replayed division 1's body — the key ignores a path parameter that is not the first")
}
