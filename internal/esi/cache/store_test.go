package cache

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	l1, err := NewL1(1 << 20) // 1MB budget, plenty for these tests
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l1.Close)
	return &Store{L1: l1} // no L2 — these tests only need L1 + the no-store gate
}

// TestNoCacheRouteWritesNothingAndSendsNoValidators asserts all three
// halves of the not-cached/no-cache contract (Phase 3 exit criterion,
// 01_ARCHITECTURE.md §5.4): no L1 write, no L2 write, and no conditional
// headers sent — not just the first.
func TestNoCacheRouteWritesNothingAndSendsNoValidators(t *testing.T) {
	const mode = "not-cached"
	s := newTestStore(t)
	l2 := &countingL2{}
	s.L2 = l2
	ctx := context.Background()

	entry := Entry{ETag: `"abc"`, Body: []byte("hello"), Status: 200, ExpiresAt: time.Now().Add(time.Hour)}
	s.Set(ctx, "key1", entry, mode)
	s.L1.Wait()

	// 1. No L1 write.
	if _, ok := s.L1.Get("key1"); ok {
		t.Error("no-store mode must not write to L1")
	}
	// 2. No L2 write.
	if l2.sets != 0 {
		t.Errorf("no-store mode must not write to L2, got %d Set call(s)", l2.sets)
	}

	// Get must also miss unconditionally, even if something were
	// (incorrectly) sitting in L1/L2 already.
	s.L1.Set("key1", entry)
	s.L1.Wait()
	if _, ok := s.Get(ctx, "key1", mode); ok {
		t.Error("no-store mode must always miss on Get, even if L1 happens to hold a stale entry")
	}

	// 3. No conditional headers sent.
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	v := &Validators{ETag: `"abc"`, LastModified: time.Now(), HasLastModified: true}
	if sent := ApplyConditionalHeaders(req, v, mode); sent {
		t.Error("no-store mode must not send conditional headers")
	}
	if req.Header.Get("If-None-Match") != "" || req.Header.Get("If-Modified-Since") != "" {
		t.Error("no-store mode must not set If-None-Match or If-Modified-Since")
	}
}

// TestConditionalRequestYields304 — a stored ETag produces If-None-Match
// on the next request, and a 304 response serves from cache (Phase 3 exit
// criterion, 01_ARCHITECTURE.md §5.4).
func TestConditionalRequestYields304(t *testing.T) {
	const mode = "ttl-based"
	const etag = `"v1"`
	const body = `{"hello":"world"}`

	var gotIfNoneMatch string
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		if gotIfNoneMatch == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	s := newTestStore(t)
	ctx := context.Background()
	key := "conditional-test-key"

	fetch := func() (Entry, bool /* servedFromCache */) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		var validators *Validators
		if e, ok := s.Get(ctx, key, mode); ok {
			validators = &Validators{ETag: e.ETag}
		}
		ApplyConditionalHeaders(req, validators, mode)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode == http.StatusNotModified {
			cached, ok := s.Get(ctx, key, mode)
			if !ok {
				t.Fatal("304 received but nothing was cached to serve")
			}
			return cached, true
		}

		respBody, _ := io.ReadAll(resp.Body)
		e := Entry{ETag: resp.Header.Get("ETag"), Body: respBody, Status: resp.StatusCode, ExpiresAt: time.Now().Add(time.Hour)}
		s.Set(ctx, key, e, mode)
		s.L1.Wait()
		return e, false
	}

	first, servedFromCache := fetch()
	if servedFromCache {
		t.Fatal("first request must not be served from cache — nothing was stored yet")
	}
	if string(first.Body) != body {
		t.Fatalf("first fetch body = %q, want %q", first.Body, body)
	}
	if requests != 1 {
		t.Fatalf("expected 1 upstream request so far, got %d", requests)
	}

	second, servedFromCache := fetch()
	if gotIfNoneMatch != etag {
		t.Errorf("second request must send If-None-Match: %s, got %q", etag, gotIfNoneMatch)
	}
	if !servedFromCache {
		t.Error("a 304 response must be served from cache")
	}
	if string(second.Body) != body {
		t.Errorf("cache-served body = %q, want %q (the original 200 body, not empty)", second.Body, body)
	}
	if requests != 2 {
		t.Fatalf("expected 2 upstream requests total (200 then 304), got %d", requests)
	}
}

// countingL2 is a minimal L2 fake that only counts Set calls, for
// TestNoCacheRouteWritesNothingAndSendsNoValidators.
type countingL2 struct {
	sets int
}

func (c *countingL2) Get(context.Context, string) (Entry, bool) { return Entry{}, false }
func (c *countingL2) Set(context.Context, string, Entry)        { c.sets++ }
