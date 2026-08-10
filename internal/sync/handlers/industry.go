// Industry sync (industry jobs, blueprints) is owner-kind-generic, same
// rationale as wallet.go.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/shopspring/decimal"
)

// ---- GET /{owner}/{id}/industry/jobs ----
// "still active" vs "historical/delivered" (roadmap edge case) is the
// `status` open-vocabulary field itself ('active'|'paused'|'ready'|
// 'delivered'|'cancelled'|'reverted') — ESI returns both in one list
// unless include_completed=false narrows it; this phase does not collapse
// the distinction, it stores status verbatim.
type IndustryJobDTO struct {
	ActivityID           int32               `json:"activity_id"`
	BlueprintID          int64               `json:"blueprint_id"`
	BlueprintLocationID  int64               `json:"blueprint_location_id"`
	BlueprintTypeID      int32               `json:"blueprint_type_id"`
	CompletedCharacterID *int64              `json:"completed_character_id,omitempty"`
	CompletedDate        time.Time           `json:"completed_date,omitempty"`
	Cost                 decimal.NullDecimal `json:"cost"`
	Duration             int32               `json:"duration"`
	EndDate              time.Time           `json:"end_date"`
	FacilityID           int64               `json:"facility_id"`
	InstallerID          int64               `json:"installer_id"`
	JobID                int64               `json:"job_id"`
	LicensedRuns         *int32              `json:"licensed_runs,omitempty"`
	LocationID           int64               `json:"location_id"`
	OutputLocationID     int64               `json:"output_location_id"`
	PauseDate            time.Time           `json:"pause_date,omitempty"`
	Probability          *float64            `json:"probability,omitempty"`
	ProductTypeID        *int32              `json:"product_type_id,omitempty"`
	Runs                 int32               `json:"runs"`
	StartDate            time.Time           `json:"start_date"`
	Status               string              `json:"status"`
	SuccessfulRuns       *int32              `json:"successful_runs,omitempty"`
}

func ParseIndustryJobs(body []byte) ([]IndustryJobDTO, error) {
	var dto []IndustryJobDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing industry jobs: %w", err)
	}
	return dto, nil
}

// SyncIndustryJobs upserts every job. `station_id` (app.industry_job's
// column) has no source field on this endpoint at all in the live spec —
// only `location_id`/`blueprint_location_id`/`output_location_id`. Rather
// than leave a NOT NULL column unfillable, `location_id` is written into
// `station_id` too (the two concepts are the same "where the job runs"
// value under ESI's older/newer field naming — SeAT's legacy
// Jobs/Industry job maps them identically). Reported as a naming
// discrepancy between 02_DATABASE_SCHEMA.md's column name and the live
// spec's field name, not a missing value.
func SyncIndustryJobs(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, jobs []IndustryJobDTO) (SyncResult, error) {
	for _, j := range jobs {
		if _, err := s.UpsertIndustryJob(ctx, gen.UpsertIndustryJobParams{
			OwnerKind: ownerKind, OwnerID: ownerID, JobID: j.JobID, InstallerID: j.InstallerID,
			FacilityID: j.FacilityID, StationID: j.LocationID, ActivityID: j.ActivityID,
			BlueprintID: j.BlueprintID, BlueprintTypeID: j.BlueprintTypeID, BlueprintLocationID: j.BlueprintLocationID,
			OutputLocationID: j.OutputLocationID, Runs: j.Runs, Cost: j.Cost, LicensedRuns: j.LicensedRuns,
			Probability: j.Probability, ProductTypeID: j.ProductTypeID, Status: j.Status, Duration: j.Duration,
			StartDate: j.StartDate, EndDate: j.EndDate, PauseDate: nilIfZero(j.PauseDate),
			CompletedDate: nilIfZero(j.CompletedDate), CompletedCharacterID: j.CompletedCharacterID,
			SuccessfulRuns: j.SuccessfulRuns,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting industry job %d for %s %d: %w", j.JobID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(jobs))}, nil
}

// ---- GET /{owner}/{id}/blueprints ----

type BlueprintDTO struct {
	ItemID             int64  `json:"item_id"`
	LocationFlag       string `json:"location_flag"`
	LocationID         int64  `json:"location_id"`
	MaterialEfficiency int16  `json:"material_efficiency"`
	Quantity           int64  `json:"quantity"`
	Runs               int32  `json:"runs"`
	TimeEfficiency     int16  `json:"time_efficiency"`
	TypeID             int32  `json:"type_id"`
}

func ParseBlueprints(body []byte) ([]BlueprintDTO, error) {
	var dto []BlueprintDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing blueprints: %w", err)
	}
	return dto, nil
}

func SyncBlueprints(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, blueprints []BlueprintDTO) (SyncResult, error) {
	for _, b := range blueprints {
		if _, err := s.UpsertBlueprint(ctx, gen.UpsertBlueprintParams{
			OwnerKind: ownerKind, OwnerID: ownerID, ItemID: b.ItemID, TypeID: b.TypeID,
			LocationID: b.LocationID, LocationFlag: b.LocationFlag, Quantity: b.Quantity,
			TimeEfficiency: b.TimeEfficiency, MaterialEfficiency: b.MaterialEfficiency, Runs: b.Runs,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting blueprint %d for %s %d: %w", b.ItemID, ownerKind, ownerID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(blueprints))}, nil
}
