package channels_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/stretchr/testify/require"
)

func sampleMessage(lines int) channels.Message {
	out := make([]string, lines)
	for i := range out {
		out[i] = fmt.Sprintf("structure Nakugard XI - Moon 3 - Astrahus #%02d — system 30002053 — attacker Test Corp", i)
	}
	return channels.Message{
		AlertType: "StructureUnderAttack",
		Subject:   "Structure under attack (40 events)",
		Header:    "Structure under attack (40 events)",
		Lines:     out,
		Count:     lines,
	}
}

func TestSlackWebhookPostsBlocksAndTruncates(t *testing.T) {
	var got struct {
		Text   string `json:"text"`
		Blocks []struct {
			Type string `json:"type"`
			Text struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"text"`
		} `json:"blocks"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &got))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	slack := &channels.SlackWebhook{URL: server.URL, HTTPClient: server.Client()}
	require.Equal(t, channels.KindSlackWebhook, slack.Kind())

	msg := sampleMessage(40)
	msg.Mention = "<!here>"
	require.NoError(t, slack.Send(context.Background(), msg))

	require.Equal(t, msg.Subject, got.Text, "the fallback text is what Slack shows in a notification preview")
	require.Len(t, got.Blocks, 1)
	block := got.Blocks[0].Text.Text
	require.True(t, strings.HasPrefix(block, "<!here>\n"), "the mention must lead, verbatim and unescaped")
	require.LessOrEqual(t, len([]rune(block)), channels.SlackSectionTextLimit,
		"the section text must fit Slack's 3,000-character block limit even with the mention prepended")
}

func TestDiscordWebhookPostsContentAndTruncates(t *testing.T) {
	var got struct {
		Content  string `json:"content"`
		Username string `json:"username"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &got))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	discord := &channels.DiscordWebhook{URL: server.URL, Username: "HANGAR", HTTPClient: server.Client()}
	require.Equal(t, channels.KindDiscordWebhook, discord.Kind())

	msg := sampleMessage(40)
	msg.Mention = "<@&123456789>"
	require.NoError(t, discord.Send(context.Background(), msg))

	require.Equal(t, "HANGAR", got.Username)
	require.LessOrEqual(t, len([]rune(got.Content)), channels.DiscordContentLimit,
		"content must fit Discord's 2,000-character limit")
	require.Contains(t, got.Content, "… and ", "a truncated roll-up must declare its remainder")
	require.True(t, strings.HasPrefix(got.Content, "<@&123456789>\n"))
}

// TestWebhookErrorClassification is what the dead-letter policy hangs on:
// a 5xx or a 429 is worth retrying, a 4xx is not.
func TestWebhookErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status    int
		permanent bool
	}{
		{http.StatusNotFound, true},         // webhook deleted
		{http.StatusBadRequest, true},       // malformed payload
		{http.StatusForbidden, true},        // revoked
		{http.StatusTooManyRequests, false}, // rate-limited: retry
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
	} {
		t.Run(fmt.Sprintf("status-%d", tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
			}))
			defer server.Close()

			slackErr := (&channels.SlackWebhook{URL: server.URL, HTTPClient: server.Client()}).Send(context.Background(), sampleMessage(1))
			require.Error(t, slackErr)
			require.Equal(t, tc.permanent, channels.IsPermanent(slackErr), "slack status %d", tc.status)

			discordErr := (&channels.DiscordWebhook{URL: server.URL, HTTPClient: server.Client()}).Send(context.Background(), sampleMessage(1))
			require.Error(t, discordErr)
			require.Equal(t, tc.permanent, channels.IsPermanent(discordErr), "discord status %d", tc.status)
		})
	}

	// An unreachable host is transient — a webhook host having a bad
	// minute must not dead-letter every queued alert.
	unreachable := &channels.DiscordWebhook{URL: "http://127.0.0.1:1/hook", Timeout: time.Second}
	err := unreachable.Send(context.Background(), sampleMessage(1))
	require.Error(t, err)
	require.False(t, channels.IsPermanent(err))

	// A channel with no URL configured cannot be fixed by retrying.
	require.True(t, channels.IsPermanent((&channels.SlackWebhook{}).Send(context.Background(), sampleMessage(1))))
	require.True(t, channels.IsPermanent((&channels.DiscordWebhook{}).Send(context.Background(), sampleMessage(1))))
}

// TestWebhookURLNeverAppearsInAnError is a security assertion, not a
// cosmetic one: a webhook URL carries its own token, and a delivery
// error's text is written to app.alert_delivery.error (rendered on the
// admin dead-letter board, and present in every database dump) and to the
// process log. net/http embeds the full request URL in every transport
// error, so without scrubbing, one unreachable endpoint leaks a live
// credential into both.
//
// Found during Phase 14's image verification, where a deliberately
// unreachable webhook put its full URL on the dead-letter board.
func TestWebhookURLNeverAppearsInAnError(t *testing.T) {
	const (
		slackURL   = "https://hooks.slack.test/services/T00000000/B00000000/SUPERSECRETTOKEN"
		discordURL = "https://discord.test/api/webhooks/123456789/dISCORDsUPERsECRETtOKEN"
	)

	// Point both at a closed port so the failure is a transport error —
	// the path that carries the URL.
	slackErr := (&channels.SlackWebhook{URL: "http://127.0.0.1:1/" + slackURL, Timeout: 2 * time.Second}).
		Send(context.Background(), sampleMessage(1))
	require.Error(t, slackErr)
	require.NotContains(t, slackErr.Error(), "SUPERSECRETTOKEN", "a Slack webhook token must never reach an error string")
	require.Contains(t, slackErr.Error(), "<webhook url redacted>")

	discordErr := (&channels.DiscordWebhook{URL: "http://127.0.0.1:1/" + discordURL, Timeout: 2 * time.Second}).
		Send(context.Background(), sampleMessage(1))
	require.Error(t, discordErr)
	require.NotContains(t, discordErr.Error(), "dISCORDsUPERsECRETtOKEN", "a Discord webhook token must never reach an error string")
	require.Contains(t, discordErr.Error(), "<webhook url redacted>")

	// Scrubbing must not swallow the diagnosis: an operator still needs to
	// know what went wrong, just not the credential.
	require.Contains(t, slackErr.Error(), "slack: posting to webhook")
	require.Contains(t, discordErr.Error(), "discord: posting to webhook")

	// A status-code failure carries the response body, not the URL, and
	// must remain classifiable.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Unknown Webhook"}`))
	}))
	defer server.Close()
	statusErr := (&channels.DiscordWebhook{URL: server.URL, HTTPClient: server.Client()}).
		Send(context.Background(), sampleMessage(1))
	require.True(t, channels.IsPermanent(statusErr))
	require.Contains(t, statusErr.Error(), "Unknown Webhook")
}

// fakeSMTP is a minimal SMTP responder: enough of RFC 5321 for net/smtp's
// client to complete a session, and no more. reply overrides let a test
// make the server reject at a chosen stage with a chosen code.
type fakeSMTP struct {
	listener net.Listener
	replies  map[string]string

	received chan string
}

func newFakeSMTP(t *testing.T, replies map[string]string) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &fakeSMTP{listener: listener, replies: replies, received: make(chan string, 1)}
	go s.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return s
}

func (s *fakeSMTP) addr() (host string, port int) {
	tcpAddr := s.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcpAddr.Port
}

func (s *fakeSMTP) reply(verb, fallback string) string {
	if r, ok := s.replies[verb]; ok {
		return r
	}
	return fallback
}

func (s *fakeSMTP) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 fake.hangar.test ESMTP")

	var body strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			write(s.reply("EHLO", "250 fake.hangar.test"))
		case strings.HasPrefix(cmd, "MAIL FROM"):
			write(s.reply("MAIL", "250 2.1.0 Ok"))
		case strings.HasPrefix(cmd, "RCPT TO"):
			write(s.reply("RCPT", "250 2.1.5 Ok"))
		case strings.HasPrefix(cmd, "DATA"):
			write(s.reply("DATA", "354 End data with <CR><LF>.<CR><LF>"))
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			select {
			case s.received <- body.String():
			default:
			}
			write(s.reply("END", "250 2.0.0 Ok: queued as ABC123"))
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 2.0.0 Bye")
			return
		default:
			write("250 2.0.0 Ok")
		}
	}
}

func TestSMTPSendBuildsAValidMIMEMessage(t *testing.T) {
	server := newFakeSMTP(t, nil)
	host, port := server.addr()

	smtpChannel := &channels.SMTP{
		Host: host, Port: port,
		From: "hangar@example.test", To: []string{"ops@example.test", "director@example.test"},
		STARTTLS: false, HELO: "hangar.test", Timeout: 10 * time.Second,
	}
	require.Equal(t, channels.KindSMTP, smtpChannel.Kind())

	msg := sampleMessage(40)
	msg.Subject = "Structure under attack (40 events) — Nakugard ☃"
	require.NoError(t, smtpChannel.Send(context.Background(), msg))

	var raw string
	select {
	case raw = <-server.received:
	case <-time.After(5 * time.Second):
		t.Fatal("the fake SMTP server never received a message body")
	}

	require.Contains(t, raw, "From: hangar@example.test\r\n")
	require.Contains(t, raw, "To: ops@example.test, director@example.test\r\n")
	require.Contains(t, raw, "MIME-Version: 1.0\r\n")
	require.Contains(t, raw, `Content-Type: text/plain; charset="utf-8"`)
	require.Contains(t, raw, "Content-Transfer-Encoding: quoted-printable\r\n")
	require.Contains(t, raw, "X-HANGAR-Alert-Type: StructureUnderAttack\r\n")
	require.Contains(t, raw, "Message-ID: <", "every message needs a unique id")

	// A non-ASCII subject must be RFC 2047 encoded, not sent raw.
	require.Contains(t, raw, "Subject: =?utf-8?", "a non-ASCII subject must be encoded")
	require.NotContains(t, raw, "Subject: Structure under attack (40 events) — Nakugard ☃")

	// SMTP's 998-character line limit: quoted-printable wrapping is what
	// keeps a long roll-up legal without any wrapping logic of our own.
	for _, line := range strings.Split(raw, "\r\n") {
		require.LessOrEqual(t, len(line), 998, "no line may exceed SMTP's limit: %q", line)
	}

	// Email is the channel that carries everything — no truncation.
	require.NotContains(t, raw, "and 3D", "the quoted-printable body must not contain a truncation marker")
}

func TestSMTPHeaderInjectionIsNeutralised(t *testing.T) {
	server := newFakeSMTP(t, nil)
	host, port := server.addr()

	smtpChannel := &channels.SMTP{
		Host: host, Port: port, From: "hangar@example.test", To: []string{"ops@example.test"},
		Timeout: 10 * time.Second,
	}

	// An alert type is an OPEN VOCABULARY — a runtime-discovered CCP type
	// reaches the X-HANGAR-Alert-Type header. A newline in it would let
	// the payload inject arbitrary headers.
	msg := sampleMessage(1)
	msg.AlertType = "Evil\r\nBcc: attacker@example.test"
	require.NoError(t, smtpChannel.Send(context.Background(), msg))

	raw := <-server.received
	// The check is per LINE, not per substring: the injected text is still
	// present (folded into the header's own value, which is exactly what
	// sanitising means) — what must NOT exist is a line that starts a new
	// header. Asserting on the substring alone would fail against the
	// correctly sanitised output.
	for _, line := range strings.Split(raw, "\r\n") {
		require.False(t, strings.HasPrefix(line, "Bcc:"),
			"a header value must not be able to start a new header line: %q", line)
	}
	require.Contains(t, raw, "X-HANGAR-Alert-Type: Evil  Bcc: attacker@example.test\r\n",
		"the injected CR/LF must be folded to spaces, keeping the value on one line")
}

func TestSMTPFailureClassification(t *testing.T) {
	t.Run("permanent 5xx rejection", func(t *testing.T) {
		server := newFakeSMTP(t, map[string]string{"RCPT": "550 5.1.1 No such user here"})
		host, port := server.addr()
		err := (&channels.SMTP{
			Host: host, Port: port, From: "hangar@example.test", To: []string{"nobody@example.test"},
			Timeout: 10 * time.Second,
		}).Send(context.Background(), sampleMessage(1))
		require.Error(t, err)
		require.True(t, channels.IsPermanent(err), "a 5xx SMTP reply is a permanent rejection: %v", err)
	})

	t.Run("transient 4xx rejection", func(t *testing.T) {
		server := newFakeSMTP(t, map[string]string{"MAIL": "451 4.3.0 Try again later"})
		host, port := server.addr()
		err := (&channels.SMTP{
			Host: host, Port: port, From: "hangar@example.test", To: []string{"ops@example.test"},
			Timeout: 10 * time.Second,
		}).Send(context.Background(), sampleMessage(1))
		require.Error(t, err)
		require.False(t, channels.IsPermanent(err), "a 4xx SMTP reply must be retried: %v", err)
	})

	t.Run("unreachable host is transient", func(t *testing.T) {
		err := (&channels.SMTP{
			Host: "127.0.0.1", Port: 1, From: "hangar@example.test", To: []string{"ops@example.test"},
			Timeout: 2 * time.Second,
		}).Send(context.Background(), sampleMessage(1))
		require.Error(t, err)
		require.False(t, channels.IsPermanent(err), "a mail server that is down must not dead-letter the queue")
	})

	t.Run("required TLS on a server without STARTTLS is permanent", func(t *testing.T) {
		server := newFakeSMTP(t, nil) // advertises no extensions
		host, port := server.addr()
		err := (&channels.SMTP{
			Host: host, Port: port, From: "hangar@example.test", To: []string{"ops@example.test"},
			STARTTLS: true, RequireTLS: true, Timeout: 10 * time.Second,
		}).Send(context.Background(), sampleMessage(1))
		require.Error(t, err)
		require.True(t, channels.IsPermanent(err), "a configuration that can never succeed must not be retried forever")
	})

	t.Run("misconfiguration is permanent", func(t *testing.T) {
		err := (&channels.SMTP{From: "hangar@example.test"}).Send(context.Background(), sampleMessage(1))
		require.True(t, channels.IsPermanent(err))
	})
}

func TestNewChannelFromConfig(t *testing.T) {
	slack, err := channels.New(channels.KindSlackWebhook, []byte(`{"url":"https://hooks.slack.test/x"}`))
	require.NoError(t, err)
	require.Equal(t, channels.KindSlackWebhook, slack.Kind())

	discord, err := channels.New(channels.KindDiscordWebhook, []byte(`{"url":"https://discord.test/api/webhooks/1/x","username":"HANGAR"}`))
	require.NoError(t, err)
	require.Equal(t, channels.KindDiscordWebhook, discord.Kind())

	mail, err := channels.New(channels.KindSMTP, []byte(`{"host":"mail.test","port":587,"from":"a@b.test","to":["c@d.test"],"smtp_username":"u","smtp_password":"p"}`))
	require.NoError(t, err)
	require.Equal(t, channels.KindSMTP, mail.Kind())
	require.True(t, mail.(*channels.SMTP).STARTTLS, "STARTTLS must default ON — a silent plaintext downgrade is the worse default")
	require.Equal(t, "u", mail.(*channels.SMTP).Username)

	off, err := channels.New(channels.KindSMTP, []byte(`{"host":"mail.test","starttls":false}`))
	require.NoError(t, err)
	require.False(t, off.(*channels.SMTP).STARTTLS, "an explicit false must be honoured")

	_, err = channels.New("carrier_pigeon", nil)
	require.Error(t, err, "a kind outside app.alert_channel's CHECK constraint has no delivery mechanism")

	_, err = channels.New(channels.KindSlackWebhook, []byte(`{not json`))
	require.Error(t, err)

	// An unknown key must be ignored, not rejected: a config written by a
	// newer HANGAR must not stop an older one delivering.
	_, err = channels.New(channels.KindSlackWebhook, []byte(`{"url":"https://x.test","future_field":true}`))
	require.NoError(t, err)
}
