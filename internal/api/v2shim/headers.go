package v2shim

import (
	"net/http"
	"time"
)

// Deprecation / Sunset signalling — SRS §10: "Every shim response carries
// `Deprecation: true` and a `Sunset` header (RFC 8594) set to the removal
// date."
//
// Both headers go on EVERY shim response, including errors and the 410s
// for the reshaped routes. A client that only ever hits an error path is
// exactly the client that most needs to be told the surface is going away,
// and a header that appears only on success is one an integrator's logging
// will never surface.
const (
	// DeprecationHeader — draft-ietf-httpapi-deprecation-header. The
	// literal string "true" rather than a date: the field's boolean form
	// says "deprecated now", which is the true statement here. The
	// removal date lives in Sunset, which is what RFC 8594 is for.
	DeprecationHeader = "Deprecation"
	// SunsetHeader — RFC 8594. An IMF-fixdate, per that RFC.
	SunsetHeader = "Sunset"
	// LinkHeader carries the deprecation documentation, which RFC 8594 §3
	// recommends alongside Sunset. Without it the client is told when the
	// surface disappears but not what to do about it.
	LinkHeader = "Link"
)

// SunsetDate is when `/api/v2` is removed.
//
// SRS §10 fixes the POLICY — "ships in v1.0 and is removed no earlier than
// two minor versions later", with a release-note announcement at least one
// minor version in advance — but a policy is not a date, and RFC 8594
// requires a date. This is that date, stated once, here, so the header, the
// migration guide and the release notes cannot drift apart.
//
// Chosen as 12 months from the v1.0 target: long enough that an integrator
// with a maintenance window twice a year gets two of them, short enough
// that the shim does not become permanent by neglect. Moving it LATER is a
// deliberate decision requiring a release-note announcement; moving it
// EARLIER breaks the §10 promise and must not happen.
var SunsetDate = time.Date(2027, time.August, 31, 23, 59, 59, 0, time.UTC)

// DeprecationDocsURL is where a client that reads the Link header lands.
const DeprecationDocsURL = "https://github.com/hangar-project/hangar/blob/main/docs/APPENDIX_C_MIGRATION.md"

// ApplyDeprecationHeaders stamps the sunset signalling onto w. Called for
// every response the shim produces, on every path.
func ApplyDeprecationHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set(DeprecationHeader, "true")
	header.Set(SunsetHeader, SunsetDate.UTC().Format(http.TimeFormat))
	header.Add(LinkHeader, `<`+DeprecationDocsURL+`>; rel="deprecation"; type="text/html"`)
	header.Add(LinkHeader, `<`+DeprecationDocsURL+`>; rel="sunset"; type="text/html"`)
}

// writeJSON sends an already-encoded legacy body.
//
// Content-Type is `application/json` with no charset parameter, matching
// what Laravel's response()->json() sets — a client doing an exact-match
// comparison on the header (which is wrong of it, and which real clients
// do) keeps working.
func writeJSON(w http.ResponseWriter, status int, body []byte) {
	ApplyDeprecationHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeLegacyError emits legacy's error shape: a BARE JSON STRING, not an
// object.
//
// That is genuinely what SeAT does — `return response()->json('Unauthorized',
// 401)` in Http/Middleware/ApiToken.php — and reproducing it matters more
// than improving on it. A client that has been reading `response.json()` as
// a string for years and gets `{"error": "..."}` instead has been handed a
// new bug by the compatibility layer that exists to prevent exactly that.
func writeLegacyError(w http.ResponseWriter, status int, message string) {
	body, err := Encode(message)
	if err != nil {
		// Encoding a plain string cannot fail; if it somehow does, say so
		// rather than emitting a 200 with nothing in it.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, status, body)
}
