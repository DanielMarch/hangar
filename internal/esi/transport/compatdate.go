package transport

import (
	"fmt"
	"net/http"
)

// PinSource returns the current app-wide compatibility date pin
// (internal/esi/catalogue.GetPin's return, formatted "YYYY-MM-DD"). It is
// a function rather than a static string so the transport chain always
// reads whatever the pin currently is — including immediately after an
// administrator calls AdvancePin — without needing to be rebuilt.
type PinSource func() (string, error)

// compatDateTransport is the "unconditional X-Compatibility-Date from the
// app pin" layer (01_ARCHITECTURE.md §5.1): every data request carries the
// app pin, never D_max (D_max is exclusively for the catalogue's own
// discovery fetch — internal/esi/catalogue/fetch.go — which does not run
// through this transport at all). An absent X-Compatibility-Date resolves
// upstream to the *oldest* date, which is never correct, so this layer
// cannot be opted out of and a missing pin is a hard failure, not a
// silently-omitted header.
type compatDateTransport struct {
	next http.RoundTripper
	pin  PinSource
}

// WithCompatibilityDate wraps next so every request through it carries
// X-Compatibility-Date: <app pin>.
func WithCompatibilityDate(next http.RoundTripper, pin PinSource) http.RoundTripper {
	return &compatDateTransport{next: next, pin: pin}
}

func (t *compatDateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	pin, err := t.pin()
	if err != nil {
		return nil, fmt.Errorf("transport: resolving compatibility date pin: %w", err)
	}
	if pin == "" {
		// "A request built without one is a panic in development and a
		// hard error in production" (01_ARCHITECTURE.md §5.1). This layer
		// is the one place that distinction collapses to one behaviour:
		// an empty pin here means GetPin's own seeding failed, which is a
		// configuration-level defect no request should paper over.
		panic("transport: compatibility date pin resolved to the empty string — refusing to send an unpinned ESI request")
	}
	// http.RoundTripper implementations must not mutate the original
	// request (net/http's own documented contract) — clone before setting
	// the header.
	cloned := req.Clone(req.Context())
	cloned.Header.Set("X-Compatibility-Date", pin)
	return t.next.RoundTrip(cloned)
}
