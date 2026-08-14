// Corporation project sync (Phase 9). project_id is uuid FROM CCP — never
// generated here, never coerced to/from bigint or text (Principle 13's
// proof case, 02_DATABASE_SCHEMA.md §5.3). It joins directly against the
// bigint character_id of app.corporation_project_contribution in the same
// row with no coercion — github.com/google/uuid.UUID end to end, matching
// the sqlc override already in place for every `uuid` column.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/shopspring/decimal"
)

// CorporationProjectDTO mirrors one element of GET /corporations/{id}/projects.
type CorporationProjectDTO struct {
	ContributionType *string             `json:"contribution_type,omitempty"`
	CurrentProgress  decimal.NullDecimal `json:"current_progress"`
	ExpiresAt        time.Time           `json:"expires_at,omitempty"`
	Name             string              `json:"name"`
	ProjectID        uuid.UUID           `json:"project_id"`
	RewardIsk        decimal.NullDecimal `json:"reward_isk"`
	State            string              `json:"state"`
	TargetProgress   decimal.NullDecimal `json:"target_progress"`
}

// corporationProjectsListing is the ENVELOPE GET
// /corporations/{corporation_id}/projects actually returns.
//
// ── DEFECT B49 ───────────────────────────────────────────────────────────
// This was parsed as a bare JSON array and failed on the first real
// response with "json: cannot unmarshal object into Go value of type
// []handlers.CorporationProjectDTO". The spec is unambiguous — the
// operation's 200 response is `#/components/schemas/
// CorporationsProjectsListing`, an OBJECT with a required `projects` array
// and an optional `cursor`. Nearly every other corporation route does
// return a bare array, which is presumably how the assumption got made.
//
// It went undetected because no corporation route had ever executed: the
// acting-character election could not succeed (B44, B45, B46), so this
// parser had never once been handed a real body.
//
// The `cursor` field is captured but not yet followed — this route is
// cursor-paginated (§5.9's second mechanism), and §5.9's cursor
// implementation has no live caller at all. That is defect B31, and
// walking the cursor belongs with it rather than here, so a corporation
// with more than one page of projects currently syncs only the first.
// Recorded rather than silently truncated.
type corporationProjectsListing struct {
	Cursor   *struct{ After *string } `json:"cursor,omitempty"`
	Projects []CorporationProjectDTO  `json:"projects"`
}

func ParseCorporationProjects(body []byte) ([]CorporationProjectDTO, error) {
	var listing corporationProjectsListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation projects: %w", err)
	}
	return listing.Projects, nil
}

func SyncCorporationProjects(ctx context.Context, s *store.Store, corporationID int64, projects []CorporationProjectDTO) (SyncResult, error) {
	for _, p := range projects {
		if _, err := s.UpsertCorporationProject(ctx, gen.UpsertCorporationProjectParams{
			ProjectID: p.ProjectID, CorporationID: corporationID, Name: p.Name, State: p.State,
			ContributionType: p.ContributionType, TargetProgress: p.TargetProgress,
			CurrentProgress: p.CurrentProgress, RewardIsk: p.RewardIsk, ExpiresAt: nilIfZero(p.ExpiresAt),
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting corporation project %s for corporation %d: %w", p.ProjectID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(projects))}, nil
}

// CorporationProjectContributorDTO mirrors one element of
// GET /corporations/{id}/projects/{project_id}/contributors.
type CorporationProjectContributorDTO struct {
	CharacterID int64     `json:"character_id"`
	JoinedAt    time.Time `json:"joined_at,omitempty"`
}

func ParseCorporationProjectContributors(body []byte) ([]CorporationProjectContributorDTO, error) {
	var dto []CorporationProjectContributorDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation project contributors: %w", err)
	}
	return dto, nil
}

func SyncCorporationProjectContributors(ctx context.Context, s *store.Store, projectID uuid.UUID, contributors []CorporationProjectContributorDTO) (SyncResult, error) {
	for _, c := range contributors {
		joined := nilIfZero(c.JoinedAt)
		if _, err := s.UpsertCorporationProjectContributor(ctx, projectID, c.CharacterID, joined); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting corporation project contributor %d of project %s: %w", c.CharacterID, projectID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(contributors))}, nil
}

// CorporationProjectContributionDTO mirrors one element of
// GET /corporations/{id}/projects/{project_id}/contributions — the
// Principle 13 / Gate 6 fixture row: a uuid project_id (path parameter,
// carried down from the caller, not part of this DTO's own JSON) paired
// with a bigint character_id in the same upsert, never coerced through text.
type CorporationProjectContributionDTO struct {
	Amount      decimal.Decimal `json:"amount"`
	CharacterID int64           `json:"character_id"`
}

func ParseCorporationProjectContributions(body []byte) ([]CorporationProjectContributionDTO, error) {
	var dto []CorporationProjectContributionDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation project contributions: %w", err)
	}
	return dto, nil
}

func SyncCorporationProjectContributions(ctx context.Context, s *store.Store, projectID uuid.UUID, contributions []CorporationProjectContributionDTO) (SyncResult, error) {
	for _, c := range contributions {
		if _, err := s.UpsertCorporationProjectContribution(ctx, projectID, c.CharacterID, c.Amount); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting corporation project contribution for character %d of project %s: %w", c.CharacterID, projectID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(contributions))}, nil
}
