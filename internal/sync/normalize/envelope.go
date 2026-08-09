// Package normalize turns a completed sync attempt's HTTP envelope into the
// generic, route-agnostic bookkeeping fields app.sync_run needs: an outcome
// classification and (when the body is a JSON array) a row count. It stops
// at the envelope deliberately — turning a response BODY into typed domain
// rows is a per-route concern that lands with each route-handler phase
// (Phase 7+, internal/sync/handlers) — this package must never parse a path
// or a domain field (01_ARCHITECTURE.md §3: "Per-route response
// normalisers (request-envelope only; never paths)").
package normalize

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Outcome classifies a completed sync attempt into app.sync_run.outcome's
// open vocabulary (02_DATABASE_SCHEMA.md §4.3 #32: "open vocabulary:
// '304'|'200'|'429'|'error'|..."). It is deliberately not a closed Go enum:
// a status code this build has never special-cased still round-trips as
// its own string (e.g. "500"), never coerced into a generic "error" —
// Principle 14 applies to outcome vocabularies HANGAR itself defines as
// "open" just as much as to ones CCP defines. transportErr covers the one
// case with no status code to report at all: connection failure, timeout,
// or context cancellation.
func Outcome(statusCode int, transportErr bool) string {
	if transportErr || statusCode <= 0 {
		return "error"
	}
	return strconv.Itoa(statusCode)
}

// RowsAffected counts elements in a JSON array response body — a
// route-agnostic, envelope-level fact (it never looks at what the elements
// contain) suitable for app.sync_run.rows_affected. ok is false when body
// doesn't decode as a JSON array (empty body, a single object, a 304 with
// no body, malformed JSON) — callers should leave rows_affected NULL in
// that case rather than storing a misleading zero.
func RowsAffected(body []byte) (count int32, ok bool) {
	if len(body) == 0 {
		return 0, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(body, &arr); err != nil {
		return 0, false
	}
	return int32(len(arr)), true
}

// ETag and LastModified read the standard conditional-cache validator
// headers off a completed response — the same two headers
// internal/esi/cache.Validators models for the L1/L2 cache, extracted here
// for the separate purpose of persisting app.sync_subscription.etag /
// .last_modified so the NEXT attempt's conditional request can be built
// without re-reading the cache store.
func ETag(h http.Header) string {
	return h.Get("ETag")
}

// LastModified parses the Last-Modified header (RFC 7231 HTTP-date). ok is
// false when the header is absent or fails to parse — callers should leave
// app.sync_subscription.last_modified untouched, not zero-time, on !ok.
func LastModified(h http.Header) (t time.Time, ok bool) {
	raw := h.Get("Last-Modified")
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
