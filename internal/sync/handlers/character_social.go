package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
)

// ignoreUnchanged swallows pgx.ErrNoRows from a `:one` upsert whose
// ON CONFLICT DO UPDATE ... WHERE IS DISTINCT FROM guard (or, for
// medal_issued, a plain ON CONFLICT DO NOTHING) suppressed the write
// because nothing changed — social.sql's UpsertContact/UpsertContactLabel/
// UpsertStanding/UpsertMedal and InsertMedalIssued are all `:one` with
// RETURNING *, so "no row changed" and "a real error" are both possible
// outcomes of the same call and must be told apart here, not upstream.
func ignoreUnchanged(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// ownerKindCharacter is app.contact/contact_label/standing's owner_kind
// discriminator for this phase — the corporation/alliance rows through
// the same tables are Phase 8/9's.
const ownerKindCharacter = "character"

// CharacterContactDTO is one element of GET
// /characters/{character_id}/contacts (CharactersCharacterIdContactsGet).
type CharacterContactDTO struct {
	ContactID   int64   `json:"contact_id"`
	ContactType string  `json:"contact_type"`
	IsBlocked   *bool   `json:"is_blocked,omitempty"`
	IsWatched   *bool   `json:"is_watched,omitempty"`
	LabelIDs    []int64 `json:"label_ids,omitempty"`
	Standing    float64 `json:"standing"`
}

func ParseCharacterContacts(body []byte) ([]CharacterContactDTO, error) {
	var dto []CharacterContactDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character contacts: %w", err)
	}
	return dto, nil
}

func SyncCharacterContacts(ctx context.Context, s *store.Store, characterID int64, contacts []CharacterContactDTO) (SyncResult, error) {
	ids := make([]int64, len(contacts))
	for i, c := range contacts {
		ids[i] = c.ContactID
		labelIDs := c.LabelIDs
		if labelIDs == nil {
			labelIDs = []int64{}
		}
		if _, err := s.UpsertContact(ctx, gen.UpsertContactParams{
			OwnerKind: ownerKindCharacter, OwnerID: characterID, ContactID: c.ContactID,
			ContactType: c.ContactType, Standing: c.Standing, IsBlocked: c.IsBlocked, IsWatched: c.IsWatched, LabelIds: labelIDs,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact %d for character %d: %w", c.ContactID, characterID, err)
		}
	}
	if err := s.DeleteContactsNotIn(ctx, ownerKindCharacter, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale contacts for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(contacts))}, nil
}

// CharacterContactLabelDTO is one element of GET
// /characters/{character_id}/contacts/labels.
type CharacterContactLabelDTO struct {
	LabelID   int64  `json:"label_id"`
	LabelName string `json:"label_name"`
}

func ParseCharacterContactLabels(body []byte) ([]CharacterContactLabelDTO, error) {
	var dto []CharacterContactLabelDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character contact labels: %w", err)
	}
	return dto, nil
}

func SyncCharacterContactLabels(ctx context.Context, s *store.Store, characterID int64, labels []CharacterContactLabelDTO) (SyncResult, error) {
	ids := make([]int64, len(labels))
	for i, l := range labels {
		ids[i] = l.LabelID
		if _, err := s.UpsertContactLabel(ctx, gen.UpsertContactLabelParams{
			OwnerKind: ownerKindCharacter, OwnerID: characterID, LabelID: l.LabelID, Name: l.LabelName,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact label %d for character %d: %w", l.LabelID, characterID, err)
		}
	}
	if err := s.DeleteContactLabelsNotIn(ctx, ownerKindCharacter, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale contact labels for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(labels))}, nil
}

// CharacterStandingDTO is one element of GET
// /characters/{character_id}/standings.
type CharacterStandingDTO struct {
	FromID   int64   `json:"from_id"`
	FromType string  `json:"from_type"`
	Standing float64 `json:"standing"`
}

func ParseCharacterStandings(body []byte) ([]CharacterStandingDTO, error) {
	var dto []CharacterStandingDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character standings: %w", err)
	}
	return dto, nil
}

func SyncCharacterStandings(ctx context.Context, s *store.Store, characterID int64, standings []CharacterStandingDTO) (SyncResult, error) {
	ids := make([]int64, len(standings))
	for i, st := range standings {
		ids[i] = st.FromID
		if _, err := s.UpsertStanding(ctx, gen.UpsertStandingParams{
			OwnerKind: ownerKindCharacter, OwnerID: characterID, FromID: st.FromID, FromType: st.FromType, Standing: st.Standing,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting standing %d for character %d: %w", st.FromID, characterID, err)
		}
	}
	if err := s.DeleteStandingsNotIn(ctx, ownerKindCharacter, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale standings for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(standings))}, nil
}
