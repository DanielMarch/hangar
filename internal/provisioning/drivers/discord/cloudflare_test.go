package discord_test

import (
	"testing"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/discord"
	"github.com/stretchr/testify/require"
)

// TestCloudflare1015ParsedFromHTMLAndJSON (roadmap exit criterion): both
// Cloudflare 1015 framings are detected, on both status codes the
// architecture doc names (429 and 403), and neither JSON-decoding the
// HTML body nor any other combination is ever mistaken for a ban.
func TestCloudflare1015ParsedFromHTMLAndJSON(t *testing.T) {
	htmlBody := []byte(`<!DOCTYPE html><html><head><title>Discord</title></head>
<body><h1>Discord is not responding</h1><p>Error code: 1015</p></body></html>`)
	jsonBody := []byte(`{"code": 40333, "message": "You are being rate limited."}`)

	cases := []struct {
		name        string
		status      int
		contentType string
		body        []byte
		want        bool
	}{
		{"HTML framing on 429", 429, "text/html; charset=UTF-8", htmlBody, true},
		{"HTML framing on 403", 403, "text/html; charset=UTF-8", htmlBody, true},
		{"JSON framing on 429", 429, "application/json", jsonBody, true},
		{"JSON framing on 403", 403, "application/json", jsonBody, true},
		{"normal Discord JSON 429 is not a ban", 429, "application/json", []byte(`{"code": 20028, "message": "rate limited"}`), false},
		{"normal Discord JSON 403 is not a ban", 403, "application/json", []byte(`{"code": 50013, "message": "Missing Permissions"}`), false},
		{"HTML body without the 1015 marker is not a ban", 429, "text/html", []byte(`<html><body>generic error</body></html>`), false},
		{"2XX is never sniffed as a ban regardless of body", 200, "text/html", htmlBody, false},
		{"unrelated 500 is never sniffed as a ban", 500, "text/html", htmlBody, false},
		{"malformed JSON body must not error, just not match", 429, "application/json", []byte(`not json at all`), false},
		{"missing content-type falls back to HTML sniff", 429, "", htmlBody, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := discord.DetectCloudflareBan(tc.status, tc.contentType, tc.body)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestCloudflareBanErrorMessage: the error type carries the status code
// through so a caller/log line can distinguish a 429 ban from a 403 ban.
func TestCloudflareBanErrorMessage(t *testing.T) {
	err := &discord.CloudflareBanError{StatusCode: 429}
	require.Contains(t, err.Error(), "429")
}
