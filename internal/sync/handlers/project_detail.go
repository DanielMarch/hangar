// Corporation project DETAIL and per-character CONTRIBUTION sync — the two
// halves of Appendix A capability #25 that had no handler.
//
// ── PHASE 20.7 (B48) ─────────────────────────────────────────────────────
// The project LIST and CONTRIBUTORS routes were wired in Phase 20.5. The
// project DETAIL route and the per-character CONTRIBUTION route were not,
// which is why app.corporation_project.contribution_type and .expires_at had
// no writer at all — neither field appears on the list route (defect B50,
// project_sync.go).
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/hangar-project/hangar/internal/store"
)

// CorporationProjectDetailDTO is GET
// /corporations/{corporation_id}/projects/{project_id}.
//
// Only the fields HANGAR has columns for are decoded. `creator`,
// `contribution` (the per-submission limits and multipliers) and the bulk of
// `configuration` are deliberately not modelled: there is nowhere to put
// them, and a DTO field that lands nowhere is a promise the schema does not
// keep.
type CorporationProjectDetailDTO struct {
	Configuration map[string]json.RawMessage `json:"configuration"`
	Details       ProjectDetails             `json:"details"`
	ID            uuid.UUID                  `json:"id"`
	Name          string                     `json:"name"`
	State         string                     `json:"state"`
}

// ProjectDetails is the detail response's `details` object.
type ProjectDetails struct {
	Career      string     `json:"career"`
	Created     time.Time  `json:"created"`
	Description string     `json:"description"`
	Expires     *time.Time `json:"expires,omitempty"`
	Finished    *time.Time `json:"finished,omitempty"`
}

func ParseCorporationProjectDetail(body []byte) (CorporationProjectDetailDTO, error) {
	var dto CorporationProjectDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationProjectDetailDTO{}, fmt.Errorf("handlers: parsing corporation project detail: %w", err)
	}
	return dto, nil
}

// ContributionType reads the project's contribution kind out of the
// `configuration` object.
//
// ── WHY A MAP AND NOT A TYPED oneOf ──────────────────────────────────────
// `configuration` is a seventeen-branch oneOf — capture_fw_complex,
// damage_ship, deliver_item, destroy_npc, manufacture_item, mine_material,
// ... and `unknown` — where each branch is an object with exactly one
// property, and that property's NAME is the contribution type. HANGAR stores
// only that name (app.corporation_project.contribution_type is text), so
// decoding into a map and reading its single key gets the whole of what the
// column needs without seventeen structs whose bodies nothing reads.
//
// CCP's own `unknown` branch is the reason the vocabulary stays open text
// rather than becoming an enum: they ship a value that means "a kind this
// spec version does not name", so any closed set HANGAR wrote would be wrong
// by construction the first time they added a project type.
//
// A configuration with no keys, or more than one, returns "" — a shape that
// contradicts the oneOf, and guessing which key to believe would be worse
// than recording nothing.
func (d CorporationProjectDetailDTO) ContributionType() *string {
	if len(d.Configuration) != 1 {
		return nil
	}
	for k := range d.Configuration {
		key := k
		return &key
	}
	return nil
}

// SyncCorporationProjectDetail writes the two columns only this route
// carries. It does NOT re-write name/state/progress: those belong to the
// list route, which is authoritative for them and runs on its own cadence.
//
// A project the list sync has not landed yet is not an error — the UPDATE
// simply matches no row. That is the correct outcome for a detail fetched
// for a project that has since been pruned, and it is why this is an UPDATE
// rather than an upsert that could resurrect a deleted project as a row with
// nothing but an expiry.
func SyncCorporationProjectDetail(ctx context.Context, s *store.Store, projectID uuid.UUID, dto CorporationProjectDetailDTO) (SyncResult, error) {
	if err := s.UpdateCorporationProjectDetail(ctx, projectID, dto.ContributionType(), dto.Details.Expires); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: updating detail of project %s: %w", projectID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// CorporationProjectContributionDTO is GET
// .../projects/{project_id}/contribution/{character_id} — "Your
// contribution", one character's contributed PROGRESS toward one project.
//
// `contributed` is a progress count, not isk, for the same reason the
// contributors route's identically-named field is (project_sync.go's long
// note). It lands in app.corporation_project_contribution.amount, which is
// the same column the contributors route writes.
type CorporationProjectContributionDTO struct {
	Contributed  int64      `json:"contributed"`
	LastModified *time.Time `json:"last_modified,omitempty"`
}

func ParseCorporationProjectContribution(body []byte) (CorporationProjectContributionDTO, error) {
	var dto CorporationProjectContributionDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationProjectContributionDTO{}, fmt.Errorf("handlers: parsing corporation project contribution: %w", err)
	}
	return dto, nil
}

// SyncCorporationProjectContribution records one character's contribution.
//
// ── WHY THIS ROUTE EXISTS ALONGSIDE THE CONTRIBUTORS ROUTE ───────────────
// The contributors route returns every contributor's total in one call, and
// writes the same column. This one answers for a SINGLE character, and CCP
// documents it as "your contribution" — it is the route a member's own view
// is entitled to when the corporation-wide contributors list is not
// something that member may see. HANGAR syncs both because they can disagree
// legitimately: a corporation whose acting character loses the roles for the
// full list keeps this one working for the characters it holds tokens for.
//
// Last writer wins, and that is correct — both report the same underlying
// quantity, so the fresher call is the better answer regardless of which
// route it came from.
func SyncCorporationProjectContribution(ctx context.Context, s *store.Store, projectID uuid.UUID, characterID int64, dto CorporationProjectContributionDTO) (SyncResult, error) {
	if _, err := s.UpsertCorporationProjectContribution(ctx, projectID, characterID, decimal.NewFromInt(dto.Contributed)); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting contribution of character %d to project %s: %w", characterID, projectID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}
