package transport

import (
	"bytes"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// RetryConfig bounds the retry layer. It exists only for transport-level
// failures (connection reset, timeout, DNS failure) and 5xx responses —
// never for 4xx, and never for 429 specifically: a 429 is Governor 1's
// signal (Phase 4's rate-limit ledger), and retrying it here would both
// double-count against the ledger and race the subscription snooze that
// is supposed to be the only thing that waits it out.
type RetryConfig struct {
	MaxAttempts int           // total attempts, including the first; 1 disables retry
	BaseDelay   time.Duration // delay before the first retry
	MaxDelay    time.Duration // cap on backoff growth
}

// DefaultRetryConfig is a conservative default: three attempts, starting
// at 200ms, capped at 2s, doubling with full jitter between attempts.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   200 * time.Millisecond,
	MaxDelay:    2 * time.Second,
}

type retryTransport struct {
	next http.RoundTripper
	cfg  RetryConfig
	rand *rand.Rand
}

// WithRetry wraps next with bounded retry of transient failures. Only
// idempotent methods (GET, HEAD) are retried — every route this gateway
// calls is a read, but the guard is kept explicit rather than assumed.
func WithRetry(next http.RoundTripper, cfg RetryConfig) http.RoundTripper {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	return &retryTransport{next: next, cfg: cfg, rand: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return t.next.RoundTrip(req)
	}

	// A request body must be re-readable across attempts. GET/HEAD never
	// carry one in this gateway, but guard correctly rather than assume.
	var bodyBytes []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		bodyBytes = b
	}

	var lastErr error
	var lastResp *http.Response
	for attempt := 0; attempt < t.cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := t.backoff(attempt)
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}
		}

		attemptReq := req.Clone(req.Context())
		if bodyBytes != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := t.next.RoundTrip(attemptReq)
		if err != nil {
			lastErr = err
			lastResp = nil
			continue // transport-level failure: retry
		}
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			// Retry 5xx, but drain and close first — an unread,
			// unclosed body from a discarded attempt leaks the
			// connection.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			lastErr = nil
			lastResp = resp
			continue
		}
		// Anything else — 2xx, 3xx, 4xx including 429 — is returned
		// immediately, retried or not: those are meaningful responses
		// the caller (and, for 429, Phase 4's ledger) must see.
		return resp, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return lastResp, nil
}

func (t *retryTransport) backoff(attempt int) time.Duration {
	d := t.cfg.BaseDelay << (attempt - 1)
	if d > t.cfg.MaxDelay || d <= 0 {
		d = t.cfg.MaxDelay
	}
	// Full jitter: uniform in [0, d).
	if d <= 0 {
		return 0
	}
	return time.Duration(t.rand.Int63n(int64(d)))
}
