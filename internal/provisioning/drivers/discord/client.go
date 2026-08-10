// Package discord is HANGAR's Discord provisioning driver
// (01_ARCHITECTURE.md §9.3, Phase 12) — a hand-rolled HTTP client, not
// discordgo, because the bucket accounting, invalid-request budget, and
// Cloudflare 1015 detection all need raw response headers a general
// library abstracts away.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
)

// DefaultBaseURL is Discord's REST API root. Overridable (Config.BaseURL)
// so tests point the client at an httptest.Server.
const DefaultBaseURL = "https://discord.com/api"

// ErrInvalidBudgetPaused is returned by Do when the installation-wide
// invalid-request budget is paused (01_ARCHITECTURE.md §9.3) — the call is
// refused before any HTTP request is made, exactly like
// internal/esi.ErrErrorBudgetPaused's shape for Governor 2.
var ErrInvalidBudgetPaused = errors.New("discord: installation-wide invalid-request budget paused")

// Client is the low-level Discord REST transport: one Do() call per
// request, threading every response through the bucket limiter, the
// global ceiling, Cloudflare detection, and the invalid-request budget.
// driver.go's Grant/Revoke are its only real callers; hierarchy.go also
// calls it for the read-only endpoints the role-hierarchy guard needs.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	BotToken   string
	APIVersion int

	Buckets *BucketLimiter
	Global  *GlobalLimiter
	Budget  *InvalidBudget

	log *slog.Logger
}

// NewClient constructs a Client from cfg, validating cfg first (§9.3 /
// roadmap: "a version outside the allowlist fails at config validation,
// not at first request").
func NewClient(cfg Config, budget *InvalidBudget, clock Clock, log *slog.Logger) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		BotToken:   cfg.BotToken,
		APIVersion: cfg.APIVersion,
		Buckets:    NewBucketLimiter(clock),
		Global:     NewGlobalLimiter(cfg.GlobalRate, clock),
		Budget:     budget,
		log:        log.With("component", "provisioning.drivers.discord.client"),
	}, nil
}

// Result is one completed Discord API call's outcome. StatusCode/Body are
// always populated for a response that actually arrived — Do only returns
// a non-nil error for conditions that mean no meaningful response exists
// to interpret (budget paused, transport failure, Cloudflare ban). A
// normal 4XX/5XX from Discord itself is NOT an error at this layer —
// driver.go decides what a given status code means for Grant/Revoke.
type Result struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

// Do executes one Discord API call. route is the bucket-keying template
// (e.g. "PUT /guilds/{guild}/members/{user}/roles/{role}" — constant
// across calls that share a bucket regardless of the actual ids in path);
// path is the real request path (e.g. "/guilds/123/members/456/roles/789").
// body, if non-nil, is marshalled as the JSON request body.
func (c *Client) Do(ctx context.Context, method, route, path string, body any) (Result, error) {
	if c.Budget != nil {
		paused, err := c.Budget.IsPaused(ctx)
		if err != nil {
			return Result{}, err
		}
		if paused {
			return Result{}, ErrInvalidBudgetPaused
		}
	}
	if c.Buckets != nil {
		if err := c.Buckets.Reserve(ctx, route); err != nil {
			return Result{}, err
		}
	}
	if c.Global != nil {
		if err := c.Global.Wait(ctx); err != nil {
			return Result{}, err
		}
	}

	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return Result{}, fmt.Errorf("discord: encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}

	url := fmt.Sprintf("%s/v%d%s", c.BaseURL, c.APIVersion, path)
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return Result{}, fmt.Errorf("discord: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+c.BotToken)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("discord: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("discord: reading response body: %w", err)
	}

	c.recordRateLimitHeaders(route, resp)

	contentType := resp.Header.Get("Content-Type")
	if DetectCloudflareBan(resp.StatusCode, contentType, respBody) {
		return Result{}, &CloudflareBanError{StatusCode: resp.StatusCode}
	}

	scope := resp.Header.Get("X-RateLimit-Scope")
	if c.Budget != nil && ShouldCount(resp.StatusCode, scope) {
		if err := c.Budget.RecordInvalid(ctx); err != nil {
			return Result{}, err
		}
	}

	return Result{StatusCode: resp.StatusCode, Body: respBody, Header: resp.Header}, nil
}

// recordRateLimitHeaders updates the bucket limiter and, on a global
// signal, pauses the global limiter — §9.3: "X-RateLimit-Global and
// X-RateLimit-Scope: global mean the whole client pauses, not one bucket."
func (c *Client) recordRateLimitHeaders(route string, resp *http.Response) {
	bucketID := resp.Header.Get("X-RateLimit-Bucket")
	remaining, _ := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	resetAfter := parseResetAfter(resp.Header.Get("X-RateLimit-Reset-After"))
	if c.Buckets != nil {
		c.Buckets.Update(route, bucketID, remaining, resetAfter)
	}

	isGlobal := resp.Header.Get("X-RateLimit-Global") == "true" || resp.Header.Get("X-RateLimit-Scope") == "global"
	if isGlobal && c.Global != nil {
		retryAfter := parseResetAfter(resp.Header.Get("Retry-After"))
		if retryAfter == 0 {
			retryAfter = resetAfter
		}
		c.Global.Pause(secondsToDuration(retryAfter))
	}
}
