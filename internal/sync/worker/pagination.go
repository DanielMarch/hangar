package worker

// ── PHASE 20.2, DEFECT B31: ONE PAGE-WALKER ──────────────────────────────
//
// Two implementations of §5.9's page mechanism existed. internal/esi/
// pagination was fully built and fully tested and NOT IMPORTED BY THE
// BINARY AT ALL; worker/corporation.go had its own serial copy, which is
// the one that actually ran. They disagreed on two points, and both
// disagreements are settled here rather than by preferring whichever was
// easier to keep:
//
//  1. CONCURRENCY. The dead one fanned out at 4, which is what §5.9
//     specifies ("Fan-out capped at concurrency 4"); the live one was
//     serial. The SPEC WINS and the fan-out ships. It is a cap, not a
//     quota, and it cannot cause a rate-limit breach: every page goes
//     through internal/esi.Client.Do, so every page takes a Governor 1
//     reservation, and a walk that runs out of budget is refused with a
//     RetryAtError and snoozed (see unavailable.go) rather than admitted.
//     The serial walker was never safer, only slower — a 20-page corporate
//     asset list took twenty sequential round trips to ESI.
//
//  2. THE TORN-SET CHECK. The dead one ignored a page carrying no
//     Last-Modified; the live one treated a disagreement about the
//     validator's PRESENCE as torn. THE STRICTER, LIVE READING WINS and is
//     now the only implementation — see detectTornSet's comment in
//     internal/esi/pagination/torn.go for the argument. This is a
//     tightening of §5.9's wording, recorded deliberately rather than
//     applied silently.
//
// The CURSOR mechanism (§5.9's other row) has a real consumer as of this
// phase and is implemented at the bottom of this file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/esi/pagination"
)

// nonOKFirstPage carries a page-1 response that is not a 200 back out
// through pagination.FetchAllPages, which is only interested in errors.
//
// A 304, a 403, a 429 or a data-level 404 on page 1 is not a pagination
// failure at all — it is the caller's ordinary response to handle, exactly
// as if the route had never been paginated. Wrapping it as an error is the
// price of routing page 1 through the same fetcher as every other page,
// which is what keeps the walk logic in one place.
type nonOKFirstPage struct{ resp *esi.Response }

func (e *nonOKFirstPage) Error() string {
	return fmt.Sprintf("worker: page 1 returned status %d", e.resp.StatusCode)
}

// fetchAllPages walks a page-paginated route (page=1..X-Pages) through
// internal/esi/pagination and returns the assembled set as one synthesized
// esi.Response, so nothing downstream needs to know pagination happened.
//
// A torn set (pagination.ErrTornPageSet) propagates as an error and the
// WHOLE payload is discarded — never partially committed. The subscription
// retries on its normal cadence.
func fetchAllPages(ctx context.Context, gw *esi.Client, base esi.Request) (*esi.Response, error) {
	var firstResp *esi.Response

	results, err := pagination.FetchAllPages(ctx, func(ctx context.Context, page int) (pagination.PageResult, error) {
		resp, doErr := gw.Do(ctx, cloneRequestWithPage(base, page))
		if doErr != nil {
			if page == 1 {
				return pagination.PageResult{}, doErr
			}
			return pagination.PageResult{}, fmt.Errorf("worker: fetching page %d for %s: %w", page, base.UpstreamPath, doErr)
		}
		if resp.StatusCode != http.StatusOK {
			if page == 1 {
				return pagination.PageResult{}, &nonOKFirstPage{resp: resp}
			}
			return pagination.PageResult{}, fmt.Errorf("worker: page %d of %s returned status %d mid-walk", page, base.UpstreamPath, resp.StatusCode)
		}
		if page == 1 {
			firstResp = resp
		}
		return pagination.PageResult{
			Page: page, TotalPages: resp.Pages,
			LastModified: resp.LastModified, HasLastModified: resp.HasLastModified,
			Body: resp.Body,
		}, nil
	})
	if err != nil {
		var nonOK *nonOKFirstPage
		if errors.As(err, &nonOK) {
			return nonOK.resp, nil
		}
		if errors.Is(err, pagination.ErrTornPageSet) {
			return nil, fmt.Errorf("worker: torn page set for %s — discarding, will retry next scheduled attempt: %w", base.UpstreamPath, err)
		}
		return nil, err
	}

	if len(results) == 1 {
		// Single-page set: hand back the real response rather than a
		// re-serialised copy of it, so an ETag/Last-Modified round trip on
		// an unpaginated route is byte-identical to what it was before this
		// route joined the paginated set.
		return firstResp, nil
	}

	var elements []json.RawMessage
	for _, r := range results {
		more, err := splitJSONArray(r.Body)
		if err != nil {
			return nil, fmt.Errorf("worker: page %d of %s is not a JSON array: %w", r.Page, base.UpstreamPath, err)
		}
		elements = append(elements, more...)
	}

	return &esi.Response{
		StatusCode: http.StatusOK, Body: joinJSONArray(elements), ETag: firstResp.ETag,
		LastModified: firstResp.LastModified, HasLastModified: firstResp.HasLastModified,
		Pages: firstResp.Pages,
	}, nil
}

func cloneRequestWithPage(base esi.Request, page int) esi.Request {
	req := base
	req.Query = cloneQuery(base.Query)
	req.Query["page"] = []string{strconv.Itoa(page)}
	return req
}

func cloneQuery(q map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(q)+1)
	for k, v := range q {
		cloned[k] = v
	}
	return cloned
}

// splitJSONArray decodes a JSON array body into its raw element messages
// without re-marshalling them (preserving exact upstream byte content —
// important for money fields' decimal precision), for later concatenation
// across pages.
func splitJSONArray(body []byte) ([]json.RawMessage, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal(body, &elements); err != nil {
		return nil, err
	}
	return elements, nil
}

// joinJSONArray re-assembles a slice of raw JSON elements into one JSON
// array body.
func joinJSONArray(elements []json.RawMessage) []byte {
	out := make([]byte, 0, 2+len(elements)*32)
	out = append(out, '[')
	for i, e := range elements {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	out = append(out, ']')
	return out
}

// ── §5.9's CURSOR MECHANISM ──────────────────────────────────────────────
//
// Answered at last. Phase 20.1.1 found that GET /corporations/{id}/projects
// returns CorporationsProjectsListing — an OBJECT of {cursor, projects} —
// parsed the envelope, captured the cursor, and did not follow it, so a
// corporation with more than one page of projects synced only the first
// (recorded in handlers/project_sync.go rather than silently truncated).
// That is the real consumer §5.9's cursor mechanism never had, and the
// reason CursorQuery/FetchAllCursor are implemented and wired here instead
// of deleted as dead code.
//
// ── NO TORN-SET CHECK ON A CURSOR WALK, DELIBERATELY ─────────────────────
// §5.9 states the Last-Modified rule under the `page` row and only there,
// and that is right rather than an omission: the cursor parameters are
// documented by CCP as "continue walking forwards in time", i.e. the
// mechanism is designed for a set that is being appended to while it is
// read. Applying the page rule here would make a corporation whose projects
// change during a walk permanently unsyncable — the failure mode the check
// exists to prevent, inverted.

// cursorEnvelope is the part of a cursor-paginated body this package reads:
// the cursor, and nothing else. The items themselves stay as raw JSON and
// are handed to the route's own handler untouched (money precision,
// Principle 9). Cursor values are opaque — read, echoed back, never parsed
// or synthesised (§5.9).
type cursorEnvelope struct {
	Cursor *struct {
		After  *string `json:"after"`
		Before *string `json:"before"`
	} `json:"cursor"`
}

// fetchAllCursorPages walks a cursor-paginated route from the start of the
// set and returns ONE synthesized response whose body is a single envelope
// of the same shape, with every page's items concatenated under itemsField.
// Reassembling the envelope rather than a bare array is what lets the
// route's existing handler stay exactly as it is: it parses the same shape
// it always did, and simply never learns that four requests produced it.
func fetchAllCursorPages(ctx context.Context, gw *esi.Client, base esi.Request, itemsField string) (*esi.Response, error) {
	var firstResp *esi.Response
	var nonOK *esi.Response

	bodies, err := pagination.FetchAllCursor(ctx, func(ctx context.Context, dir pagination.Direction, cursor string) (pagination.CursorPage, error) {
		req := base
		req.Query = cloneQuery(base.Query)
		for k, v := range pagination.CursorQuery(dir, cursor) {
			req.Query[k] = v
		}

		resp, doErr := gw.Do(ctx, req)
		if doErr != nil {
			return pagination.CursorPage{}, doErr
		}
		if resp.StatusCode != http.StatusOK {
			if firstResp == nil {
				// Same reasoning as nonOKFirstPage above: a 304/403/429 on
				// the first request is the caller's business, not a
				// pagination failure. Recorded and the walk stopped.
				nonOK = resp
				return pagination.CursorPage{HasMore: false}, nil
			}
			return pagination.CursorPage{}, fmt.Errorf("worker: cursor page of %s returned status %d mid-walk", base.UpstreamPath, resp.StatusCode)
		}
		if firstResp == nil {
			firstResp = resp
		}

		var env cursorEnvelope
		if err := json.Unmarshal(resp.Body, &env); err != nil {
			return pagination.CursorPage{}, fmt.Errorf("worker: cursor page of %s is not a {cursor, ...} envelope: %w", base.UpstreamPath, err)
		}
		next := ""
		if env.Cursor != nil && env.Cursor.After != nil {
			next = *env.Cursor.After
		}
		// A cursor that repeats itself would loop forever. ESI signals
		// end-of-set by omitting `after`; guarding on equality as well
		// costs nothing and turns a hang into a clean stop.
		return pagination.CursorPage{
			Body:       resp.Body,
			NextCursor: next,
			HasMore:    next != "" && next != cursor,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	if nonOK != nil {
		return nonOK, nil
	}
	if firstResp == nil {
		return nil, fmt.Errorf("worker: cursor walk of %s produced no response", base.UpstreamPath)
	}
	if len(bodies) == 1 {
		return firstResp, nil
	}

	merged, err := mergeCursorEnvelopes(bodies, itemsField)
	if err != nil {
		return nil, fmt.Errorf("worker: merging cursor pages of %s: %w", base.UpstreamPath, err)
	}
	return &esi.Response{
		StatusCode: http.StatusOK, Body: merged, ETag: firstResp.ETag,
		LastModified: firstResp.LastModified, HasLastModified: firstResp.HasLastModified,
	}, nil
}

// mergeCursorEnvelopes concatenates each page's itemsField array into one
// envelope. The merged envelope carries NO cursor: the walk is complete, and
// leaving the last page's cursor in place would tell a reader there is more
// when there is not.
func mergeCursorEnvelopes(bodies [][]byte, itemsField string) ([]byte, error) {
	var elements []json.RawMessage
	for _, body := range bodies {
		var page map[string]json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		raw, ok := page[itemsField]
		if !ok {
			// A page with no items field at all is an empty page, not a
			// malformed one — the field is required by the schema but an
			// absent-vs-empty distinction is not worth failing a whole walk.
			continue
		}
		more, err := splitJSONArray(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not an array: %w", itemsField, err)
		}
		elements = append(elements, more...)
	}

	out := make(map[string]json.RawMessage, 1)
	out[itemsField] = joinJSONArray(elements)
	return json.Marshal(out)
}
