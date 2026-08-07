package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCompatDateTransportSetsPinHeader(t *testing.T) {
	var gotHeader string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		gotHeader = req.Header.Get("X-Compatibility-Date")
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
	})

	rt := WithCompatibilityDate(base, func() (string, error) { return "2026-08-04", nil })
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "2026-08-04" {
		t.Errorf("X-Compatibility-Date = %q, want 2026-08-04", gotHeader)
	}
	// The original request must never be mutated (net/http's RoundTripper contract).
	if req.Header.Get("X-Compatibility-Date") != "" {
		t.Error("the original *http.Request must not be mutated by the transport")
	}
}

func TestCompatDateTransportPanicsOnEmptyPin(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic when the pin resolves to the empty string")
		}
	}()
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatal("must never reach the inner transport with no pin")
		return nil, nil
	})
	rt := WithCompatibilityDate(base, func() (string, error) { return "", nil })
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	_, _ = rt.RoundTrip(req)
}

func TestUserAgentTransportSetsHeader(t *testing.T) {
	var gotUA string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		gotUA = req.Header.Get("User-Agent")
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
	})
	rt := New(Options{
		Version:       "1.2.3",
		ContactURL:    "https://example.org",
		Pin:           func() (string, error) { return "2026-08-04", nil },
		BaseTransport: base,
		Retry:         RetryConfig{MaxAttempts: 1},
	})
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	want := "HANGAR/1.2.3 (+https://example.org)"
	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}

func TestRetryRetriesTransportErrorsAndFiveXX(t *testing.T) {
	var attempts atomic.Int32
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		n := attempts.Add(1)
		if n < 3 {
			return nil, errors.New("connection reset")
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
	})
	rt := WithRetry(base, RetryConfig{MaxAttempts: 3, BaseDelay: 0, MaxDelay: 0})
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 after retries succeeded", resp.StatusCode)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestRetryNeverRetries429(t *testing.T) {
	var attempts atomic.Int32
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return &http.Response{StatusCode: 429, Body: http.NoBody, Header: http.Header{}}, nil
	})
	rt := WithRetry(base, RetryConfig{MaxAttempts: 3, BaseDelay: 0, MaxDelay: 0})
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 429 {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want exactly 1 — 429 is Governor 1's signal (Phase 4), never retried here", attempts.Load())
	}
}

func TestRetryNeverRetriesOther4xx(t *testing.T) {
	var attempts atomic.Int32
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return &http.Response{StatusCode: 404, Body: http.NoBody, Header: http.Header{}}, nil
	})
	rt := WithRetry(base, RetryConfig{MaxAttempts: 3, BaseDelay: 0, MaxDelay: 0})
	req := httptest.NewRequest(http.MethodGet, "https://esi.evetech.net/x", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want exactly 1 for a 404", attempts.Load())
	}
}

func TestBuildUserAgentDefaults(t *testing.T) {
	if got := BuildUserAgent("", ""); got != "HANGAR/dev (+no-contact-configured)" {
		t.Errorf("BuildUserAgent(\"\", \"\") = %q", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
