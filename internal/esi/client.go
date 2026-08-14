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

	// EntityBreaker is §5.8's 403 breaker: >=5 consecutive 403s for the
	// same (route, entity) stops calling that pair until a half-open probe
	// at the route TTL. nil disables it; a Request with EntityID == 0
	// (global routes, which have no owner) never consults it.
	//
	// ── B28: WHY THIS SITS BESIDE THE RE-ELECTION PATH, NOT INSTEAD OF IT ──
	// internal/sync/worker already records a 403 against
	// app.sync_acting_character_history and re-elects on the next attempt.
	// That mechanism answers WHICH character acts. This one answers WHETHER
	// to call at all: after five consecutive 403s the acting-character pool
	// has demonstrably been exhausted (each attempt elects afresh, and the
	// elector orders by fewest recent 403s, so five attempts have already
	// walked the viable candidates), and continuing to call spends Governor
	// 2's installation-wide error budget on a request that cannot succeed.
	//
	// The two never disagree because they never decide the same thing, and
	// they share one reset: a 2XX/3XX closes the entity circuit AND clears
	// that character's 403 history, so one success by any character re-opens
	// the route for the entity.
	EntityBreaker *breaker.EntityBreaker

	// Metrics receives Gate 1's counter signals (§1.2, §1.3). nil disables
	// them entirely — every call site is nil-guarded — which is what a unit
	// test constructing a bare Client gets.
	Metrics Observer

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

	// CompatibilityPin resolves the installation's current ESI
	// compatibility date ("YYYY-MM-DD") for the cache key. nil means "not
	// pinned", which keys every entry under the empty string — correct for
	// a unit test, and never the case in a real gateway.
	//
	// ── PHASE 20.3: THE CACHE KEY OMITTED THE COMPATIBILITY DATE ─────────
	// 01_ARCHITECTURE.md §5.3's key formula is
	//   sha256(method ‖ path ‖ query ‖ COMPATIBILITY_DATE ‖ tenant ‖
	//          language ‖ token_subject)
	// and cache.KeyInput has declared the field since Phase 3. cacheKey
	// never populated it. Every cached body was therefore keyed as though
	// the pin did not exist, and advancing the pin — the one operation the
	// pin exists for, because it changes what ESI returns — invalidated
	// NOTHING. An installation that advanced its pin kept serving bodies
	// fetched under the old one until each aged out on its own TTL.
	//
	// It is a function rather than a string because the pin is stored in
	// app.setting and an operator can advance it on a running installation
	// (the pin-advance flow has its own confirmation dialog and its own
	// e2e spec). A value captured at gateway construction would go stale at
	// the exact moment it matters most.
	//
	// A resolution failure is treated as "" rather than failing the
	// request. That is deliberate and it is the conservative direction: an
	// unreadable pin means the key falls back to the shape it had before
	// this fix, so a request still succeeds and still caches. Refusing to
	// serve because app.setting could not be read would turn a degraded
	// cache into an outage.
	CompatibilityPin func() (string, error)
}

// ErrBreakerOpen is returned when RouteBreaker.Allow refuses the call.
var ErrBreakerOpen = errors.New("esi: circuit breaker open for route")

// ErrEntityBreakerOpen is returned when EntityBreaker.Allow refuses the
// call for this (route, entity) pair — §5.8's 403 breaker. Distinct from
// ErrBreakerOpen because the caller's response differs: a route breaker
// means ESI is unwell, an entity breaker means THIS owner's authorisation
// is, and only the latter is worth surfacing to an operator as "we cannot
// read this corporation".
var ErrEntityBreakerOpen = errors.New("esi: entity circuit breaker open (5 consecutive 403s)")

// ErrErrorBudgetPaused is returned when Governor 2 has paused the
// installation (§5.7).
var ErrErrorBudgetPaused = errors.New("esi: installation-wide error budget paused")

// Observer receives the Gate 1 counter signals Do produces. It is an
// interface rather than a direct dependency on internal/telemetry so this
// package keeps no Prometheus import and a test can count calls with a
// three-line double.
type Observer interface {
	// Observe429 counts one 429, split by whether it carried rate-limit
	// headers at all (04_RELEASE_GATES.md §1.3's headerless-429 row).
	// group is app.esi_route.rate_limit_group, which is bounded by the
	// catalogue and therefore safe as a label; a character id never is.
	Observe429(group string, hasHeaders bool)
	// Observe420 counts one observed 420 from ESI — Gate 1.2's pass
	// condition is that this stays at zero for the whole run.
	Observe420()
}

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
	//
	// RateLimitMax is the route's REAL, advertised ceiling and must never
	// carry a call-site reduction: it is what gets persisted as the
	// bucket's max_tokens and what Ledger.Reconcile measures the server's
	// X-Ratelimit-Remaining against. A reduction goes in
	// RateLimitAdmissionMax below.
	RateLimitGroup  string
	RateLimitMax    int
	RateLimitWindow time.Duration

	// RateLimitAdmissionMax is the ceiling THIS call may admit against
	// when the caller holds part of the bucket back for somebody else —
	// internal/sync/worker.BackgroundRateLimitMax keeps five
	// char-notification tokens for interactive callers (§4.4). Zero means
	// "no reduction", which is correct for every other caller.
	//
	// PHASE 20.3 replaced the previous RateLimitRealMax field, which said
	// the same thing the other way round: RateLimitMax carried the
	// REDUCED value and RateLimitRealMax the true one. That shape put the
	// fiction on the field the ledger persists, so the reduction was
	// written to app.esi_ledger_bucket.max_tokens and Gate 1.3 read a
	// permanent divergence of 5 on char-notification. Defaulting the
	// truth and making the policy the opt-in also means a caller that
	// forgets this field loses a reserve, where a caller that forgot the
	// old one corrupted the ledger.
	RateLimitAdmissionMax int

	// EntityID is the owner this call is made on behalf of — the character
	// id, the corporation id, or 0 for a global/unowned route. It keys
	// §5.8's entity-scoped 403 breaker and nothing else; it never reaches
	// the wire.
	EntityID int64
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

	// SnoozeFor is how long the CALLER's subscription must be snoozed
	// because of this response: Retry-After's value on a 429 that carries
	// one, otherwise Client.TTLFloor. Zero on every non-429.
	//
	// It is returned rather than acted on here because internal/esi has no
	// idea what a subscription is — §5.5's "snooze the affected
	// subscription only, siblings unaffected" is a statement about
	// app.sync_subscription rows, which only internal/sync/worker can honour.
	SnoozeFor time.Duration

	// Is429Headerless marks a 429 that arrived with no X-Ratelimit-*
	// headers at all (CCP's in-monolith limiters do this). Informational
	// for the caller's logging; the counter is already incremented here.
	Is429Headerless bool
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
	if c.EntityBreaker != nil && req.EntityID != 0 && !c.EntityBreaker.Allow(req.UpstreamPath, req.EntityID) {
		return nil, fmt.Errorf("%w: %s for entity %d", ErrEntityBreakerOpen, req.UpstreamPath, req.EntityID)
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
			MaxTokens:          req.RateLimitMax,
			AdmissionMaxTokens: req.RateLimitAdmissionMax,
			Window:             req.RateLimitWindow,
			RequestTimeout:     c.requestTimeout(),
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
	// PHASE 20.2 (B23). The resolved language was already part of the cache
	// key (§5.3) and was never actually SENT — so before internal/i18n was
	// wired the field was empty and the omission was invisible, and the
	// moment it was populated the cache would have keyed on a language the
	// request never asked for. Set together, or neither.
	if c.Language != "" {
		httpReq.Header.Set("Accept-Language", c.Language)
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

	// ── B29: ONE CLASSIFICATION POINT ────────────────────────────────────
	// This used to be a bare ClassifyCost, and everything else in §5.5 that
	// depends on the response's headers — reconciliation, the 429 snooze,
	// the headerless-429 signal — simply did not happen. ClassifyResponse
	// is the whole rule in one place; every branch below reads its Outcome
	// rather than re-deriving anything from `status`.
	outcome := ratelimit.ClassifyResponse(status, header, sendErr != nil, c.TTLFloor)

	if reservation != nil {
		if settleErr := c.Ledger.Settle(ctx, reservation, outcome.Cost, respondedAt); settleErr != nil {
			// Settlement failure must not mask the real response — log
			// via the caller (no logger on Client to keep it dependency-
			// light); surfaced through the returned error only if the
			// request itself also failed.
			if sendErr != nil {
				sendErr = errors.Join(sendErr, settleErr)
			}
		}
		// Reconciliation runs AFTER settle, deliberately: the server's
		// reading already accounts for the request we just made, so
		// converging against it before this request's own cost is in the
		// ledger would inject a synthetic entry covering a cost that is
		// about to be added again.
		c.reconcile(ctx, req, outcome)
	}

	if sendErr != nil {
		if c.RouteBreaker != nil {
			c.RouteBreaker.RecordFailure(req.UpstreamPath)
		}
		c.recordErrorBudget(ctx, outcome, status)
		return nil, fmt.Errorf("esi: request failed: %w", sendErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if status >= 500 && status < 600 {
		if c.RouteBreaker != nil {
			c.RouteBreaker.RecordFailure(req.UpstreamPath)
		}
	} else if c.RouteBreaker != nil {
		c.RouteBreaker.RecordSuccess(req.UpstreamPath)
	}
	c.recordErrorBudget(ctx, outcome, status)

	// §5.8's entity breaker, and only for the two statuses that say
	// anything about THIS owner's authorisation. Every other status (a
	// 404, a 5XX, a 429) is silent about it and must leave the circuit
	// exactly as it was — counting a 5XX here would open an entity's
	// breaker for an outage that has nothing to do with its roles.
	if c.EntityBreaker != nil && req.EntityID != 0 {
		switch {
		case status == http.StatusForbidden:
			c.EntityBreaker.RecordFailure(req.UpstreamPath, req.EntityID)
		case status >= 200 && status < 400:
			c.EntityBreaker.RecordSuccess(req.UpstreamPath, req.EntityID)
		}
	}

	if c.Metrics != nil {
		if status == http.StatusTooManyRequests {
			c.Metrics.Observe429(req.RateLimitGroup, !outcome.Is429Headerless)
		}
		if status == statusErrorLimited {
			c.Metrics.Observe420()
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
		Pages:     parsePages(header.Get("X-Pages")),
		SnoozeFor: outcome.SnoozeFor, Is429Headerless: outcome.Is429Headerless,
	}, nil
}

// statusErrorLimited is ESI's 420 "error limited" status. Not in net/http's
// constant set (it is not an IANA-registered code), so it is named here
// rather than left as a bare literal at the two places that test for it.
const statusErrorLimited = 420

// recordErrorBudget charges one non-2XX/3XX outcome against Governor 2's
// installation-wide window (§5.7). The classification of what counts is
// ClassifyResponse's, not a second copy of the rule here — that duplication
// is precisely how the transport-error branch and the response branch used
// to disagree about whether a 420 was possible.
func (c *Client) recordErrorBudget(ctx context.Context, outcome ratelimit.Outcome, status int) {
	if c.ErrorBudget == nil || !outcome.IsErrorForGovernor2 {
		return
	}
	_ = c.ErrorBudget.RecordError(ctx, status == statusErrorLimited)
}

// reconcile applies §5.5's "the server always wins" against the bucket this
// request was admitted from.
//
// It is a no-op unless the response actually carried X-Ratelimit-Remaining:
// an absent header is NOT a reading of zero (that would stall the
// installation on every headerless 429 — §5.5's own edge case), and a
// bucket the server has said nothing about must keep the local view rather
// than converge on a fiction.
//
// The ceiling is the server's own X-Ratelimit-Limit when it sent one, and
// the route's catalogue value otherwise — Request.RateLimitMax, which since
// Phase 20.3 is the real ceiling for every caller (a call-site reserve
// lives in RateLimitAdmissionMax and never reaches this function or the
// bucket row). See internal/sync/worker/reserve.go.
func (c *Client) reconcile(ctx context.Context, req Request, outcome ratelimit.Outcome) {
	if c.Ledger == nil || req.RateLimitGroup == "" || !outcome.ServerRemainingOK {
		return
	}
	maxTokens := req.RateLimitMax
	if outcome.ServerLimitOK {
		maxTokens = outcome.ServerMaxTokens
	}
	if maxTokens <= 0 {
		return
	}
	// A reconciliation failure is not the caller's problem: the response is
	// already in hand and returning an error here would turn a bookkeeping
	// hiccup into a failed sync. The divergence it leaves behind is exactly
	// what esi_ledger_divergence exists to show.
	_ = c.Ledger.Reconcile(ctx, req.RateLimitGroup, req.UserKey, maxTokens, outcome.ServerRemaining)
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
		CompatibilityDate: c.compatibilityDate(),
		Tenant:            c.Tenant, ResolvedLanguage: c.Language,
		TokenSubject: tokenSubject(req.AccessToken),
	})
}

// compatibilityDate resolves the pin for the cache key, or "" — see
// Client.CompatibilityPin for why a failure degrades rather than errors.
func (c *Client) compatibilityDate() string {
	if c.CompatibilityPin == nil {
		return ""
	}
	pin, err := c.CompatibilityPin()
	if err != nil {
		return ""
	}
	return pin
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
