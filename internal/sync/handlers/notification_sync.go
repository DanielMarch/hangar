// Character notification sync (Phase 9). CCP's notification `text` field
// is YAML — and, per the roadmap's own edge case, not always VALID YAML:
// some payloads carry unquoted values a strict parser rejects. That is an
// expected path here, not an exception: SyncCharacterNotifications never
// returns an error for one bad payload, because Principle 14 in its most
// operationally important form is "the sync queue must never halt on one
// bad payload".
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"gopkg.in/yaml.v3"
)

// CharacterNotificationDTO mirrors GET /characters/{id}/notifications.
type CharacterNotificationDTO struct {
	IsRead         *bool     `json:"is_read,omitempty"`
	NotificationID int64     `json:"notification_id"`
	SenderID       int64     `json:"sender_id"`
	SenderType     string    `json:"sender_type"`
	Text           string    `json:"text"`
	Timestamp      time.Time `json:"timestamp"`
	Type           string    `json:"type"`
}

func ParseCharacterNotifications(body []byte) ([]CharacterNotificationDTO, error) {
	var dto []CharacterNotificationDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character notifications: %w", err)
	}
	return dto, nil
}

// SyncCharacterNotifications upserts every notification, recording `type`
// into the open vocabulary (Principle 14) and attempting to parse `text`
// as YAML into `payload`. A parse failure:
//   - never aborts the loop or returns an error — the row is still
//     written, with parse_failed=true and payload={"raw": text};
//   - is recorded on app.notification_unknown_type's board via
//     RecordUnknownNotificationType so an operator can see it, using the
//     same board the roadmap calls "the unknown-types board" rather than
//     inventing a second one for this specific failure mode.
func SyncCharacterNotifications(ctx context.Context, s *store.Store, characterID int64, notifications []CharacterNotificationDTO) (SyncResult, error) {
	for _, n := range notifications {
		if err := s.RecordOpenVocabularyValue(ctx, "notification_type", n.Type); err != nil {
			return SyncResult{}, fmt.Errorf("handlers: recording notification_type %q: %w", n.Type, err)
		}

		payload, parseFailed := parseNotificationYAML(n.Text)
		if parseFailed {
			if err := s.RecordUnknownNotificationType(ctx, n.Type, payload); err != nil {
				return SyncResult{}, fmt.Errorf("handlers: recording unparseable notification %d (type %q) on the unknown-types board: %w", n.NotificationID, n.Type, err)
			}
		}

		text := n.Text
		if _, err := s.UpsertCharacterNotification(ctx, gen.UpsertCharacterNotificationParams{
			CharacterID: characterID, NotificationID: n.NotificationID, SentAt: n.Timestamp,
			SenderID: &n.SenderID, SenderType: &n.SenderType, Type: n.Type, Text: &text,
			IsRead: n.IsRead, Payload: payload, ParseFailed: parseFailed,
		}); ignoreUnchanged(err) != nil {
			// A DB error here is a genuine infrastructure failure, distinct
			// from a YAML-shape failure — this one DOES propagate, since
			// Principle 14 only promises to never reject a value the
			// EXTERNAL system sent us, not to swallow our own storage errors.
			return SyncResult{}, fmt.Errorf("handlers: upserting notification %d for character %d: %w", n.NotificationID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(notifications))}, nil
}

// parseNotificationYAML attempts to decode raw as YAML into a
// JSON-compatible structure. On success it returns the re-encoded JSON and
// parseFailed=false. On failure — the expected, common case for certain
// CCP notification types — it returns a `{"raw": raw}` wrapper and
// parseFailed=true, never an error: the caller always has a payload to
// store and render.
func parseNotificationYAML(raw string) (payload []byte, parseFailed bool) {
	var decoded any
	if err := yaml.Unmarshal([]byte(raw), &decoded); err != nil {
		fallback, _ := json.Marshal(map[string]string{"raw": raw})
		return fallback, true
	}
	normalized := normalizeYAMLValue(decoded)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		fallback, _ := json.Marshal(map[string]string{"raw": raw})
		return fallback, true
	}
	return encoded, false
}

// normalizeYAMLValue converts yaml.v3's decoded map[string]interface{} (it
// actually produces map[string]interface{} for mapping nodes when the
// target is `any`) recursively so every nested value is JSON-marshalable —
// yaml.v3 can produce map[interface{}]interface{} keys in some code paths,
// which encoding/json cannot marshal directly.
func normalizeYAMLValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeYAMLValue(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = normalizeYAMLValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeYAMLValue(val)
		}
		return out
	default:
		return t
	}
}
