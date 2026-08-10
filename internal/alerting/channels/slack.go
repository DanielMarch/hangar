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

// SlackSectionTextLimit is the payload limit this channel truncates
// against: 3,000 characters.
//
// It is the limit on a `section` block's text object, which is what this
// channel posts — NOT the 40,000-character limit on a top-level `text`
// field. The distinction is the whole reason §4.4 says Slack and Discord
// "have different size limits": posting blocks (which is what gets a
// readable, mention-capable message rather than a wall of plain text)
// buys formatting at the cost of the tighter per-block cap. A 40-event
// roll-up will exceed it, and render.Rollup truncates with an explicit
// remainder count rather than failing the delivery.
const SlackSectionTextLimit = 3000

// SlackWebhook posts to a Slack incoming webhook
// (https://api.slack.com/messaging/webhooks). Hand-rolled over net/http
// for the same reason every other outbound client in this codebase is: the
// wire contract is one POST of one JSON object, and a library would add a
// dependency without removing any code.
type SlackWebhook struct {
	// URL is the incoming-webhook URL. It is a credential (anyone holding
	// it can post to the channel) and is never logged by this type.
	URL string
	// HTTPClient defaults to a client with Timeout below when nil.
	HTTPClient *http.Client
	// Timeout bounds one delivery attempt when HTTPClient is nil.
	Timeout time.Duration
}

var _ Channel = (*SlackWebhook)(nil)

// Kind implements Channel.
func (s *SlackWebhook) Kind() string { return KindSlackWebhook }

// Send implements Channel.
func (s *SlackWebhook) Send(ctx context.Context, msg Message) error {
	if s.URL == "" {
		return &PermanentError{Reason: "slack: no webhook URL configured"}
	}

	body := render.Rollup(msg.Header, msg.Lines, SlackSectionTextLimit-mentionRoom(msg.Mention))
	if msg.Mention != "" {
		// The mention leads the block so it is the first thing Slack's
		// notification preview shows. It is passed through verbatim —
		// mention syntax is an open vocabulary (Principle 14) and HANGAR
		// neither validates nor escapes it, since escaping would break the
		// very syntax an operator configured it for.
		body = msg.Mention + "\n" + body
	}

	payload := slackPayload{
		Text: msg.Subject, // fallback text for notifications and old clients
		Blocks: []slackBlock{{
			Type: "section",
			Text: &slackText{Type: "mrkdwn", Text: body},
		}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return &PermanentError{Reason: "slack: encoding payload", Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(encoded))
	if err != nil {
		return &PermanentError{Reason: "slack: building request", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client().Do(req)
	if err != nil {
		// Transport failure: DNS, connection refused, timeout. Transient
		// by default — retried with backoff. The URL is scrubbed from the
		// text because net/http embeds it verbatim and a Slack webhook URL
		// is a credential (see channels.scrubURL).
		return scrubURL(s.URL, fmt.Errorf("slack: posting to webhook: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		// Slack is rate-limiting or broken — retry.
		return fmt.Errorf("slack: webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	default:
		// 400 "invalid_payload", 403 "action_prohibited", 404 "no_service"
		// (the webhook was revoked). Retrying cannot fix any of these.
		return &PermanentError{Reason: fmt.Sprintf("slack: webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}
}

func (s *SlackWebhook) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

// mentionRoom reserves space for the mention prefix so adding it cannot
// push an already-truncated body back over the limit.
func mentionRoom(mention string) int {
	if mention == "" {
		return 0
	}
	return len([]rune(mention)) + 1 // + the newline
}

type slackPayload struct {
	Text   string       `json:"text,omitempty"`
	Blocks []slackBlock `json:"blocks,omitempty"`
}

type slackBlock struct {
	Type string     `json:"type"`
	Text *slackText `json:"text,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
