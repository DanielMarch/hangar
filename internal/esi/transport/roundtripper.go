package transport

import "net/http"

// Options configures the outbound ESI transport chain.
type Options struct {
	Version       string    // for the User-Agent, e.g. main.version
	ContactURL    string    // for the User-Agent — the operator's own URL/email
	Pin           PinSource // resolves the app compatibility date pin
	Retry         RetryConfig
	BaseTransport http.RoundTripper // defaults to http.DefaultTransport
}

// userAgentTransport sets a fixed User-Agent on every request.
type userAgentTransport struct {
	next      http.RoundTripper
	userAgent string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("User-Agent", t.userAgent)
	return t.next.RoundTrip(cloned)
}

// New builds the full outbound chain: retry(compatDate(userAgent(base))).
// Order matters: User-Agent and X-Compatibility-Date must be set on every
// attempt a retry makes, so those layers sit inside (closer to the wire
// than) the retry layer — a naive ordering that put retry innermost would
// retry a request that never had its headers set on the retried attempt,
// since RoundTrip clones defensively at each layer.
func New(opts Options) http.RoundTripper {
	base := opts.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}
	userAgent := BuildUserAgent(opts.Version, opts.ContactURL)

	chain := &userAgentTransport{next: base, userAgent: userAgent}
	withDate := WithCompatibilityDate(chain, opts.Pin)
	return WithRetry(withDate, opts.Retry)
}
