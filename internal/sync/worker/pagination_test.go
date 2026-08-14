package worker

// PHASE 20.2 (B31). These exercise the worker's side of the single
// page-walker and the newly implemented cursor walker against a real
// http.Client and a real internal/esi.Client — the walk assembles a body
// that a route handler will parse, so a test that stubbed the gateway would
// prove nothing about the shape that actually comes out.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/pagination"
)

func gateway(base string) *esi.Client {
	return &esi.Client{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		BaseURL:    base,
		TTLFloor:   300 * time.Second,
		Tenant:     "test",
	}
}

func pagedRequest(path string) esi.Request {
	return esi.Request{Method: http.MethodGet, UpstreamPath: path}
}

// TestFetchAllPagesConcatenatesEveryPage covers the ordinary walk: X-Pages
// on page 1, every page a JSON array, one assembled array out.
func TestFetchAllPagesConcatenatesEveryPage(t *testing.T) {
	t.Parallel()

	lastMod := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		w.Header().Set("X-Pages", "3")
		w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `[{"page":%d}]`, page)
	}))
	defer srv.Close()

	resp, err := fetchAllPages(context.Background(), gateway(srv.URL), pagedRequest("/paged"))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var rows []map[string]int
	require.NoError(t, json.Unmarshal(resp.Body, &rows))
	require.Equal(t, []map[string]int{{"page": 1}, {"page": 2}, {"page": 3}}, rows,
		"every page's elements must appear, in page order")
}

// TestFetchAllPagesFansOutAtFour pins §5.9's "Fan-out capped at
// concurrency 4" — the specified behaviour the DEAD implementation had and
// the live serial one did not, resolved in the spec's favour (see
// pagination.go's header).
//
// The assertion is on the observed peak concurrency, not on wall-clock
// time, because a timing assertion on a machine under test load is a flake
// waiting to happen.
func TestFetchAllPagesFansOutAtFour(t *testing.T) {
	t.Parallel()

	lastMod := time.Now().UTC().Truncate(time.Second)
	var inFlight, peak int64
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page > 1 {
			n := atomic.AddInt64(&inFlight, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
					break
				}
			}
			<-release
			atomic.AddInt64(&inFlight, -1)
		}
		w.Header().Set("X-Pages", "9")
		w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `[{"page":%d}]`, page)
	}))
	defer srv.Close()

	done := make(chan error, 1)
	go func() {
		_, err := fetchAllPages(context.Background(), gateway(srv.URL), pagedRequest("/paged"))
		done <- err
	}()

	// Let the fan-out saturate, then let everything through.
	require.Eventually(t, func() bool { return atomic.LoadInt64(&inFlight) >= pagination.MaxPageConcurrency },
		5*time.Second, 5*time.Millisecond, "the walker must fan out, not walk serially")
	close(release)
	require.NoError(t, <-done)

	require.LessOrEqual(t, atomic.LoadInt64(&peak), int64(pagination.MaxPageConcurrency),
		"§5.9 caps fan-out at %d; more in flight is a rate-limit hazard, not a speed-up", pagination.MaxPageConcurrency)
}

// TestFetchAllPagesDiscardsATornSet is §5.9's correctness control seen from
// the worker: the whole payload is discarded, and the caller gets an error
// naming the retry, never a partial body.
func TestFetchAllPagesDiscardsATornSet(t *testing.T) {
	t.Parallel()

	first := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		lm := first
		if page == 2 {
			lm = first.Add(time.Minute) // the dataset changed mid-read
		}
		w.Header().Set("X-Pages", "2")
		w.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `[{"page":%d}]`, page)
	}))
	defer srv.Close()

	resp, err := fetchAllPages(context.Background(), gateway(srv.URL), pagedRequest("/paged"))
	require.Error(t, err)
	require.ErrorIs(t, err, pagination.ErrTornPageSet)
	require.Nil(t, resp, "a torn set must yield NO body — a partial page set must never reach a handler")
}

// TestFetchAllPagesPassesANonOKFirstPageStraightBack: a 304, 403 or 429 on
// page 1 is the caller's ordinary response to handle, not a pagination
// failure. This is what the nonOKFirstPage sentinel exists for.
func TestFetchAllPagesPassesANonOKFirstPageStraightBack(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	resp, err := fetchAllPages(context.Background(), gateway(srv.URL), pagedRequest("/paged"))
	require.NoError(t, err, "a 403 is a response, not a walk failure")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ── the cursor walker (§5.9's second mechanism) ──────────────────────────

// TestFetchAllCursorPagesFollowsTheCursor is the defect B31 answered: GET
// /corporations/{id}/projects returns {cursor, projects}, Phase 20.1.1
// captured the cursor and did not follow it, and a corporation with more
// than one page of projects synced only the first.
//
// The assembled body must be the SAME envelope shape a single-page response
// has, so handlers.ParseCorporationProjects needs no knowledge that a walk
// happened.
func TestFetchAllCursorPagesFollowsTheCursor(t *testing.T) {
	t.Parallel()

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after")
		seen = append(seen, after)
		require.Equal(t, "100", r.URL.Query().Get("limit"),
			"§5.9: HANGAR always requests the maximum page size")
		require.Empty(t, r.URL.Query().Get("before"),
			"after and before are mutually exclusive — supplying both is a client error")

		w.Header().Set("Content-Type", "application/json")
		switch after {
		case "0":
			_, _ = fmt.Fprint(w, `{"cursor":{"after":"CUR2"},"projects":[{"name":"a"}]}`)
		case "CUR2":
			_, _ = fmt.Fprint(w, `{"cursor":{"after":"CUR3"},"projects":[{"name":"b"}]}`)
		default:
			_, _ = fmt.Fprint(w, `{"projects":[{"name":"c"}]}`)
		}
	}))
	defer srv.Close()

	resp, err := fetchAllCursorPages(context.Background(), gateway(srv.URL), pagedRequest("/projects"), "projects")
	require.NoError(t, err)
	require.Equal(t, []string{"0", "CUR2", "CUR3"}, seen,
		"the walk starts at the '0' sentinel and echoes each cursor back verbatim, never synthesising one")

	var envelope struct {
		Cursor   *struct{ After *string } `json:"cursor"`
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &envelope))
	require.Len(t, envelope.Projects, 3, "every page's projects must survive the merge")
	require.Equal(t, "a", envelope.Projects[0].Name)
	require.Equal(t, "c", envelope.Projects[2].Name)
	require.Nil(t, envelope.Cursor,
		"the merged envelope must carry no cursor: the walk is complete, and leaving one would say otherwise")
}

// TestFetchAllCursorPagesStopsOnARepeatedCursor guards the one way a cursor
// walk can hang: a server that keeps handing back the cursor it was given.
func TestFetchAllCursorPagesStopsOnARepeatedCursor(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		after := r.URL.Query().Get("after")
		_, _ = fmt.Fprintf(w, `{"cursor":{"after":%q},"projects":[]}`, after)
	}))
	defer srv.Close()

	_, err := fetchAllCursorPages(context.Background(), gateway(srv.URL), pagedRequest("/projects"), "projects")
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "a cursor that repeats itself must end the walk, not loop")
}

// TestFetchAllCursorPagesPassesANonOKFirstPageBack mirrors the page
// walker's behaviour: the caller's switch handles 304/403/429.
func TestFetchAllCursorPagesPassesANonOKFirstPageBack(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	resp, err := fetchAllCursorPages(context.Background(), gateway(srv.URL), pagedRequest("/projects"), "projects")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotModified, resp.StatusCode)
}
