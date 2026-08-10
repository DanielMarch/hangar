package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/stretchr/testify/require"
)

// TestWalletPagePaginationAndTornDetection (roadmap exit criterion): a
// full page walk assembles every page into one payload, and a mismatched
// Last-Modified between pages discards the whole set and returns an error
// rather than committing a partial (torn) result — 01_ARCHITECTURE.md
// §5.9's page-pagination rule, exercised through fetchAllPages exactly as
// CorporationWorker.doSync calls it.
func TestWalletPagePaginationAndTornDetection(t *testing.T) {
	t.Run("full page walk assembles every page", func(t *testing.T) {
		const totalPages = 3
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == 0 {
				page = 1
			}
			w.Header().Set("X-Pages", strconv.Itoa(totalPages))
			w.Header().Set("Last-Modified", "Tue, 06 Aug 2026 12:00:00 GMT")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":` + strconv.Itoa(page) + `,"ref_type":"bounty_prize","description":"x","date":"2026-08-01T00:00:00Z"}]`))
		}))
		defer srv.Close()

		client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
		resp, err := fetchAllPages(context.Background(), client, esi.Request{
			Method: http.MethodGet, UpstreamPath: "/corporations/{corporation_id}/wallets/{division}/journal",
			PathParams: map[string]string{"corporation_id": "98000001", "division": "1"},
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		entries, err := handlers.ParseWalletJournalPage(resp.Body)
		require.NoError(t, err)
		require.Len(t, entries, totalPages, "every page's element must appear in the assembled body")

		seen := map[int64]bool{}
		for _, e := range entries {
			seen[e.ID] = true
		}
		for i := int64(1); i <= totalPages; i++ {
			require.Truef(t, seen[i], "page %d's entry is missing from the assembled set", i)
		}
	})

	t.Run("torn set (Last-Modified mismatch mid-walk) is discarded, never partially committed", func(t *testing.T) {
		const totalPages = 2
		var callCount int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			w.Header().Set("X-Pages", strconv.Itoa(totalPages))
			if page == 2 {
				// The dataset changed mid-read — a different Last-Modified.
				w.Header().Set("Last-Modified", "Tue, 06 Aug 2026 13:00:00 GMT")
			} else {
				w.Header().Set("Last-Modified", "Tue, 06 Aug 2026 12:00:00 GMT")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":` + strconv.Itoa(page) + `,"ref_type":"bounty_prize","description":"x","date":"2026-08-01T00:00:00Z"}]`))
		}))
		defer srv.Close()

		client := &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
		resp, err := fetchAllPages(context.Background(), client, esi.Request{
			Method: http.MethodGet, UpstreamPath: "/corporations/{corporation_id}/wallets/{division}/journal",
			PathParams: map[string]string{"corporation_id": "98000001", "division": "1"},
		})
		require.Error(t, err, "a Last-Modified mismatch between pages must fail the whole fetch, never return a partial result")
		require.Nil(t, resp)
		require.Equal(t, totalPages, callCount, "both pages must have been fetched before the mismatch was caught")
	})
}
