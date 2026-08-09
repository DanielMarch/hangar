package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterTitleDTO is one element of GET
// /characters/{character_id}/titles (CharactersCharacterIdTitlesGet).
type CharacterTitleDTO struct {
	Name    string `json:"name"`
	TitleID int64  `json:"title_id"`
}

func ParseCharacterTitles(body []byte) ([]CharacterTitleDTO, error) {
	var dto []CharacterTitleDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character titles: %w", err)
	}
	return dto, nil
}

func SyncCharacterTitles(ctx context.Context, s *store.Store, characterID int64, titles []CharacterTitleDTO) (SyncResult, error) {
	ids := make([]int64, len(titles))
	for i, t := range titles {
		ids[i] = t.TitleID
		if _, err := s.ReplaceCharacterTitle(ctx, characterID, t.TitleID, t.Name); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting title %d for character %d: %w", t.TitleID, characterID, err)
		}
	}
	if err := s.DeleteCharacterTitlesNotIn(ctx, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale titles for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(titles))}, nil
}

// CharacterRolesDTO is GET /characters/{character_id}/roles
// (CharactersCharacterIdRolesGet). There is no "grantable" concept on this
// endpoint at all — that's a corporation-level roles concern (Phase 8);
// every row this phase writes has grantable = false, matching
// app.character_role's default and db/queries/character_sheet.sql's
// ReplaceCharacterRole.
type CharacterRolesDTO struct {
	Roles        []string `json:"roles,omitempty"`
	RolesAtBase  []string `json:"roles_at_base,omitempty"`
	RolesAtHQ    []string `json:"roles_at_hq,omitempty"`
	RolesAtOther []string `json:"roles_at_other,omitempty"`
}

func ParseCharacterRoles(body []byte) (CharacterRolesDTO, error) {
	var dto CharacterRolesDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterRolesDTO{}, fmt.Errorf("handlers: parsing character roles: %w", err)
	}
	return dto, nil
}

// roleRow is one (role, at_hq, at_base, at_other) combination this
// endpoint can produce; grantable is always false here.
type roleRow struct {
	Role                  string
	AtHQ, AtBase, AtOther bool
}

func SyncCharacterRoles(ctx context.Context, s *store.Store, characterID int64, dto CharacterRolesDTO) (SyncResult, error) {
	rows := make([]roleRow, 0, len(dto.Roles)+len(dto.RolesAtHQ)+len(dto.RolesAtBase)+len(dto.RolesAtOther))
	for _, r := range dto.Roles {
		rows = append(rows, roleRow{Role: r})
	}
	for _, r := range dto.RolesAtHQ {
		rows = append(rows, roleRow{Role: r, AtHQ: true})
	}
	for _, r := range dto.RolesAtBase {
		rows = append(rows, roleRow{Role: r, AtBase: true})
	}
	for _, r := range dto.RolesAtOther {
		rows = append(rows, roleRow{Role: r, AtOther: true})
	}

	if err := s.DeleteCharacterOwnedRoles(ctx, characterID); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: clearing character-owned roles for character %d: %w", characterID, err)
	}
	for _, rr := range rows {
		if _, err := s.ReplaceCharacterRole(ctx, gen.ReplaceCharacterRoleParams{
			CharacterID: characterID, Role: rr.Role, AtHq: rr.AtHQ, AtBase: rr.AtBase, AtOther: rr.AtOther,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting role %q for character %d: %w", rr.Role, characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}
