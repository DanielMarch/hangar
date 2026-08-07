package pagination

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestTornPaginationDiscardsPayload — a mismatched Last-Modified across
// pages discards the whole payload and signals retry; nothing is
// committed (Phase 3 exit criterion, 01_ARCHITECTURE.md §5.9).
func TestTornPaginationDiscardsPayload(t *testing.T) {
	lm1 := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	lm2 := lm1.Add(time.Minute) // the dataset changed mid-read

	fetch := func(ctx context.Context, page int) (PageResult, error) {
		lm := lm1
		if page == 3 {
			lm = lm2 // page 3 disagrees with pages 1-2
		}
		return PageResult{
			Page: page, TotalPages: 4,
			LastModified: lm, HasLastModified: true,
			Body: []byte{byte(page)},
		}, nil
	}

	_, err := FetchAllPages(context.Background(), fetch)
	if err == nil {
		t.Fatal("expected ErrTornPageSet, got nil")
	}
	if !errors.Is(err, ErrTornPageSet) {
		t.Fatalf("expected ErrTornPageSet, got %v", err)
	}
}

// TestFetchAllPagesHappyPath — a consistent Last-Modified across every
// page succeeds and returns every page in order.
func TestFetchAllPagesHappyPath(t *testing.T) {
	lm := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	fetch := func(ctx context.Context, page int) (PageResult, error) {
		return PageResult{
			Page: page, TotalPages: 5,
			LastModified: lm, HasLastModified: true,
			Body: []byte{byte(page)},
		}, nil
	}

	results, err := FetchAllPages(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 pages, got %d", len(results))
	}
	for i, r := range results {
		if r.Page != i+1 {
			t.Errorf("results[%d].Page = %d, want %d", i, r.Page, i+1)
		}
	}
}

// TestFetchAllPagesXPagesAbsentIsOnePage — X-Pages absent (TotalPages == 0)
// on page 1 is treated as exactly one page, never an infinite loop
// (01_ARCHITECTURE.md §5.9 edge case).
func TestFetchAllPagesXPagesAbsentIsOnePage(t *testing.T) {
	calls := 0
	fetch := func(ctx context.Context, page int) (PageResult, error) {
		calls++
		return PageResult{Page: page, TotalPages: 0, Body: []byte("only page")}, nil
	}

	results, err := FetchAllPages(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 page when X-Pages is absent, got %d", len(results))
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 fetch call, got %d — an absent X-Pages must never cause looping", calls)
	}
}

// TestFetchAllPagesConcurrencyBounded proves the fan-out never exceeds
// MaxPageConcurrency in-flight fetches at once.
func TestFetchAllPagesConcurrencyBounded(t *testing.T) {
	var inFlight, maxSeen atomic.Int32
	fetch := func(ctx context.Context, page int) (PageResult, error) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxSeen.Load()
			if cur <= m || maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		return PageResult{Page: page, TotalPages: 20}, nil
	}
	if _, err := FetchAllPages(context.Background(), fetch); err != nil {
		t.Fatal(err)
	}
	if maxSeen.Load() > MaxPageConcurrency {
		t.Errorf("observed %d concurrent fetches, want <= %d", maxSeen.Load(), MaxPageConcurrency)
	}
}
