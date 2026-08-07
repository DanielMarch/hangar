// Package cache implements HANGAR's two-tier conditional ESI cache
// (01_ARCHITECTURE.md §5.4): L1 is an in-process ristretto cache
// cost-weighted by body size; L2 is either the Postgres UNLOGGED
// app.esi_cache_entry table or, when configured, Redis — never
// authoritative, degrading to a miss on any error.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// fieldSeparator delimits fields within the hashed byte stream. Any single
// byte outside the printable ASCII range used by the fields themselves
// works; 0x1F (ASCII "unit separator") is chosen for its historical intent
// (a field delimiter, distinct from data) and because none of the inputs
// below can ever legitimately contain it.
const fieldSeparator = 0x1F

// KeyInput is every field 01_ARCHITECTURE.md §5.3's cache-key formula
// names: sha256(method ‖ normalized_path ‖ sorted_query ‖
// compatibility_date ‖ tenant ‖ resolved_esi_language ‖ token_subject).
type KeyInput struct {
	Method            string
	Path              string // the resolved request path — NEVER touched beyond NormalizePath's envelope cleanup
	Query             url.Values
	CompatibilityDate string // "YYYY-MM-DD", the app pin at request time
	Tenant            string
	ResolvedLanguage  string // ESI's Accept-Language value, never the UI locale (internal/i18n)
	TokenSubject      string // "CHARACTER:EVE:<id>", or the literal "anonymous"
}

// Key computes the cache key for one request, as a lowercase hex string
// (a stable, loggable, and directly usable Postgres/Redis key form —
// app.esi_cache_entry.cache_key is bytea and accepts the raw sha256 sum
// just as well, see KeyBytes).
func Key(in KeyInput) string {
	return hex.EncodeToString(KeyBytes(in))
}

// KeyBytes computes the raw 32-byte sha256 sum, for callers (the Postgres
// L2 store) that want app.esi_cache_entry.cache_key's native bytea form
// rather than a hex string.
func KeyBytes(in KeyInput) []byte {
	h := sha256.New()
	write := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{fieldSeparator})
	}
	write(strings.ToUpper(in.Method))
	write(NormalizePath(in.Path))
	write(NormalizeQuery(in.Query))
	write(in.CompatibilityDate)
	write(in.Tenant)
	write(in.ResolvedLanguage)
	write(in.TokenSubject)
	sum := h.Sum(nil)
	return sum
}

// NormalizePath applies envelope-only normalisation to a resolved request
// path (upstream_path with its {parameters} already substituted): a
// trailing slash is trimmed (the root path "/" is left alone) and any
// percent-encoded octet's hex digits are upper-cased for a canonical form.
// It NEVER reorders, renames, or otherwise rewrites a path segment's
// content — 01_ARCHITECTURE.md §5.3's "upstream_path is the sole authority
// for request construction" — so the singular
// "/corporation/{corporation_id}/mining/extractions" survives unchanged.
func NormalizePath(path string) string {
	path = canonicalizePercentEncoding(path)
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

func canonicalizePercentEncoding(s string) string {
	b := []byte(s)
	for i := 0; i+2 < len(b); i++ {
		if b[i] == '%' && isHexDigit(b[i+1]) && isHexDigit(b[i+2]) {
			b[i+1] = upperHexDigit(b[i+1])
			b[i+2] = upperHexDigit(b[i+2])
			i += 2
		}
	}
	return string(b)
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func upperHexDigit(c byte) byte {
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 'A'
	}
	return c
}

// NormalizeQuery renders query parameters in a canonical, deterministic
// form: keys sorted, and each key's values sorted too (map/slice
// iteration order is never assumed to be stable upstream of this call).
func NormalizeQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			pairs = append(pairs, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(pairs, "&")
}

// NormalizeEnvelope returns a copy of u with scheme/host lower-cased and
// its path run through NormalizePath. It never mutates u.
func NormalizeEnvelope(u *url.URL) *url.URL {
	out := *u
	out.Scheme = strings.ToLower(out.Scheme)
	out.Host = strings.ToLower(out.Host)
	out.Path = NormalizePath(out.Path)
	return &out
}
