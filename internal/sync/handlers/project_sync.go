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

// ── DEFECT B38's LEFTOVER, CLOSED IN PHASE 20.5 ──────────────────────────
//
// GET /corporations/{id}/projects/{project_id}/contributors was parsed with
// the DTO of a route that does not exist. B38 (Phase 20.2) renamed the path
// from `.../contributions` — a spelling ESI has never had — and left
// ParseCorporationProjectContributions, which expects a bare array of
// {amount, character_id}, attached to it. The parser had never once run: the
// route was not subscribable (B30), so no body ever reached it.
//
// READ FROM THE LIVE SPEC, not from the field names. `#/components/schemas/
// CorporationsProjectsContributors` is an OBJECT:
//
//	{ "contributors": [ {id, name, contributed} ], "cursor": {after, before} }
//
// with `contributors` required, and each element's three fields all
// required. The two answers this phase had to record:
//
//   - `id` is `$ref: "#/components/schemas/CharacterID"`, described by CCP
//     as "Contributor's character ID". It is a CHARACTER, not a project
//     member row, not an opaque contributor handle. It maps to the bigint
//     `character_id` of BOTH app.corporation_project_contributor and
//     app.corporation_project_contribution — which is what makes this route
//     the Principle 13 / Gate 6 fixture it was always meant to be: a
//     CCP-issued uuid project_id joined against a bigint character_id in one
//     upsert, with no coercion through text at any point.
//
//   - `contributed` is `type: integer, format: int64`, described as
//     "Contributor's contributed PROGRESS". It is NOT isk. The project's own
//     isk field is `reward_isk` (what the corporation pays out), and its
//     progress fields are `current_progress`/`target_progress`, which is the
//     scale `contributed` is denominated in — the sibling route
//     `.../contribution/{character_id}` returns the same field, "Your
//     contribution", for one character. So `contributed` maps to
//     app.corporation_project_contribution.amount, and `amount` is a
//     PROGRESS COUNT in that table, not money. numeric(30,2) holds an int64
//     exactly, so nothing is lost; the column is deliberately NOT renamed,
//     because renaming a shipped column to improve a comment is a migration
//     nobody's data benefits from. Recorded here instead, which is where
//     somebody reading the sync would look.
//
// ONE RESPONSE, TWO TABLES. 00011 created `contributor` ("the roster of
// characters participating, distinct from the amounts in #3") and
// `contribution` (the amounts) as if two routes fed them. There is one
// route, and it carries both facts, so it writes both rows in one pass.
// joined_at stays NULL: ESI does not report when a contributor joined, and
// stamping now() would date every membership to the moment HANGAR first
// looked at it — a fabricated fact that would then age and look real.
// `name` is deliberately not persisted anywhere: app.character is written by
// the character-sheet sync for characters HANGAR holds a token for, and
// inventing rows there from a projects response would manufacture characters
// out of a display string.
type CorporationProjectContributorDTO struct {
	// Contributed is the contributor's contributed PROGRESS, int64 in the
	// spec — see this block's header for why it is not money.
	Contributed int64 `json:"contributed"`
	// ID is the contributor's CHARACTER id (spec: $ref CharacterID).
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// corporationProjectContributors is the ENVELOPE the route actually returns.
// The cursor is captured by the worker's cursor walker (§5.9), which merges
// every page into one envelope of this same shape before this parser sees
// it — so the field is present, always empty by then, and read by nobody
// here.
type corporationProjectContributors struct {
	Contributors []CorporationProjectContributorDTO `json:"contributors"`
	Cursor       *struct{ After *string }           `json:"cursor,omitempty"`
}

// ProjectContributorsItemsField is the array key inside that envelope,
// verbatim from the spec. Exported so worker/pagination.go's cursor merge
// and this parser cannot disagree about it — the last time a path and its
// parser were kept in two places, they drifted for three phases.
const ProjectContributorsItemsField = "contributors"

func ParseCorporationProjectContributors(body []byte) ([]CorporationProjectContributorDTO, error) {
	var env corporationProjectContributors
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation project contributors: %w", err)
	}
	return env.Contributors, nil
}

// SyncCorporationProjectContributors writes the roster row and the progress
// row for each contributor. Both are upserts keyed on (project_id,
// character_id), so a contributor whose progress has not moved since the
// last poll writes nothing (each query's own IS DISTINCT FROM guard).
func SyncCorporationProjectContributors(ctx context.Context, s *store.Store, projectID uuid.UUID, contributors []CorporationProjectContributorDTO) (SyncResult, error) {
	var rows int32
	for _, c := range contributors {
		if _, err := s.UpsertCorporationProjectContributor(ctx, projectID, c.ID, nil); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting corporation project contributor %d of project %s: %w", c.ID, projectID, err)
		}
		if _, err := s.UpsertCorporationProjectContribution(ctx, projectID, c.ID, decimal.NewFromInt(c.Contributed)); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting corporation project contribution for character %d of project %s: %w", c.ID, projectID, err)
		}
		rows++
	}
	return SyncResult{RowsAffected: rows}, nil
}
