// Package alerting is Phase 14's delivery pipeline: CCP notifications and
// HANGAR domain events in, deduplicated and coalesced messages out over
// SMTP/Slack/Discord webhooks, with a transactional outbox and an
// admin-visible dead-letter queue (00_SRS_v3.1.md §4.4).
//
// The pieces, and which file owns each:
//
//	dedupe.go     the stable fingerprint that makes re-ingesting the same
//	              notification a no-op, across process restarts
//	coalesce.go   the (routing target, alert type) coalescing key
//	route.go      routing rules -> concrete (target, channel) destinations
//	interpret.go  ingest: notification -> outbox rows, in one transaction
//	dispatch.go   the outbox pump: claim, group, render, send, settle
//	deadletter.go retry/backoff policy and the admin-visible dead-letter board
package alerting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// fingerprintVersion prefixes every hash. Bumping it deliberately
// invalidates every stored dedupe_hash at once — which is the only safe
// way to change the hashing scheme, since a silent change would make every
// already-delivered alert look new and re-deliver the lot.
const fingerprintVersion = "hangar-alert-v1"

// Fingerprint is the semantic identity of one alert event: the alert type
// plus the minimal set of fields that make this occurrence distinct from
// every other. Hash() turns it into app.alert_event.dedupe_hash, whose
// UNIQUE constraint (with RecordAlertEvent's ON CONFLICT DO NOTHING) is
// what makes ingestion idempotent.
//
// §4.4: "hash-based deduplication stable across process restarts" and
// "hash the payload's semantic fields, not a serialisation that includes a
// timestamp or map ordering". Three concrete rules follow from that, and
// all three are enforced by construction here rather than by convention:
//
//   - NEVER hash a whole payload serialisation. Go map iteration order is
//     randomised per process, so json.Marshal of a map[string]any produces
//     a different byte string on a different run — the hash would be
//     stable within one process and unstable across restarts, which is the
//     worst possible failure mode (it looks fine in a unit test).
//     Fields is therefore an explicit, caller-chosen set, sorted here.
//   - NEVER include a timestamp. The same notification re-read on the next
//     poll must hash identically; `occurred_at` and any ingest time are
//     excluded by simply not being fields anyone passes.
//   - NEVER concatenate ambiguously. "a"+"bc" and "ab"+"c" must not
//     collide, so every component is length-prefixed below.
type Fingerprint struct {
	AlertType string
	Fields    map[string]string
}

// Hash renders the fingerprint as a hex SHA-256 digest, stable across
// processes, restarts, architectures and Go versions.
//
// crypto/sha256 rather than hash/maphash or hash/fnv on purpose: maphash
// is explicitly seeded per process and documented as unstable across runs,
// which would silently break the exact property §4.4 asks for.
func (f Fingerprint) Hash() string {
	keys := make([]string, 0, len(f.Fields))
	for k := range f.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys) // map ordering must never reach the digest

	h := sha256.New()
	writeLengthPrefixed(h, fingerprintVersion)
	writeLengthPrefixed(h, f.AlertType)
	for _, k := range keys {
		writeLengthPrefixed(h, k)
		writeLengthPrefixed(h, f.Fields[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeLengthPrefixed writes len(s) and s, making the concatenation
// injective: no two distinct field sets can produce the same byte stream.
func writeLengthPrefixed(h interface{ Write([]byte) (int, error) }, s string) {
	_, _ = h.Write([]byte(strconv.Itoa(len(s))))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(s))
	_, _ = h.Write([]byte{'\x00'})
}

// NotificationFingerprint is the identity of one CCP notification routed
// to one target: (alert type, notification id, target).
//
// The PAYLOAD IS DELIBERATELY NOT HASHED. CCP's notification_id is already
// the upstream's own primary key for the notification — it is the semantic
// field, and it is a single integer that cannot vary with serialisation.
// Hashing the payload as well would mean a whitespace or key-order change
// in CCP's YAML re-delivering an alert an operator already read, which is
// precisely the restart-instability §4.4 forbids, just triggered by
// upstream rather than by us.
//
// The target is part of the identity because the same notification routed
// to two different targets is two different alerts, and each needs its own
// coalescing group and its own delivery record.
func NotificationFingerprint(alertType string, notificationID int64, target Target) Fingerprint {
	return Fingerprint{
		AlertType: alertType,
		Fields: map[string]string{
			"notification_id": strconv.FormatInt(notificationID, 10),
			"target_kind":     target.Kind,
			"target_ref":      target.Ref,
		},
	}
}

// ThresholdFingerprint is the identity of one threshold evaluation:
// (alert type, subject, target, bucket). The bucket is the caller's
// re-arm token — e.g. a fuel-low alert for structure 1234 uses the
// fuel-expiry timestamp it fired against, so the same structure alerts
// once per refuelling cycle rather than once per sync pass, and no
// timestamp of the EVALUATION (which would defeat dedupe entirely) is
// involved.
func ThresholdFingerprint(alertType, subjectKind string, subjectID int64, bucket string, target Target) Fingerprint {
	return Fingerprint{
		AlertType: alertType,
		Fields: map[string]string{
			"subject_kind": subjectKind,
			"subject_id":   strconv.FormatInt(subjectID, 10),
			"bucket":       bucket,
			"target_kind":  target.Kind,
			"target_ref":   target.Ref,
		},
	}
}

// SemanticFields extracts the named top-level keys from a JSON payload as
// canonical strings, for a caller that must fingerprint on payload content
// (a domain event with no natural upstream id). Only the named keys are
// read — that is the whole point: naming them is what makes the field set
// semantic rather than a serialisation.
//
// A missing key yields the empty string rather than an error: "absent" is
// a stable, meaningful value for a fingerprint, and erroring here would
// turn a payload-shape change into a halted queue.
func SemanticFields(payload json.RawMessage, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	var decoded map[string]any
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &decoded) // a non-object payload leaves decoded nil
	}
	for _, k := range keys {
		out[k] = canonicalScalar(decoded[k])
	}
	return out
}

// canonicalScalar renders a decoded JSON value as a stable string. Nested
// values are rendered through their sorted-key JSON form so a map's
// iteration order still cannot reach the digest.
func canonicalScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+canonicalScalar(t[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, canonicalScalar(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%v", t)
	}
}
