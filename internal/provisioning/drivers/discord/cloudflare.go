package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// CloudflareBanError is returned when a response is detected as
// Cloudflare's undocumented 1015 rate-limit ban rather than a normal
// Discord API response — 01_ARCHITECTURE.md §9.3: "delivered outside
// Discord's normal framing: either an HTML body containing
// 'error code: 1015', or JSON {"code": 40333}, on a 429 or a 403."
type CloudflareBanError struct {
	StatusCode int
}

func (e *CloudflareBanError) Error() string {
	return fmt.Sprintf("discord: Cloudflare 1015 ban detected (status %d)", e.StatusCode)
}

// cloudflareJSONBody is the shape of Discord's normal JSON error envelope
// AND of the 1015-as-JSON framing — both carry a numeric "code" field, so
// one decode covers both without needing to special-case which framing
// arrived.
type cloudflareJSONBody struct {
	Code int `json:"code"`
}

// discord40333 is the JSON framing's code for a Cloudflare ban
// (01_ARCHITECTURE.md §9.3).
const discord40333 = 40333

// DetectCloudflareBan inspects a 429 or 403 response's content-type and
// body for either Cloudflare 1015 framing. Only ever called for 429/403 —
// callers must check statusCode themselves first, since sniffing the body
// of every 2XX response would be wasted work.
//
// Content-type is sniffed BEFORE attempting a JSON decode of the HTML
// framing — a JSON decode of an HTML body fails, and that failure must
// never be reported as a transport error (§9.3's edge case), so this
// function only decodes JSON when the content-type actually says JSON.
func DetectCloudflareBan(statusCode int, contentType string, body []byte) bool {
	if statusCode != 429 && statusCode != 403 {
		return false
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "text/html"):
		return bytes.Contains(bytes.ToLower(body), []byte("error code: 1015"))
	case strings.Contains(ct, "application/json"):
		var parsed cloudflareJSONBody
		if err := json.Unmarshal(body, &parsed); err != nil {
			return false // malformed JSON is not a 1015 ban — just not decodable, never a transport error
		}
		return parsed.Code == discord40333
	default:
		// Some proxies omit or mislabel content-type — fall back to a
		// body-prefix sniff of the HTML framing specifically, since that's
		// the one framing that doesn't require successful JSON decoding to
		// recognise.
		return bytes.Contains(bytes.ToLower(body), []byte("error code: 1015"))
	}
}
