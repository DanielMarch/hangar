package teamspeak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ErrTS3 wraps a TS3 ServerQuery error response — TS3's own error
// signal, carried inside a normal HTTP 200 (01_ARCHITECTURE.md §9.4:
// "TS3 reports errors inside HTTP 200 responses — the body, not the
// status, is the error signal"). ID/Msg are TS3's own vocabulary
// (Principle 14 — open, never validated against a closed Go set).
type ErrTS3 struct {
	ID  int
	Msg string
}

func (e *ErrTS3) Error() string {
	return fmt.Sprintf("teamspeak: error id=%d msg=%q", e.ID, e.Msg)
}

// Known TS3 ServerQuery error ids this driver treats as idempotent
// successes for Grant/Revoke (provisioning.Driver's idempotency
// contract). Best-effort against TS3's own documented error id list —
// not independently verified against a live server in this environment
// (reported per this phase's own "surface uncertainty" instruction; an
// integrator pointing this driver at a real TS3 instance should confirm
// these against that server's actual responses before relying on them).
const (
	ts3ErrDatabaseEmptyResult    = 1281 // e.g. removing a client that isn't in the group
	ts3ErrDatabaseDuplicateEntry = 2560 // e.g. adding a client already in the group
)

// envelope is TS3 WebQuery's response shape:
// {"status":{"code":0,"message":"ok"},"body":[...]}. body's shape varies
// per command, so it's decoded lazily by each caller.
type envelope struct {
	Status struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	Body json.RawMessage `json:"body"`
}

// Client is the TS3 WebQuery HTTP transport.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string // e.g. http://teamspeak:10080
	APIKey     string
	ServerID   int
}

// NewClient constructs a Client. httpClient may be nil (defaults to
// http.DefaultClient) — tests inject one pointed at an httptest.Server.
func NewClient(baseURL, apiKey string, serverID int, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{HTTPClient: httpClient, BaseURL: baseURL, APIKey: apiKey, ServerID: serverID}
}

// Do issues one TS3 WebQuery command against the configured virtual
// server. Every string value in params is TS3-escaped (escape.go) before
// being placed in the query string — the query string's own percent-
// encoding (net/url) is a separate, orthogonal transport-level concern
// applied on top, never a substitute for TS3's protocol-level escaping.
//
// A non-zero status.code — even though the HTTP status is always 200 for
// a well-formed WebQuery request — is returned as *ErrTS3, never treated
// as success. Do never inspects resp.StatusCode to decide success/failure
// for exactly this reason: the body's status.code is the sole signal.
func (c *Client) Do(ctx context.Context, command string, params map[string]string) (json.RawMessage, error) {
	q := url.Values{}
	for k, v := range params {
		q.Set(k, Escape(v))
	}
	reqURL := fmt.Sprintf("%s/%d/%s", c.BaseURL, c.ServerID, command)
	if len(q) > 0 {
		reqURL += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("teamspeak: building request for %s: %w", command, err)
	}
	req.Header.Set("x-api-key", c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("teamspeak: request %s failed: %w", command, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("teamspeak: reading response body for %s: %w", command, err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("teamspeak: decoding WebQuery envelope for %s (http status %d): %w", command, resp.StatusCode, err)
	}
	if env.Status.Code != 0 {
		return nil, &ErrTS3{ID: env.Status.Code, Msg: env.Status.Message}
	}
	return env.Body, nil
}

// isIdempotentGrantError reports whether err is a TS3 error this driver
// treats as "already in the desired state" for Grant.
func isIdempotentGrantError(err error) bool {
	var tsErr *ErrTS3
	return errors.As(err, &tsErr) && tsErr.ID == ts3ErrDatabaseDuplicateEntry
}

// isIdempotentRevokeError reports whether err is a TS3 error this driver
// treats as "already in the desired state" for Revoke.
func isIdempotentRevokeError(err error) bool {
	var tsErr *ErrTS3
	return errors.As(err, &tsErr) && tsErr.ID == ts3ErrDatabaseEmptyResult
}
