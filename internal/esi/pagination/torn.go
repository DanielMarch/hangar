package pagination

import (
	"errors"
	"fmt"
)

// ErrTornPageSet is returned when a page-paginated fetch's Last-Modified
// validators disagree across pages: the dataset changed mid-read.
// Torn-set detection is a correctness control, not an optimisation
// (01_ARCHITECTURE.md §5.9) — partially committing a paged wallet journal
// silently loses transactions, so the whole payload must be discarded and
// the fetch retried, never partially committed.
var ErrTornPageSet = errors.New("pagination: torn page set — Last-Modified mismatch across pages")

// detectTornSet returns ErrTornPageSet unless every page of the set agrees
// about Last-Modified: all of them carry it with the same value, or none of
// them carries it at all.
//
// ── PHASE 20.2 (B31): THE STRICTER RULE WON, AND WHY ─────────────────────
// Two implementations of this check existed. This one skipped any page with
// no validator — "some ESI responses omit it, so it contributes nothing to
// the comparison". internal/sync/worker's live one treated a page that
// disagrees about the PRESENCE of the validator as torn. They disagree on
// exactly one input: a set where page 1 carries Last-Modified and page 3
// does not.
//
// The live, stricter reading is the one that ships, and this is now the
// only implementation. §5.9's rule exists because "partially committing a
// paged wallet journal silently loses transactions", and a page that
// suddenly stops carrying a validator is not evidence that the set is
// intact — it is the absence of the evidence the rule is built on. Treating
// missing proof as proof is the wrong direction for a control whose whole
// justification is that the failure is silent. The cost of being wrong is
// one discarded fetch and a retry on the next scheduled attempt; the cost
// of the lenient reading being wrong is missing rows nobody ever notices.
//
// A set where NO page carries a validator is not torn — there is nothing to
// disagree about, and rejecting it would make every validator-less
// paginated route permanently unsyncable.
func detectTornSet(pages []PageResult) error {
	if len(pages) == 0 {
		return nil
	}
	want, wantHas := pages[0].LastModified, pages[0].HasLastModified
	for _, p := range pages[1:] {
		if p.HasLastModified != wantHas {
			return fmt.Errorf("%w: page %d %s a Last-Modified validator and page 1 %s — a page that stops carrying the validator is not evidence the set is intact",
				ErrTornPageSet, p.Page, presence(p.HasLastModified), presence(wantHas))
		}
		if wantHas && !p.LastModified.Equal(want) {
			return fmt.Errorf("%w: page %d has %v, page 1 has %v",
				ErrTornPageSet, p.Page, p.LastModified, want)
		}
	}
	return nil
}

func presence(has bool) string {
	if has {
		return "carries"
	}
	return "does not carry"
}
