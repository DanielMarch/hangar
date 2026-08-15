// Contact-notification sync — the second half of Appendix A capability #11
// "Notifications (+ contact notifications)".
//
// ── PHASE 20.7 (B48) ─────────────────────────────────────────────────────
// The character notifications feed (/characters/{id}/notifications) has been
// synced since Phase 9. Its sibling — the CONTACT notifications feed, which
// is what ESI calls the "so-and-so added you to their contacts, at this
// standing" messages — has a table (app.notification_contact) and a
// generated UpsertNotificationContact with no production caller, so the
// table was empty on every installation.
//
// The two feeds are deliberately NOT merged. They are different routes with
// different shapes: a character notification carries a `type` from CCP's
// open notification vocabulary and a YAML `text` payload, while a contact
// notification carries a standing level and a plain message and has no type
// at all. Folding the second into app.character_notification would have
// required inventing a `type` CCP never sent.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ContactNotificationDTO is one element of
// GET /characters/{character_id}/notifications/contacts.
//
// Every field is required on the live spec, but `message` and
// `standing_level` are carried as pointers because the columns behind them
// are nullable and a future spec revision relaxing them must not start
// writing zero-valued lies (standing 0.0 is a REAL standing — neutral —
// and is not the same statement as "no standing was reported").
type ContactNotificationDTO struct {
	Message           *string   `json:"message"`
	NotificationID    int64     `json:"notification_id"`
	SendDate          time.Time `json:"send_date"`
	SenderCharacterID int64     `json:"sender_character_id"`
	StandingLevel     *float64  `json:"standing_level"`
}

func ParseContactNotifications(body []byte) ([]ContactNotificationDTO, error) {
	var dto []ContactNotificationDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing contact notifications: %w", err)
	}
	return dto, nil
}

// SyncContactNotifications upserts every contact notification in the page.
//
// There is no prune, for the same reason character notifications have none:
// ESI serves a rolling recent window, so a notification leaving the window
// means "it aged out", not "it was retracted". Deleting on absence would
// throw away the entire history this table exists to accumulate.
//
// sender_name is left NULL: this route reports only the sender's character
// id, and the name is resolved from app.character elsewhere. Writing the id
// into the name column, or a placeholder, would make an unresolved sender
// indistinguishable from a resolved one.
func SyncContactNotifications(ctx context.Context, s *store.Store, characterID int64, notifications []ContactNotificationDTO) (SyncResult, error) {
	for _, n := range notifications {
		if _, err := s.UpsertNotificationContact(ctx, gen.UpsertNotificationContactParams{
			CharacterID:       characterID,
			NotificationID:    n.NotificationID,
			SendDate:          n.SendDate,
			SenderCharacterID: n.SenderCharacterID,
			SenderName:        nil,
			Message:           n.Message,
			StandingLevel:     n.StandingLevel,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact notification %d for character %d: %w", n.NotificationID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(notifications))}, nil
}
