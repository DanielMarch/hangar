package cache

import (
	"net/http"
	"time"
)

// Validators is the pair of conditional-request tokens a stored cache
// Entry may carry.
type Validators struct {
	ETag            string
	LastModified    time.Time
	HasLastModified bool
}

// IsNoStore reports whether mode is HANGAR's no-store scheduling mode:
// "no L1 write, no L2 write, and no conditional headers sent"
// (01_ARCHITECTURE.md §5.4). It recognises both the live ESI spec's actual
// value — "not-cached", confirmed against
// internal/esi/catalogue/embedded/openapi.snapshot.json, and already the
// value internal/esi/catalogue.SchedulingMode treats specially — and
// 01_ARCHITECTURE.md §5.4/§6.2's own name for it, "no-cache". Neither this
// package nor Principle 14 should bet on which spelling a future CCP spec
// revision settles on, so both are honoured.
func IsNoStore(mode string) bool {
	return mode == "not-cached" || mode == "no-cache"
}

// ApplyConditionalHeaders sets If-None-Match / If-Modified-Since on req
// from a stored validator set, unless mode is a no-store mode — in which
// case it sends nothing at all, per contract, and reports false. The
// return value lets a caller (or a test) assert the "no conditional
// headers sent" half of the no-store contract directly, rather than by
// inspecting req.Header after the fact.
func ApplyConditionalHeaders(req *http.Request, v *Validators, mode string) bool {
	if IsNoStore(mode) || v == nil {
		return false
	}
	sent := false
	if v.ETag != "" {
		req.Header.Set("If-None-Match", v.ETag)
		sent = true
	}
	if v.HasLastModified {
		req.Header.Set("If-Modified-Since", v.LastModified.UTC().Format(http.TimeFormat))
		sent = true
	}
	return sent
}
