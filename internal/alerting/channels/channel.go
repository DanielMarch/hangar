// Package channels holds Phase 14's three delivery integrations: SMTP,
// Slack incoming webhooks, and Discord webhooks (§4.4 "Delivery
// Channels"; the roadmap's legacy reference calls them out as
// notifications.integrations.php's three).
//
// ── discord.go IN THIS PACKAGE IS NOT THE PROVISIONING DRIVER ───────────
// internal/alerting/channels/discord.go posts a message to a Discord
// WEBHOOK URL — an unauthenticated, single-purpose endpoint that accepts a
// JSON body and returns 204. internal/provisioning/drivers/discord (Phase
// 12) is an entirely separate concern: a bot-token REST client that grants
// and revokes guild roles, with per-bucket rate accounting, an
// invalid-request budget and Cloudflare-ban detection. Same company, same
// hostname, unrelated code paths and unrelated failure modes. They share
// no client, no config and no rate limiter, and must not be made to: a
// webhook post has no bot token, is not subject to the bot's buckets, and
// a 404 from it means "this webhook was deleted", not "this guild member
// is gone".
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Kind values match app.alert_channel.kind's CHECK constraint
// (migration 00008).
const (
	KindSMTP           = "smtp"
	KindSlackWebhook   = "slack_webhook"
	KindDiscordWebhook = "discord_webhook"
)

// Message is one rendered alert delivery, before any channel-specific
// formatting. It carries the roll-up in PIECES rather than as a finished
// string because each channel has a different size limit (§4.4) and must
// therefore do its own truncation — handing every channel the same
// pre-truncated body would mean truncating everything to the smallest
// channel's limit.
type Message struct {
	// AlertType is app.alert_type.alert_type, for logging and for a
	// channel that wants to tag the message.
	AlertType string
	// Subject is the one-line summary; the email Subject header, and the
	// first line of a chat message.
	Subject string
	// Header is the roll-up's first body line (render.Header) — the
	// summary plus the event count when more than one event coalesced.
	Header string
	// Lines is one rendered line per coalesced event, oldest first, so a
	// truncation drops the most recent tail rather than the first thing
	// that happened.
	Lines []string
	// Mention is app.alert_routing_rule.mention, verbatim: an open
	// vocabulary of platform-specific strings ("<!here>", "<@&123>",
	// an email address). HANGAR does not parse or validate it — each
	// channel decides where to put it, and an unrecognised value is
	// delivered as text rather than rejected (Principle 14).
	Mention string
	// Count is len(Lines) before any truncation — what "and N more"
	// counts against.
	Count int
}

// Channel is one configured delivery destination. Implementations must be
// safe for concurrent use: the outbox pump sends to several channels at
// once.
type Channel interface {
	// Kind returns one of the Kind* constants above.
	Kind() string
	// Send delivers msg. A returned error is retried with backoff and
	// eventually dead-lettered (see internal/alerting/deadletter.go)
	// unless it is a PermanentError, which dead-letters immediately.
	Send(ctx context.Context, msg Message) error
}

// PermanentError marks a failure that retrying cannot fix: a webhook URL
// that no longer exists, a payload the platform rejects as malformed, a
// rejected recipient address. §4.4 requires exhausted deliveries to
// dead-letter rather than be lost; this lets a delivery reach that visible
// outcome immediately instead of burning a retry budget re-proving the
// same 404 five times over twenty minutes.
//
// The default is deliberately the other way round: anything NOT explicitly
// classified as permanent is treated as transient and retried, because
// misclassifying a transient failure as permanent loses time-sensitive
// alerts, while misclassifying a permanent one merely wastes attempts.
type PermanentError struct {
	Reason string
	Err    error
}

func (e *PermanentError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Reason, e.Err)
	}
	return e.Reason
}

func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err is (or wraps) a PermanentError.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// redactedWebhookURL is what replaces a webhook URL in any error text.
const redactedWebhookURL = "<webhook url redacted>"

// scrubURL removes a webhook URL from an error's text.
//
// A WEBHOOK URL IS A CREDENTIAL: it carries its own token, and anyone
// holding it can post to the channel. net/http puts the full request URL
// into every transport error ("Post \"https://hooks.slack.com/services/
// T00/B00/XXXX\": dial tcp ..."), and this phase's error text goes two
// places that outlive the request — app.alert_delivery.error, which the
// admin dead-letter board renders, and the process log. Without this, one
// unreachable mail server or a mistyped hostname writes a live webhook
// token into a table that ends up in every database dump.
//
// Found during the Phase 14 image verification, where a deliberately
// unreachable endpoint put its full URL on the dead-letter board. The
// error's wrap chain is deliberately NOT preserved: the text is what
// carries the secret, so the text is what must be rebuilt, and nothing in
// the delivery path unwraps a transport error (classification is done on
// the HTTP status, and PermanentError is constructed explicitly).
func scrubURL(url string, err error) error {
	if err == nil {
		return nil
	}
	if url == "" {
		return err
	}
	scrubbed := strings.ReplaceAll(err.Error(), url, redactedWebhookURL)
	if scrubbed == err.Error() {
		return err
	}
	return errors.New(scrubbed)
}

// Config is app.alert_channel.config's decoded shape — the union of the
// three kinds' fields, since the column is one JSONB blob per row and
// each kind reads only what it needs. Unknown keys are ignored rather
// than rejected: a config written by a newer HANGAR must not stop an
// older one from delivering (and encoding/json ignores them by default,
// which is the behaviour we want, stated rather than assumed).
type Config struct {
	// Slack and Discord webhooks.
	URL string `json:"url"`
	// Discord only: overrides the webhook's display name.
	Username string `json:"username"`

	// SMTP.
	Host string   `json:"host"`
	Port int      `json:"port"`
	From string   `json:"from"`
	To   []string `json:"to"`
	// SMTPUsername/SMTPPassword are separate from Username above because
	// "username" already means the Discord display name in this shared
	// shape, and silently reusing one key for two unrelated meanings is
	// how a Discord display name ends up in an SMTP AUTH exchange.
	//
	// SMTPPassword is a credential at rest in app.alert_channel.config.
	// That is the schema this phase inherited (#39 is a single jsonb
	// column); it is flagged here rather than quietly accepted, because a
	// database dump therefore contains a live mail password.
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"`
	STARTTLS     *bool  `json:"starttls"`
	// ImplicitTLS is HANGAR_SMTP_TLS=tls — SMTPS, TLS from the first byte.
	// Mutually exclusive with STARTTLS in practice; if both are set,
	// implicit wins (it is the stronger statement, and a server that
	// speaks SMTPS has nothing to upgrade).
	ImplicitTLS bool   `json:"implicit_tls"`
	RequireTLS  bool   `json:"require_tls"`
	HELO        string `json:"helo"`
}

// New builds a live Channel from an app.alert_channel row's kind and
// config. An unrecognised kind is an error, not an open vocabulary:
// unlike a CCP notification type (which arrives from a system HANGAR does
// not control), the kind column is CHECK-constrained to exactly these
// three values, so a fourth can only come from a database written by a
// different version of HANGAR — and inventing a delivery mechanism for it
// is not something this process can do.
func New(kind string, rawConfig []byte) (Channel, error) {
	var cfg Config
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return nil, fmt.Errorf("channels: parsing %s channel config: %w", kind, err)
		}
	}

	switch kind {
	case KindSlackWebhook:
		return &SlackWebhook{URL: cfg.URL}, nil
	case KindDiscordWebhook:
		return &DiscordWebhook{URL: cfg.URL, Username: cfg.Username}, nil
	case KindSMTP:
		// STARTTLS defaults to true when the config does not say: a mail
		// path that silently downgrades to plaintext is a worse default
		// than one that has to be explicitly switched off.
		starttls := true
		if cfg.STARTTLS != nil {
			starttls = *cfg.STARTTLS
		}
		return &SMTP{
			Host: cfg.Host, Port: cfg.Port, From: cfg.From, To: cfg.To,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
			STARTTLS: starttls && !cfg.ImplicitTLS, ImplicitTLS: cfg.ImplicitTLS,
			RequireTLS: cfg.RequireTLS, HELO: cfg.HELO,
		}, nil
	default:
		return nil, fmt.Errorf("channels: unknown channel kind %q", kind)
	}
}
