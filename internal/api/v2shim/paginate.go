package v2shim

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// LegacyPerPage is Laravel's `paginate()` default — Eloquent's
// `Model::$perPage`, which none of eveseat's models override. Every legacy
// /api/v2 collection therefore pages at 15, and a client that has been
// counting on 15 keeps getting 15.
//
// Deliberately NOT configurable and NOT reconciled with /api/v1's limit
// range of 10-100: the shim's job is to look like the thing it replaces.
const LegacyPerPage = 15

// Page is one page of a legacy collection, as the shim has fetched it.
type Page struct {
	// Rows are the already-translated items in this page.
	Rows Arr
	// Total is the number of rows in the WHOLE collection.
	Total int64
	// CurrentPage is 1-based, as legacy's `?page=` is.
	CurrentPage int
	// PerPage is LegacyPerPage unless a route legitimately differs.
	PerPage int
	// BaseURL is the absolute URL of this route WITHOUT query parameters
	// — Laravel's `path`. Built from the incoming request.
	BaseURL string
	// Query is the query string legacy would append to the pagination
	// links, `page` excluded. Empty for the routes whose controller calls
	// plain `paginate()` rather than `paginate()->appends(...)` — an
	// asymmetry in the legacy controllers that IS visible in the bytes,
	// so the shim reproduces it per route rather than picking one.
	Query url.Values
}

// Envelope renders Laravel's paginated resource response:
//
//	{"data":[...],"links":{...},"meta":{...}}
//
// The shapes come from Illuminate\Http\Resources\Json\PaginatedResourceResponse
// with eveseat's own override, which drops `links` from `meta`
// (Arr::except($meta, ['links'])) — so `meta` is exactly current_page,
// from, last_page, path, per_page, to, total, in that order. See
// testdata/legacy-api-v2/README.md for the recording that pins it.
func (p Page) Envelope() *Obj {
	perPage := p.PerPage
	if perPage <= 0 {
		perPage = LegacyPerPage
	}
	current := p.CurrentPage
	if current < 1 {
		current = 1
	}

	// Laravel's LengthAwarePaginator: lastPage is at least 1 even for an
	// empty set, so `last_page: 0` never appears.
	lastPage := int((p.Total + int64(perPage) - 1) / int64(perPage))
	if lastPage < 1 {
		lastPage = 1
	}

	// `from`/`to` are NULL on a page with no rows — not 0. The empty
	// collection in the corpus (character.assets.empty) pins this.
	var from, to any
	if len(p.Rows) > 0 {
		first := int64(current-1)*int64(perPage) + 1
		from = Int(first)
		to = Int(first + int64(len(p.Rows)) - 1)
	}

	links := NewObj(4).
		Set("first", p.pageURL(1)).
		Set("last", p.pageURL(lastPage))
	if current > 1 {
		links.Set("prev", p.pageURL(current-1))
	} else {
		links.Set("prev", nil)
	}
	if current < lastPage {
		links.Set("next", p.pageURL(current+1))
	} else {
		links.Set("next", nil)
	}

	meta := NewObj(7).
		Set("current_page", Int(current)).
		Set("from", from).
		Set("last_page", Int(lastPage)).
		Set("path", p.BaseURL).
		Set("per_page", Int(perPage)).
		Set("to", to).
		Set("total", Int(p.Total))

	rows := p.Rows
	if rows == nil {
		// An empty page is `[]`, never `null`. Legacy has no notion of
		// "unavailable", so a nil slice reaching here would silently
		// invent one — the exact Phase 18 confusion between an empty
		// success and a failure.
		rows = Arr{}
	}

	return NewObj(3).Set("data", rows).Set("links", links).Set("meta", meta)
}

// pageURL builds one pagination link the way Laravel's UrlWindow does:
// the base path, the appended query parameters (if this route's controller
// used ->appends()), then `page`.
//
// Laravel appends `page` LAST and orders the rest as they arrived, which is
// visible in the bytes, so this preserves Query's own order rather than
// sorting it.
func (p Page) pageURL(page int) string {
	if len(p.Query) == 0 {
		return p.BaseURL + "?page=" + strconv.Itoa(page)
	}
	var parts []string
	for key, values := range p.Query {
		for _, value := range values {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return p.BaseURL + "?" + strings.Join(parts, "&") + "&page=" + strconv.Itoa(page)
}

// ItemEnvelope is the single-resource response: `{"data": {...}}`. Legacy's
// non-collection routes (character sheet, corporation sheet, killmail
// detail, squad show, user show) all use it.
func ItemEnvelope(item any) *Obj {
	return NewObj(1).Set("data", item)
}

// ParsePage reads legacy's `?page=` parameter.
//
// Laravel's resolver treats a non-numeric or non-positive page as page 1
// rather than as a client error, and so does this: a migrating client that
// sends `?page=` empty on its first request must not get a 400 it never got
// before.
func ParsePage(raw string) int {
	if raw == "" {
		return 1
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// Window slices an ordered result set down to one legacy page.
//
// ── A REAL TENSION, RESOLVED IN FAVOUR OF THE INVARIANT ───────────────────
// Legacy's pagination contract is `?page=N`, and the obvious implementation
// is `LIMIT 15 OFFSET (N-1)*15`. HANGAR prohibits OFFSET outright — SRS §6,
// §17 invariant 10 — and the prohibition is not advisory: sqlc.yaml wires
// the `no-offset` rule into `sqlc vet`, which `make lint` and therefore
// `make ci` run, so an OFFSET in db/queries fails the build.
//
// The roadmap asks for one and the repo forbids the other. That is a
// genuine inconsistency between Phase 19's brief and a standing invariant,
// and it is reported rather than quietly resolved — see the Phase 19
// report and docs/APPENDIX_C_MIGRATION.md.
//
// The invariant wins, because it is the older and broader commitment and
// because the shim is the temporary surface. Pages are therefore taken by
// reading the ordered set through an existing keyset query and slicing in
// Go. The cost is honest and bounded: page 1 — overwhelmingly the common
// case for a legacy client — reads exactly what it returns, and deep pages
// read more rows than they return, exactly as an OFFSET scan would have.
// What the shim does NOT do is pretend that is free.
func Window[T any](rows []T, page, perPage int) []T {
	if perPage <= 0 {
		perPage = LegacyPerPage
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * perPage
	if start >= len(rows) {
		// Past the end is an EMPTY page, not an error and not the last
		// page — which is what Laravel's paginator does, and what a client
		// walking `next` until it stops depends on.
		return nil
	}
	end := start + perPage
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

// BaseURL reconstructs Laravel's `path` for the current request: scheme,
// host and path, no query.
//
// Every pagination link in every collection response embeds this, so it
// must be derived from the request the client actually made — a hardcoded
// host would hand a client behind a reverse proxy links pointing somewhere
// it cannot reach.
func BaseURL(scheme, host, path string) string {
	if scheme == "" {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}
