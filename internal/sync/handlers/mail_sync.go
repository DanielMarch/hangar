// Mail sync (Phase 9): headers, recipients, labels, lists — plus the body
// fetch DTO/sync pair. The body FETCH itself (one ESI request per mail,
// routed through the catalogue rather than an inline URL — roadmap edge
// case, TestMailBodyRoutedThroughCatalogue) lives in
// internal/sync/worker/character.go's doMailBodyFanout, which calls
// SyncMailBody below with the already-fetched response body. This file
// only ever receives bytes; it never builds an HTTP request itself.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// MailHeaderRecipientDTO mirrors one element of a mail header's
// `recipients` array.
type MailHeaderRecipientDTO struct {
	RecipientID   int64  `json:"recipient_id"`
	RecipientType string `json:"recipient_type"`
}

// MailHeaderDTO mirrors one element of GET /characters/{id}/mail.
type MailHeaderDTO struct {
	From       *int64                   `json:"from,omitempty"`
	IsRead     *bool                    `json:"is_read,omitempty"`
	Labels     []int64                  `json:"labels,omitempty"`
	MailID     int64                    `json:"mail_id"`
	Recipients []MailHeaderRecipientDTO `json:"recipients,omitempty"`
	Subject    *string                  `json:"subject,omitempty"`
	Timestamp  time.Time                `json:"timestamp"`
}

func ParseMailHeaders(body []byte) ([]MailHeaderDTO, error) {
	var dto []MailHeaderDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing mail headers: %w", err)
	}
	return dto, nil
}

func SyncMailHeaders(ctx context.Context, s *store.Store, characterID int64, headers []MailHeaderDTO) (SyncResult, error) {
	for _, h := range headers {
		labels := h.Labels
		if labels == nil {
			labels = []int64{}
		}
		if _, err := s.UpsertMailHeader(ctx, gen.UpsertMailHeaderParams{
			CharacterID: characterID, MailID: h.MailID, FromID: h.From, Subject: h.Subject,
			SentAt: h.Timestamp, IsRead: h.IsRead, Labels: labels,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mail header %d for character %d: %w", h.MailID, characterID, err)
		}
		for _, r := range h.Recipients {
			if _, err := s.InsertMailRecipient(ctx, gen.InsertMailRecipientParams{
				CharacterID: characterID, MailID: h.MailID, RecipientID: r.RecipientID, RecipientType: r.RecipientType,
			}); err != nil {
				return SyncResult{}, fmt.Errorf("handlers: inserting mail recipient %d of mail %d for character %d: %w", r.RecipientID, h.MailID, characterID, err)
			}
		}
	}
	return SyncResult{RowsAffected: int32(len(headers))}, nil
}

// MailBodyDTO mirrors GET /characters/{id}/mail/{mail_id}.
type MailBodyDTO struct {
	Body string `json:"body"`
}

func ParseMailBody(body []byte) (MailBodyDTO, error) {
	var dto MailBodyDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return MailBodyDTO{}, fmt.Errorf("handlers: parsing mail body: %w", err)
	}
	return dto, nil
}

func SyncMailBody(ctx context.Context, s *store.Store, characterID, mailID int64, dto MailBodyDTO) (SyncResult, error) {
	if _, err := s.UpsertMailBody(ctx, characterID, mailID, dto.Body); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting mail body for mail %d of character %d: %w", mailID, characterID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// MailLabelDTO mirrors one element of GET /characters/{id}/mail/labels's
// `labels` array.
type MailLabelDTO struct {
	Color       *string `json:"color,omitempty"`
	LabelID     int64   `json:"label_id"`
	Name        string  `json:"name"`
	UnreadCount *int32  `json:"unread_count,omitempty"`
}

// MailLabelsResponseDTO mirrors the whole GET /characters/{id}/mail/labels
// response — labels plus the mailing lists the character belongs to arrive
// as one payload on this endpoint, unlike headers/body/labels-only, which
// is why this DTO exists separately from MailLabelDTO.
type MailLabelsResponseDTO struct {
	Labels      []MailLabelDTO `json:"labels"`
	TotalUnread *int32         `json:"total_unread_count,omitempty"`
}

func ParseMailLabels(body []byte) (MailLabelsResponseDTO, error) {
	var dto MailLabelsResponseDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return MailLabelsResponseDTO{}, fmt.Errorf("handlers: parsing mail labels: %w", err)
	}
	return dto, nil
}

func SyncMailLabels(ctx context.Context, s *store.Store, characterID int64, dto MailLabelsResponseDTO) (SyncResult, error) {
	for _, l := range dto.Labels {
		if _, err := s.UpsertMailLabel(ctx, gen.UpsertMailLabelParams{
			CharacterID: characterID, LabelID: l.LabelID, Name: l.Name, Color: l.Color, UnreadCount: l.UnreadCount,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mail label %d for character %d: %w", l.LabelID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(dto.Labels))}, nil
}

// MailListDTO mirrors one element of GET /characters/{id}/mail/lists.
type MailListDTO struct {
	ListID int64  `json:"mailing_list_id"`
	Name   string `json:"name"`
}

func ParseMailLists(body []byte) ([]MailListDTO, error) {
	var dto []MailListDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing mail lists: %w", err)
	}
	return dto, nil
}

func SyncMailLists(ctx context.Context, s *store.Store, characterID int64, lists []MailListDTO) (SyncResult, error) {
	for _, l := range lists {
		if _, err := s.UpsertMailList(ctx, characterID, l.ListID, l.Name); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mail list %d for character %d: %w", l.ListID, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(lists))}, nil
}
