package pagination

import (
	"errors"
	"fmt"
	"time"
)

// ErrTornPageSet is returned when a page-paginated fetch's Last-Modified
// validators disagree across pages: the dataset changed mid-read.
// Torn-set detection is a correctness control, not an optimisation
// (01_ARCHITECTURE.md §5.9) — partially committing a paged wallet journal
// silently loses transactions, so the whole payload must be discarded and
// the fetch retried, never partially committed.
var ErrTornPageSet = errors.New("pagination: torn page set — Last-Modified mismatch across pages")

// detectTornSet compares every page's Last-Modified validator (where
// present) against the first one seen and returns ErrTornPageSet on any
// disagreement. A page with no Last-Modified at all is not itself an
// error — some ESI responses omit it — it simply contributes nothing to
// the comparison.
func detectTornSet(pages []PageResult) error {
	var want time.Time
	var have bool
	for _, p := range pages {
		if !p.HasLastModified {
			continue
		}
		if !have {
			want = p.LastModified
			have = true
			continue
		}
		if !p.LastModified.Equal(want) {
			return fmt.Errorf("%w: page %d has %v, page 1 (or the first page carrying a validator) has %v",
				ErrTornPageSet, p.Page, p.LastModified, want)
		}
	}
	return nil
}
