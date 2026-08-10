// Package esi is the top-level ESI gateway facade: one Do() call that
// assembles Phases 2-4's tested, standalone pieces — internal/esi/transport
// (UA, X-Compatibility-Date, retry), internal/esi/cache (L1/L2 conditional
// cache), internal/esi/ratelimit (Governor 1/2), internal/esi/breaker
// (route circuit breaker) — into the single outbound call a route handler
// needs. None of those phases built this assembly themselves; each shipped
// its own tested unit. Phase 7, their first real consumer, is where they
// are wired together.
package esi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hangar-project/hangar/internal/esi/breaker"
	"github.com/hangar-project/hangar/internal/esi/cache"
	"github.com/hangar-project/hangar/internal/esi/ratelimit"
	"github.com/hangar-project/hangar/internal/esi/transport"
)

// Client is the gateway facade. Every field but HTTPClient and Cache is
// nil-safe: a Client built for a parsing-only unit test can leave
// RouteBreaker/Ledger/ErrorBudget nil and Do behaves as if none of those
// governors exist (useful for tests that only care about request/response
// shape, not resilience behaviour — the resilience pieces have their own
// exhaustive Phase 4 test suites already).
type Client struct {
	HTTPClient *http.Client
	BaseURL    string // e.g. catalogue.EsiBaseURL
	Cache      *cache.Store

	RouteBreaker *breaker.RouteBreaker // nil disables the breaker check entirely
	Ledger       ratelimit.Ledger      // nil disables Governor 1 (rate limiting)
	ErrorBudget  *ratelimit.Governor2  // nil disables Governor 2 (error-limit pause)

	// TTLFloor feeds ratelimit.ClassifyResponse's headerless-429 snooze
	// fallback (internal/config.ESIConfig.TTLFloor).
	TTLFloor time.Duration
	// Language is the resolved ESI Accept-Language value (internal/i18n),
	// part of the cache key per §5.3. Empty is a valid "not resolved yet"
	// value for callers that don't localise (Phase 7 doesn't).
	Language string
	// Tenant is the cache key's installation-scoping field (§5.3) — a
	// single-tenant HANGAR install uses a fixed value.
	Tenant string
}

// ErrBreakerOpen is returned when RouteBreaker.Allow refuses the call.
var ErrBreakerOpen = errors.New("esi: circuit breaker open for route")

// ErrErrorBudgetPaused is returned when Governor 2 has paused the
// installation (§5.7).
var ErrErrorBudgetPaused = errors.New("esi: installation-wide error budget paused")

// Request describes one ESI call. UpstreamPath is app.esi_route's column,
// verbatim, with `{param}` placeholders — never derived or pluralised
// (Principle 5).
type Request struct {
	Method       string
	UpstreamPath string
	PathParams   map[string]string
	Query        url.Values

	// AccessToken is the bearer token for an authenticated route; empty
	// for the handful of public routes (e.g. corporationhistory).
	AccessToken string

	// CacheMode is app.esi_route.cache_mode's raw value ("" = absent).
	CacheMode string

	// RateLimitGroup/Max/Window are app.esi_route's Governor 1 bucket
	// config; RateLimitGroup == "" disables Governor 1 for this call
	// (only the discovery fetch and a handful of unthrottled routes).
	RateLimitGroup  string
	RateLimitMax    int
	RateLimitWindow time.Duration
	// UserKey is the Governor 1 bucket's user dimension —
	// "applicationID:characterID" for an authenticated call (§5.5).
	UserKey string

	// Validators are the caller's stored ETag/Last-Modified for this
	// exact request (from app.sync_subscription), used to build
	// conditional headers. nil means "no prior state" — always a full
	// fetch, never conditional.
	Validators *cache.Validators
}

// Response is what Do returns. NotModified distinguishes a 304 (Body is
// the cache's stored body, replayed) from a fresh 200. IsDataNotFound
// marks the one edge case §01_ARCHITECTURE documents explicitly (a 404 on
// a route where that's a legitimate data state, not a failure) — Do
// itself doesn't know which routes those are; RecordFailure is simply
// never called for any 4xx (see the doc comment on Do), so this field is
// informational for the caller, not load-bearing for the breaker decision.
type Response struct {
	StatusCode      int
	Body            []byte
	ETag            string
	LastModified    time.Time
	HasLastModified bool
	NotModified     bool
	FromCache       bool

	// Pages is the parsed X-Pages header (§5.9's page-pagination mechanism:
	// "page" + "X-Pages") — 0 when the header is absent, which callers must
	// treat as "not page-paginated" or "only one page", never as a literal
	// page count of zero. Phase 7's callers never needed this (character
	// routes in that phase's scope are all single-page); Phase 8's wallet
	// journal is the first consumer.
	Pages int
}

// buildURL substitutes {name} placeholders in path with PathParams,
// URL-escaping each value, and appends Query. upstream_path is used
// verbatim otherwise — Principle 5.
func (c *Client) buildURL(req Request) (string, error) {
	path := req.UpstreamPath
	for name, value := range req.PathParams {
		placeholder := "{" + name + "}"
		if !strings.Contains(path, placeholder) {
			continue
		}
		path = strings.ReplaceAll(path, placeholder, url.PathEscape(value))
	}
	if strings.Contains(path, "{") {
		return "", fmt.Errorf("esi: unresolved path placeholder in %q after substitution", req.UpstreamPath)
	}
	u, err := url.Parse(c.BaseURL + path)
	if err != nil {
		return "", fmt.Errorf("esi: building request URL: %w", err)
	}
	if len(req.Query) > 0 {
		u.RawQuery = req.Query.Encode()
	}
	return u.String(), nil
}

// Do sends one ESI request through the full gateway pipeline: breaker
// check, error-budget check, Governor 1 reservation, conditional cache
// headers, transport (UA/compat-date/retry already applied by
// HTTPClient's RoundTripper — see NewHTTPClient), response classification,
// cache write, and settlement.
//
// Breaker behaviour: RecordFailure is called ONLY for a 5xx response or a
// transport-level failure (no response at all). Every response that DOES
// arrive — including a 404 — is a RecordSuccess as far as the breaker is
// concerned: "the server answered" is what the breaker tracks, and a 404
// is data for many routes (character/{id}/ship on a docked character is
// the roadmap's own example), not evidence ESI itself is unhealthy. A
// route-specific "is this 404 actually an error" distinction, if one is
// ever needed, belongs to the caller inspecting Response.StatusCode, never
// to this shared breaker decision.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	if c.RouteBreaker != nil && !c.RouteBreaker.Allow(req.UpstreamPath) {
		return nil, fmt.Errorf("%w: %s", ErrBreakerOpen, req.UpstreamPath)
	}
	if c.ErrorBudget != nil {
		paused, err := c.ErrorBudget.IsPaused(ctx)
		if err != nil {
			return nil, fmt.Errorf("esi: checking error budget: %w", err)
		}
		if paused {
			return nil, ErrErrorBudgetPaused
		}
	}

	var reservation *ratelimit.Reservation
	if c.Ledger != nil && req.RateLimitGroup != "" {
		res, err := c.Ledger.Acquire(ctx, ratelimit.AcquireRequest{
			Group: req.RateLimitGroup, UserKey: req.UserKey,
			MaxTokens: req.RateLimitMax, Window: req.RateLimitWindow,
			RequestTimeout: c.requestTimeout(),
		})
		if err != nil {
			return nil, err // *ratelimit.RetryAtError on insufficient budget
		}
		reservation = res
	}

	rawURL, err := c.buildURL(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("esi: building http request: %w", err)
	}
	if req.AccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	}
	condSent := cache.ApplyConditionalHeaders(httpReq, req.Validators, req.CacheMode)

	resp, sendErr := c.HTTPClient.Do(httpReq)

	respondedAt := time.Now()
	status := 0
	var header http.Header
	if sendErr == nil {
		status = resp.StatusCode
		header = resp.Header
	}

	if reservation != nil {
		cost := ratelimit.ClassifyCost(status, sendErr != nil)
		if settleErr := c.Ledger.Settle(ctx, reservation, cost, respondedAt); settleErr != nil {
			// Settlement failure must not mask the real response — log
			// via the caller (no logger on Client to keep it dependency-
			// light); surfaced through the returned error only if the
			// request itself also failed.
			if sendErr != nil {
				sendErr = errors.Join(sendErr, settleErr)
			}
		}
	}

	if sendErr != nil {
		if c.RouteBreaker != nil {
			c.RouteBreaker.RecordFailure(req.UpstreamPath)
		}
		if c.ErrorBudget != nil {
			_ = c.ErrorBudget.RecordError(ctx, false)
		}
		return nil, fmt.Errorf("esi: request failed: %w", sendErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if status >= 500 && status < 600 {
		if c.RouteBreaker != nil {
			c.RouteBreaker.RecordFailure(req.UpstreamPath)
		}
		if c.ErrorBudget != nil {
			_ = c.ErrorBudget.RecordError(ctx, status == 420)
		}
	} else {
		if c.RouteBreaker != nil {
			c.RouteBreaker.RecordSuccess(req.UpstreamPath)
		}
		if status >= 400 && c.ErrorBudget != nil {
			_ = c.ErrorBudget.RecordError(ctx, status == 420)
		}
	}

	if status == http.StatusNotModified && condSent {
		body, etag, lastMod, hasLastMod, ok := c.cachedBody(ctx, req)
		if ok {
			return &Response{StatusCode: status, Body: body, ETag: etag, LastModified: lastMod, HasLastModified: hasLastMod, NotModified: true, FromCache: true}, nil
		}
		// No cache entry to replay (evicted, or L2 absent) — fall through
		// and return the 304 with an empty body; the caller's next full
		// sync cycle will re-fetch when the ETag inevitably misses again.
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		if c.RouteBreaker != nil {
			c.RouteBreaker.RecordFailure(req.UpstreamPath)
		}
		return nil, fmt.Errorf("esi: reading response body: %w", readErr)
	}

	etag := header.Get("ETag")
	lastMod, hasLastMod := parseLastModified(header.Get("Last-Modified"))

	if status == http.StatusOK && c.Cache != nil {
		key := c.cacheKey(req)
		c.Cache.Set(ctx, key, cache.Entry{
			ETag: etag, LastModified: lastMod, HasLastModified: hasLastMod,
			Body: body, Status: status, ExpiresAt: respondedAt.Add(cacheEntryTTL),
		}, req.CacheMode)
	}

	return &Response{
		StatusCode: status, Body: body, ETag: etag,
		LastModified: lastMod, HasLastModified: hasLastMod,
		Pages: parsePages(header.Get("X-Pages")),
	}, nil
}

// parsePages parses the X-Pages header (§5.9). An absent or malformed
// header yields 0 — "not page-paginated", not a page count.
func parsePages(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (c *Client) cacheKey(req Request) string {
	return cache.Key(cache.KeyInput{
		Method: req.Method, Path: req.UpstreamPath, Query: req.Query,
		Tenant: c.Tenant, ResolvedLanguage: c.Language,
		TokenSubject: tokenSubject(req.AccessToken),
	})
}

func (c *Client) cachedBody(ctx context.Context, req Request) (body []byte, etag string, lastMod time.Time, hasLastMod bool, ok bool) {
	if c.Cache == nil {
		return nil, "", time.Time{}, false, false
	}
	e, found := c.Cache.Get(ctx, c.cacheKey(req), req.CacheMode)
	if !found {
		return nil, "", time.Time{}, false, false
	}
	return e.Body, e.ETag, e.LastModified, e.HasLastModified, true
}

// cacheEntryTTL is a conservative fixed L1/L2 entry lifetime — the
// scheduling TTL (when the planner asks again) is a completely separate
// concern (internal/sync.PlanNextDueAt); this only bounds how long a body
// may be replayed on a 304 before it is evicted regardless of whether
// anything ever asks again.
const cacheEntryTTL = 24 * time.Hour

func (c *Client) requestTimeout() time.Duration {
	if c.HTTPClient != nil && c.HTTPClient.Timeout > 0 {
		return c.HTTPClient.Timeout
	}
	return 30 * time.Second
}

// tokenSubject renders the cache key's token-subject field. It never
// decodes the token — the raw bearer string is subject-unique enough for
// cache partitioning, and decoding would require a JWKS round trip this
// package has no business making.
func tokenSubject(accessToken string) string {
	if accessToken == "" {
		return "anonymous"
	}
	return accessToken
}

func parseLastModified(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	t, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// NewHTTPClient builds the http.Client Client.HTTPClient expects: the full
// transport chain (UA, X-Compatibility-Date, retry) from
// internal/esi/transport, bounded by timeout.
func NewHTTPClient(opts transport.Options, timeout time.Duration) *http.Client {
	return &http.Client{Transport: transport.New(opts), Timeout: timeout}
}
