// Package ratelimit implements Governor 1 (the per-(group, userID)
// floating-window consumption ledger) and Governor 2 (the installation-wide
// error limit) — 01_ARCHITECTURE.md §5.5-§5.7.
package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cost table (01_ARCHITECTURE.md §5.5), evaluated top-down — 429 overrides
// the "other 4XX" row below it.
const (
	CostReserved       int16 = 5 // predictive reservation: the worst case, held until settle
	Cost2XX            int16 = 2
	Cost3XX            int16 = 1
	Cost4XXOther       int16 = 5
	Cost429            int16 = 0
	Cost5XX            int16 = 0
	CostTransportError int16 = 5
)

// ClassifyCost maps a response outcome to its ledger cost. status is the
// HTTP status code observed; transportErr is true when no response was
// received at all (connection error, timeout, context deadline). The 429
// row is checked first because it overrides the general 4XX row — an
// ordering the exit tests (Test429ExemptionOverrides4XXCost) pin down.
func ClassifyCost(status int, transportErr bool) int16 {
	if transportErr {
		return CostTransportError
	}
	switch {
	case status == http.StatusTooManyRequests: // 429
		return Cost429
	case status >= 200 && status < 300:
		return Cost2XX
	case status >= 300 && status < 400:
		return Cost3XX
	case status >= 400 && status < 500:
		return Cost4XXOther
	case status >= 500 && status < 600:
		return Cost5XX
	default:
		// Anything outside 1xx-5xx is not a real HTTP outcome; treat it
		// as conservatively as a transport error rather than guessing.
		return CostTransportError
	}
}

// ParseRateLimitLimit parses X-Ratelimit-Limit's "<max-tokens>/<window>"
// form, where <window> is a bare integer followed by a unit suffix of `m`
// (minutes) or `h` (hours) — 01_ARCHITECTURE.md §5.5.
func ParseRateLimitLimit(value string) (maxTokens int, window time.Duration, err error) {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ratelimit: X-Ratelimit-Limit %q: want <max-tokens>/<window>", value)
	}
	maxTokens, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("ratelimit: X-Ratelimit-Limit %q: max-tokens: %w", value, err)
	}

	windowStr := strings.TrimSpace(parts[1])
	if windowStr == "" {
		return 0, 0, fmt.Errorf("ratelimit: X-Ratelimit-Limit %q: empty window", value)
	}
	suffix := windowStr[len(windowStr)-1]
	numPart := windowStr[:len(windowStr)-1]
	n, err := strconv.Atoi(numPart)
	if err != nil {
		return 0, 0, fmt.Errorf("ratelimit: X-Ratelimit-Limit %q: window: %w", value, err)
	}
	switch suffix {
	case 'm', 'M':
		window = time.Duration(n) * time.Minute
	case 'h', 'H':
		window = time.Duration(n) * time.Hour
	default:
		return 0, 0, fmt.Errorf("ratelimit: X-Ratelimit-Limit %q: unrecognised window suffix %q (want m or h)", value, string(suffix))
	}
	return maxTokens, window, nil
}

// ParseRemaining parses X-Ratelimit-Remaining. ok is false when the header
// is absent — callers must not treat "absent" as "zero" (01_ARCHITECTURE.md
// §5.5's headerless-429 edge case turns exactly on this distinction).
func ParseRemaining(value string) (remaining int, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Outcome is the fully classified result of one outbound ESI response,
// ready for Ledger.Settle and Governor 2's error accounting.
type Outcome struct {
	Cost int16

	// ServerRemaining is set when the response carried
	// X-Ratelimit-Remaining, for reconciliation (§5.5 "the server always
	// wins"). ServerRemainingOK is false when the header was absent.
	ServerRemaining   int
	ServerRemainingOK bool

	// SnoozeFor is set on a 429: Retry-After's value if present, else
	// the caller's ttl_floor. Zero means "no snooze required" (never the
	// case for a 429 in this design — see ClassifyResponse).
	SnoozeFor time.Duration

	// Is429Headerless records a 429 that arrived with no rate-limit
	// headers at all (CCP's in-monolith limiters do this) — the caller
	// increments esi_429_headerless_total{group} on this signal.
	Is429Headerless bool

	// IsErrorForGovernor2 is true for any non-2XX/3XX outcome (including
	// transport errors), the population Governor 2 counts against
	// (§5.7). 429s count too: Governor 2 has no exemption for them.
	IsErrorForGovernor2 bool
}

// ClassifyResponse turns an observed HTTP response (or its absence, on a
// transport error) plus the caller's ttl_floor into a full Outcome. header
// is nil on a transport error/timeout, since there is no response to read.
func ClassifyResponse(status int, header http.Header, transportErr bool, ttlFloor time.Duration) Outcome {
	cost := ClassifyCost(status, transportErr)
	out := Outcome{Cost: cost}

	if transportErr {
		out.IsErrorForGovernor2 = true
		return out
	}

	out.IsErrorForGovernor2 = status < 200 || status >= 400

	if remaining, ok := ParseRemaining(header.Get("X-Ratelimit-Remaining")); ok {
		out.ServerRemaining, out.ServerRemainingOK = remaining, true
	}

	if status == http.StatusTooManyRequests {
		if retryAfter := header.Get("Retry-After"); retryAfter != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil {
				out.SnoozeFor = time.Duration(secs) * time.Second
			}
		}
		if out.SnoozeFor <= 0 {
			out.SnoozeFor = ttlFloor
		}
		if !out.ServerRemainingOK {
			out.Is429Headerless = true
		}
	}

	return out
}
