package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ownerKindCorporation is app.contact/contact_label/standing's owner_kind
// discriminator for this phase's corp-side sync — mirrors
// character_social.go's ownerKindCharacter constant exactly.
const ownerKindCorporation = "corporation"

// ---- GET /corporations/{corporation_id}/medals ----
// This is the medal DEFINITION endpoint — unlike Phase 7's
// /characters/{id}/medals (issuance only), this one carries the full
// definition including created_at/creator_id, which is exactly what
// social.sql's UpsertMedal COALESCE handling was written for (see that
// query's PHASE 7 FIX comment).

type CorporationMedalDTO struct {
	CreatedAt   time.Time `json:"created_at"`
	CreatorID   int64     `json:"creator_id"`
	Description string    `json:"description"`
	MedalID     int64     `json:"medal_id"`
	Title       string    `json:"title"`
}

func ParseCorporationMedals(body []byte) ([]CorporationMedalDTO, error) {
	var dto []CorporationMedalDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation medals: %w", err)
	}
	return dto, nil
}

func SyncCorporationMedals(ctx context.Context, s *store.Store, corporationID int64, medals []CorporationMedalDTO) (SyncResult, error) {
	for _, m := range medals {
		createdAt, creatorID := m.CreatedAt, m.CreatorID
		if _, err := s.UpsertMedal(ctx, gen.UpsertMedalParams{
			CorporationID: corporationID, MedalID: m.MedalID, Title: m.Title, Description: &m.Description,
			CreatedAt: &createdAt, CreatorID: &creatorID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting medal definition %d for corp %d: %w", m.MedalID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(medals))}, nil
}

// ---- GET /corporations/{corporation_id}/medals/issued ----

type CorporationMedalIssuedDTO struct {
	CharacterID int64     `json:"character_id"`
	IssuedAt    time.Time `json:"issued_at"`
	IssuerID    int64     `json:"issuer_id"`
	MedalID     int64     `json:"medal_id"`
	Reason      string    `json:"reason"`
	Status      string    `json:"status"`
}

func ParseCorporationMedalsIssued(body []byte) ([]CorporationMedalIssuedDTO, error) {
	var dto []CorporationMedalIssuedDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation medals issued: %w", err)
	}
	return dto, nil
}

func SyncCorporationMedalsIssued(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationMedalIssuedDTO) (SyncResult, error) {
	for _, r := range rows {
		reason, status := r.Reason, r.Status
		if _, err := s.InsertMedalIssued(ctx, gen.InsertMedalIssuedParams{
			CorporationID: corporationID, MedalID: r.MedalID, CharacterID: r.CharacterID,
			Reason: &reason, Status: &status, IssuerID: r.IssuerID, IssuedAt: r.IssuedAt,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: inserting medal issuance %d for character %d: %w", r.MedalID, r.CharacterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/standings ----

type CorporationStandingDTO struct {
	FromID   int64   `json:"from_id"`
	FromType string  `json:"from_type"`
	Standing float64 `json:"standing"`
}

func ParseCorporationStandings(body []byte) ([]CorporationStandingDTO, error) {
	var dto []CorporationStandingDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation standings: %w", err)
	}
	return dto, nil
}

func SyncCorporationStandings(ctx context.Context, s *store.Store, corporationID int64, standings []CorporationStandingDTO) (SyncResult, error) {
	ids := make([]int64, len(standings))
	for i, st := range standings {
		ids[i] = st.FromID
		if _, err := s.UpsertStanding(ctx, gen.UpsertStandingParams{
			OwnerKind: ownerKindCorporation, OwnerID: corporationID, FromID: st.FromID, FromType: st.FromType, Standing: st.Standing,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting standing %d for corp %d: %w", st.FromID, corporationID, err)
		}
	}
	if err := s.DeleteStandingsNotIn(ctx, ownerKindCorporation, corporationID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale standings for corp %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: int32(len(standings))}, nil
}

// ---- GET /corporations/{corporation_id}/contacts ----
// No is_blocked field on the corporation variant — unlike
// character_social.go's CharacterContactDTO, ESI's corp-contacts schema
// has no such property at all (not merely omitted-when-false).

type CorporationContactDTO struct {
	ContactID   int64   `json:"contact_id"`
	ContactType string  `json:"contact_type"`
	IsWatched   *bool   `json:"is_watched,omitempty"`
	LabelIDs    []int64 `json:"label_ids,omitempty"`
	Standing    float64 `json:"standing"`
}

func ParseCorporationContacts(body []byte) ([]CorporationContactDTO, error) {
	var dto []CorporationContactDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation contacts: %w", err)
	}
	return dto, nil
}

func SyncCorporationContacts(ctx context.Context, s *store.Store, corporationID int64, contacts []CorporationContactDTO) (SyncResult, error) {
	ids := make([]int64, len(contacts))
	for i, c := range contacts {
		ids[i] = c.ContactID
		labelIDs := c.LabelIDs
		if labelIDs == nil {
			labelIDs = []int64{}
		}
		if _, err := s.UpsertContact(ctx, gen.UpsertContactParams{
			OwnerKind: ownerKindCorporation, OwnerID: corporationID, ContactID: c.ContactID,
			ContactType: c.ContactType, Standing: c.Standing, IsBlocked: nil, IsWatched: c.IsWatched, LabelIds: labelIDs,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact %d for corp %d: %w", c.ContactID, corporationID, err)
		}
	}
	if err := s.DeleteContactsNotIn(ctx, ownerKindCorporation, corporationID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale contacts for corp %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: int32(len(contacts))}, nil
}

// ---- GET /corporations/{corporation_id}/contacts/labels ----

type CorporationContactLabelDTO struct {
	LabelID   int64  `json:"label_id"`
	LabelName string `json:"label_name"`
}

func ParseCorporationContactLabels(body []byte) ([]CorporationContactLabelDTO, error) {
	var dto []CorporationContactLabelDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation contact labels: %w", err)
	}
	return dto, nil
}

func SyncCorporationContactLabels(ctx context.Context, s *store.Store, corporationID int64, labels []CorporationContactLabelDTO) (SyncResult, error) {
	ids := make([]int64, len(labels))
	for i, l := range labels {
		ids[i] = l.LabelID
		if _, err := s.UpsertContactLabel(ctx, gen.UpsertContactLabelParams{
			OwnerKind: ownerKindCorporation, OwnerID: corporationID, LabelID: l.LabelID, Name: l.LabelName,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact label %d for corp %d: %w", l.LabelID, corporationID, err)
		}
	}
	if err := s.DeleteContactLabelsNotIn(ctx, ownerKindCorporation, corporationID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale contact labels for corp %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: int32(len(labels))}, nil
}
