// Calendar sync (Phase 9): event list plus per-event detail and
// attendees. Character-scoped only.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CalendarEventDTO mirrors one element of GET /characters/{id}/calendar/events.
type CalendarEventDTO struct {
	EventDate     time.Time `json:"event_date"`
	EventID       int64     `json:"event_id"`
	EventResponse *string   `json:"event_response,omitempty"`
	Importance    *int32    `json:"importance,omitempty"`
	Title         string    `json:"title"`
}

func ParseCalendarEvents(body []byte) ([]CalendarEventDTO, error) {
	var dto []CalendarEventDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing calendar events: %w", err)
	}
	return dto, nil
}

func SyncCalendarEvents(ctx context.Context, s *store.Store, characterID int64, events []CalendarEventDTO) (SyncResult, error) {
	for _, e := range events {
		if _, err := s.UpsertCalendarEvent(ctx, gen.UpsertCalendarEventParams{
			CharacterID: characterID, EventID: e.EventID, Title: e.Title, EventDate: e.EventDate,
			EventResponse: e.EventResponse, Importance: e.Importance,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting calendar event %d for character %d: %w", e.EventID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(events))}, nil
}

// CalendarEventDetailDTO mirrors GET /characters/{id}/calendar/events/{event_id}.
type CalendarEventDetailDTO struct {
	Duration  *int32  `json:"duration,omitempty"`
	OwnerID   *int64  `json:"owner_id,omitempty"`
	OwnerName *string `json:"owner_name,omitempty"`
	OwnerType *string `json:"owner_type,omitempty"`
	Text      *string `json:"text,omitempty"`
}

func ParseCalendarEventDetail(body []byte) (CalendarEventDetailDTO, error) {
	var dto CalendarEventDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CalendarEventDetailDTO{}, fmt.Errorf("handlers: parsing calendar event detail: %w", err)
	}
	return dto, nil
}

func SyncCalendarEventDetail(ctx context.Context, s *store.Store, characterID, eventID int64, dto CalendarEventDetailDTO) (SyncResult, error) {
	if _, err := s.UpsertCalendarEventDetail(ctx, gen.UpsertCalendarEventDetailParams{
		CharacterID: characterID, EventID: eventID, Text: dto.Text, OwnerID: dto.OwnerID,
		OwnerName: dto.OwnerName, OwnerType: dto.OwnerType, Duration: dto.Duration,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting calendar event detail for event %d of character %d: %w", eventID, characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// CalendarAttendeeDTO mirrors one element of
// GET /characters/{id}/calendar/events/{event_id}/attendees.
type CalendarAttendeeDTO struct {
	CharacterID   int64  `json:"character_id"`
	EventResponse string `json:"event_response"`
}

func ParseCalendarAttendees(body []byte) ([]CalendarAttendeeDTO, error) {
	var dto []CalendarAttendeeDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing calendar attendees: %w", err)
	}
	return dto, nil
}

func SyncCalendarAttendees(ctx context.Context, s *store.Store, characterID, eventID int64, attendees []CalendarAttendeeDTO) (SyncResult, error) {
	for _, a := range attendees {
		resp := a.EventResponse
		if _, err := s.UpsertCalendarEventAttendee(ctx, gen.UpsertCalendarEventAttendeeParams{
			CharacterID: characterID, EventID: eventID, AttendeeCharacterID: a.CharacterID, Response: &resp,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting calendar attendee %d of event %d for character %d: %w", a.CharacterID, eventID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(attendees))}, nil
}
