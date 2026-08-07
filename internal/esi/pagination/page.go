package pagination

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
)

// MaxPageConcurrency bounds page-paginated fan-out (01_ARCHITECTURE.md
// §5.9: "Fan-out capped at concurrency 4").
const MaxPageConcurrency = 4

// PageResult is one fetched page of a page-paginated set.
type PageResult struct {
	Page            int
	TotalPages      int // from X-Pages on page 1; 0 (absent) means "treat as one page"
	LastModified    time.Time
	HasLastModified bool
	Body            []byte
}

// PageFetcher fetches one page (1-indexed) of a page-paginated route.
// TotalPages is only meaningful on the page-1 result — X-Pages is only
// guaranteed present there in practice, and every implementation here
// treats it as authoritative only from that first response.
type PageFetcher func(ctx context.Context, page int) (PageResult, error)

// FetchAllPages fetches every page of a page-paginated set, honouring
// MaxPageConcurrency, and validates the whole set is untorn before
// returning. X-Pages absent or <= 1 on page 1 is treated as exactly one
// page — it never loops looking for more (01_ARCHITECTURE.md §5.9 edge
// case). On a torn set (ErrTornPageSet), the caller receives that error
// and nothing else — there is no partial result to inspect, by design:
// the whole point is that a partial paged wallet journal must never be
// mistaken for a complete one.
func FetchAllPages(ctx context.Context, fetch PageFetcher) ([]PageResult, error) {
	first, err := fetch(ctx, 1)
	if err != nil {
		return nil, err
	}
	total := first.TotalPages
	if total <= 1 {
		return []PageResult{first}, nil
	}

	results := make([]PageResult, total)
	results[0] = first

	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, MaxPageConcurrency)
	for page := 2; page <= total; page++ {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()

			r, err := fetch(gctx, page)
			if err != nil {
				return err
			}
			results[page-1] = r
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	if err := detectTornSet(results); err != nil {
		return nil, err
	}
	return results, nil
}
