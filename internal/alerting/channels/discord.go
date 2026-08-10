package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hangar-project/hangar/internal/alerting/render"
)

// DiscordContentLimit is the payload limit this channel truncates
// against: 2,000 characters for a webhook message's `content` field.
//
// Tighter than Slack's 3,000 (SlackSectionTextLimit) — §4.4's "Slack and
// Discord webhook payloads have different size limits" is exactly this
// pair, and it is why each channel truncates for itself rather than the
// dispatcher pre-rendering one body for all of them.
const DiscordContentLimit = 2000

// DiscordWebhook posts to a Discord webhook URL.
//
// NOT to be confused with internal/provisioning/drivers/discord (Phase
// 12's bot-token role-provisioning driver) — see this package's doc
// comment. A webhook post carries no bot token, is not accounted against
// the bot's rate-limit buckets or its invalid-request budget, and its 404
// means "webhook deleted", not "member not found". The two share nothing
// but a hostname, and deliberately so.
type DiscordWebhook struct {
	// URL is the full webhook URL including its token. A credential;
	// never logged by this type.
	URL string
	// Username overrides the webhook's display name when set.
	Username string
	// HTTPClient defaults to a client with Timeout below when nil.
	HTTPClient *http.Client
	// Timeout bounds one delivery attempt when HTTPClient is nil.
	Timeout time.Duration
}

var _ Channel = (*DiscordWebhook)(nil)

// Kind implements Channel.
func (d *DiscordWebhook) Kind() string { return KindDiscordWebhook }

// Send implements Channel.
func (d *DiscordWebhook) Send(ctx context.Context, msg Message) error {
	if d.URL == "" {
		return &PermanentError{Reason: "discord: no webhook URL configured"}
	}

	content := render.Rollup(msg.Header, msg.Lines, DiscordContentLimit-mentionRoom(msg.Mention))
	if msg.Mention != "" {
		content = msg.Mention + "\n" + content
	}

	payload := discordPayload{Content: content, Username: d.Username}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return &PermanentError{Reason: "discord: encoding payload", Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(encoded))
	if err != nil {
		return &PermanentError{Reason: "discord: building request", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client().Do(req)
	if err != nil {
		// The URL carries the webhook token; net/http puts it in the
		// error text verbatim (see channels.scrubURL).
		return scrubURL(d.URL, fmt.Errorf("discord: posting to webhook: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		// 429 here is the webhook's own per-route limit, honoured by
		// retrying with the dispatcher's backoff rather than by sleeping
		// inside the send: a delivery worker that sleeps holds a claimed
		// row and blocks the queue, which §4.4 forbids ("never block the
		// queue"). Retry-After is not parsed for that same reason — the
		// backoff schedule already spaces attempts out.
		return fmt.Errorf("discord: webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	default:
		// 400 (malformed/oversized), 401/403 (bad token), 404 (webhook
		// deleted). None is fixable by retrying.
		return &PermanentError{Reason: fmt.Sprintf("discord: webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}
}

func (d *DiscordWebhook) client() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

type discordPayload struct {
	Content  string `json:"content"`
	Username string `json:"username,omitempty"`
}
