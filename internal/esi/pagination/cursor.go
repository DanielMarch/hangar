// Package pagination drives ESI's two x-pagination mechanisms
// (01_ARCHITECTURE.md §5.9): cursor (after/before/limit) and page
// (page + X-Pages) with torn-set detection.
package pagination

import (
	"context"
	"fmt"
	"net/url"
)

// RequestLimit is what HANGAR always requests on a cursor-paginated route.
// The wire contract allows the caller to ask for as few as 10, but there
// is never a reason for this gateway — which always wants the full page —
// to ask for fewer than the maximum.
const RequestLimit = 100

// StartOfSet / EndOfSet are the cursor sentinels: '0' paired with `after`
// means "start of the set"; '0' paired with `before` means "end of the
// set". Cursor values themselves are opaque and never parsed by this
// package beyond recognising this one sentinel.
const (
	StartOfSet = "0"
	EndOfSet   = "0"
)

// Direction selects which of the two mutually-exclusive cursor parameters
// a request supplies.
type Direction int

const (
	After Direction = iota
	Before
)

// CursorQuery builds the after/before/limit query parameters for one
// cursor-paginated request. after and before are mutually exclusive by
// construction here — Direction selects exactly one, so a caller cannot
// accidentally set both the way a hand-built url.Values could.
func CursorQuery(dir Direction, cursor string) url.Values {
	v := url.Values{}
	switch dir {
	case Before:
		v.Set("before", cursor)
	default:
		v.Set("after", cursor)
	}
	v.Set("limit", fmt.Sprintf("%d", RequestLimit))
	return v
}

// CursorPage is one fetched page of a cursor-paginated set.
type CursorPage struct {
	Body       []byte
	NextCursor string // opaque; verbatim from the upstream response
	HasMore    bool
}

// CursorFetcher fetches one page in direction dir starting at cursor.
// Implementations must never parse or synthesise a cursor value — they
// pass through whatever the previous CursorPage.NextCursor was, verbatim.
type CursorFetcher func(ctx context.Context, dir Direction, cursor string) (CursorPage, error)

// FetchAllCursor drives fetch from the start of the set (After, "0") until
// HasMore is false, returning every page's body in order. It never
// synthesises or inspects a cursor value beyond passing it straight back
// into the next call.
func FetchAllCursor(ctx context.Context, fetch CursorFetcher) ([][]byte, error) {
	var bodies [][]byte
	cursor := StartOfSet
	for {
		page, err := fetch(ctx, After, cursor)
		if err != nil {
			return nil, err
		}
		bodies = append(bodies, page.Body)
		if !page.HasMore {
			return bodies, nil
		}
		cursor = page.NextCursor
	}
}
