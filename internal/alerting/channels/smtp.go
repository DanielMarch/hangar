package channels

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/alerting/render"
)

// SMTPBodyLimit bounds the rendered body of an email roll-up. Email has no
// protocol limit worth truncating against the way Slack and Discord do
// (SMTP's constraint is a 998-character LINE limit, which
// quoted-printable encoding handles on its own), so this exists only to
// stop a pathological payload producing an unbounded message — a 40-event
// roll-up is nowhere near it, and an email is therefore the channel that
// carries the complete list when the chat channels have truncated.
const SMTPBodyLimit = 64 * 1024

// SMTP delivers alerts by email over net/smtp with STARTTLS and MIME
// assembled by hand.
//
// No SMTP library is in go.mod and none is added: the stdlib covers the
// whole contract (EHLO, STARTTLS, AUTH, MAIL/RCPT/DATA), and the parts it
// does not cover — the MIME headers and quoted-printable body — are forty
// lines of encoding that a dependency would not shrink. This matches how
// every other outbound integration in this codebase is built (the Discord,
// TeamSpeak and Mumble drivers are all hand-rolled clients for the same
// reason).
type SMTP struct {
	Host string
	Port int
	// From is the envelope sender and the From: header.
	From string
	// To is the envelope recipient list and the To: header. At least one
	// address is required.
	To []string
	// Username/Password enable AUTH when Username is non-empty. net/smtp
	// refuses PLAIN auth over an unencrypted connection to a non-localhost
	// server, which is correct and deliberately not worked around.
	Username string
	Password string
	// STARTTLS upgrades the connection when the server advertises the
	// extension. RequireTLS turns "advertised but unavailable" into a
	// failure instead of a silent plaintext send.
	STARTTLS   bool
	RequireTLS bool
	// ImplicitTLS dials TLS directly instead of negotiating STARTTLS —
	// SMTPS, conventionally port 465. This is HANGAR_SMTP_TLS=tls in
	// .env.example, and it is a genuinely different wire protocol from
	// STARTTLS, not a stricter version of it: the handshake happens before
	// the 220 greeting, so a client that dials plaintext and then issues
	// STARTTLS gets nothing but a timeout against a 465 listener.
	ImplicitTLS bool
	TLSConfig   *tls.Config
	// HELO is the name sent in EHLO; defaults to "localhost".
	HELO string
	// Timeout bounds the whole delivery (dial plus conversation).
	Timeout time.Duration
}

var _ Channel = (*SMTP)(nil)

// Kind implements Channel.
func (s *SMTP) Kind() string { return KindSMTP }

// Send implements Channel. Every failure it returns is classified:
// anything the server reported with a 5xx reply, and anything wrong with
// the configuration itself, is permanent; a dial failure or a 4xx reply is
// transient and gets retried with backoff. §4.4: "An SMTP failure must
// retry with backoff and eventually dead-letter — never block the queue",
// and nothing in this method sleeps or blocks on a retry: one attempt,
// one verdict, back to the pump.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	if s.Host == "" || s.From == "" || len(s.To) == 0 {
		return &PermanentError{Reason: "smtp: channel is missing host, from or recipients"}
	}

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.port()))
	conn, err := s.dial(ctx, addr)
	if err != nil {
		// Host down, DNS failure, connection refused, TLS handshake
		// failure — transient by default, so a mail server restarting does
		// not dead-letter every alert queued during the restart.
		return fmt.Errorf("smtp: dialling %s: %w", addr, err)
	}
	// The context deadline also bounds the conversation, not just the
	// dial: an SMTP server that accepts the connection and then never
	// answers must not hold a delivery worker open indefinitely.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: starting session with %s: %w", addr, err)
	}
	defer func() { _ = client.Close() }()

	helo := s.HELO
	if helo == "" {
		helo = "localhost"
	}
	if err := client.Hello(helo); err != nil {
		return classifySMTP(fmt.Errorf("smtp: EHLO: %w", err), err)
	}

	switch {
	case s.ImplicitTLS:
		// Already encrypted from the first byte — nothing to negotiate.
	case s.STARTTLS:
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(s.tlsConfig()); err != nil {
				return classifySMTP(fmt.Errorf("smtp: STARTTLS: %w", err), err)
			}
		} else if s.RequireTLS {
			return &PermanentError{Reason: fmt.Sprintf("smtp: %s does not advertise STARTTLS but TLS is required", addr)}
		}
	case s.RequireTLS:
		return &PermanentError{Reason: "smtp: TLS is required but neither STARTTLS nor implicit TLS is enabled on this channel"}
	}

	if s.Username != "" {
		auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
		if err := client.Auth(auth); err != nil {
			// Bad credentials are permanent; so is net/smtp's refusal to
			// send PLAIN over an unencrypted link. Both are configuration
			// errors an operator must fix, and neither improves on retry.
			return &PermanentError{Reason: "smtp: authentication failed", Err: err}
		}
	}

	if err := client.Mail(s.From); err != nil {
		return classifySMTP(fmt.Errorf("smtp: MAIL FROM %s: %w", s.From, err), err)
	}
	for _, rcpt := range s.To {
		if err := client.Rcpt(rcpt); err != nil {
			return classifySMTP(fmt.Errorf("smtp: RCPT TO %s: %w", rcpt, err), err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return classifySMTP(fmt.Errorf("smtp: DATA: %w", err), err)
	}
	if _, err := w.Write(s.buildMIME(msg)); err != nil {
		return fmt.Errorf("smtp: writing message body: %w", err)
	}
	// Closing the DATA writer sends the terminating dot and reads the
	// server's verdict — a failure here is the server rejecting the
	// message, not a write error, so it is classified like any other reply.
	if err := w.Close(); err != nil {
		return classifySMTP(fmt.Errorf("smtp: completing DATA: %w", err), err)
	}
	if err := client.Quit(); err != nil {
		// The message was accepted at the terminating dot; a failure to
		// QUIT cleanly is not a delivery failure and must not cause a
		// retry (which would send it twice).
		return nil
	}
	return nil
}

// buildMIME assembles the RFC 5322 message: headers with CRLF line
// endings, an RFC 2047-encoded subject so a non-ASCII structure name
// survives, and a quoted-printable body (which also keeps every line
// inside SMTP's 998-character limit without any wrapping logic here).
//
// Dot-stuffing is not handled here on purpose: net/smtp's Data() writer is
// a textproto.DotWriter, which escapes a leading "." and appends the
// terminating sequence itself. Doing it twice would corrupt every line
// that starts with a dot.
func (s *SMTP) buildMIME(msg Message) []byte {
	body := render.Rollup(msg.Header, msg.Lines, SMTPBodyLimit)
	if msg.Mention != "" {
		body = msg.Mention + "\n" + body
	}

	var b strings.Builder
	writeHeader(&b, "From", s.From)
	writeHeader(&b, "To", strings.Join(s.To, ", "))
	writeHeader(&b, "Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHeader(&b, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", s.messageID())
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&b, "Content-Transfer-Encoding", "quoted-printable")
	// X-HANGAR-Alert-Type lets a mail filter route by alert type without
	// parsing the subject line.
	writeHeader(&b, "X-HANGAR-Alert-Type", sanitiseHeaderValue(msg.AlertType))
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = qp.Close()

	return []byte(b.String())
}

func (s *SMTP) messageID() string {
	domain := "hangar.local"
	if at := strings.LastIndex(s.From, "@"); at >= 0 && at+1 < len(s.From) {
		domain = s.From[at+1:]
	}
	return fmt.Sprintf("<%s@%s>", uuid.New(), sanitiseHeaderValue(domain))
}

func (s *SMTP) port() int {
	if s.Port > 0 {
		return s.Port
	}
	if s.ImplicitTLS {
		return 465 // SMTPS
	}
	return 587 // the submission port; STARTTLS's home
}

// dial opens the connection, wrapping it in TLS up front for SMTPS.
func (s *SMTP) dial(ctx context.Context, addr string) (net.Conn, error) {
	dialer := &net.Dialer{}
	if !s.ImplicitTLS {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	return (&tls.Dialer{NetDialer: dialer, Config: s.tlsConfig()}).DialContext(ctx, "tcp", addr)
}

func (s *SMTP) tlsConfig() *tls.Config {
	if s.TLSConfig != nil {
		return s.TLSConfig
	}
	return &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
}

// writeHeader emits one header line, guarding against header injection
// from any value that reached us through configuration or a payload.
func writeHeader(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(sanitiseHeaderValue(value))
	b.WriteString("\r\n")
}

// sanitiseHeaderValue strips CR and LF. A newline inside a header value
// would let a crafted alert payload inject arbitrary headers (or a body)
// into the message — the classic email header-injection bug. The subject
// is RFC 2047-encoded before it gets here, but the alert type and the
// From-derived domain are not, and neither is any future header.
func sanitiseHeaderValue(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

// classifySMTP maps an SMTP reply onto the retry/dead-letter decision.
// net/smtp surfaces a server's reply as *textproto.Error, whose Code is
// the reply code: 5xx is a permanent rejection (the RFC's own definition —
// "the command was rejected and should not be retried"), 4xx is a
// transient one ("try again later"), which is exactly the distinction the
// dead-letter policy needs.
func classifySMTP(wrapped error, cause error) error {
	var protoErr *textproto.Error
	if errors.As(cause, &protoErr) && protoErr.Code >= 500 {
		return &PermanentError{Reason: wrapped.Error()}
	}
	return wrapped
}
