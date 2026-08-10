// Package render holds alert/notification rendering. generic.go is the
// Principle 14 fallback: when there is no per-type template for a CCP
// notification (or the payload didn't even parse as YAML — see
// internal/sync/handlers/notification_sync.go), the payload is rendered as
// a flat, readable key/value listing instead of being dropped or erroring.
// This is Phase 9's first consumer of the open-vocabulary pattern applied
// to an entire domain rather than a single field; Phase 14 wires it in as
// the last resort in the alert-delivery template chain.
package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Generic renders a JSONB notification payload (internal/sync/handlers'
// character_notification.payload column — either the parsed YAML
// structure, or a `{"raw": "<verbatim text>"}` wrapper when the YAML
// failed to parse) as a stable, human-readable "key: value" listing, one
// line per field, nested maps indented and sorted by key so the output is
// deterministic across runs (important for tests and for not shuffling an
// operator's view every render).
//
// A nil/empty payload renders as a single explanatory line rather than an
// empty string, since "notification of a known type but no body" and "no
// payload at all" both need to show something.
func Generic(payload json.RawMessage) string {
	if len(payload) == 0 {
		return "(no payload)"
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		// Should not happen — the column is JSONB, Postgres already
		// validated it — but a render function must never panic on bad
		// input, so fall back to the raw bytes rather than erroring.
		return string(payload)
	}
	var b strings.Builder
	renderValue(&b, v, 0)
	return strings.TrimRight(b.String(), "\n")
}

func renderValue(b *strings.Builder, v any, depth int) {
	indent := strings.Repeat("  ", depth)
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := t[k]
			switch val.(type) {
			case map[string]any, []any:
				fmt.Fprintf(b, "%s%s:\n", indent, k)
				renderValue(b, val, depth+1)
			default:
				fmt.Fprintf(b, "%s%s: %s\n", indent, k, scalarString(val))
			}
		}
	case []any:
		for i, item := range t {
			switch item.(type) {
			case map[string]any, []any:
				fmt.Fprintf(b, "%s- [%d]:\n", indent, i)
				renderValue(b, item, depth+1)
			default:
				fmt.Fprintf(b, "%s- %s\n", indent, scalarString(item))
			}
		}
	default:
		fmt.Fprintf(b, "%s%s\n", indent, scalarString(v))
	}
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return "(null)"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// encoding/json decodes every JSON number as float64; render
		// whole numbers without a trailing ".0" since CCP notification
		// fields are overwhelmingly integer ids/counts.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
