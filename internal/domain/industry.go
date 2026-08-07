package domain

import "time"

// IndustryJob mirrors app.industry_job. Runs, LicensedRuns and
// SuccessfulRuns are explicitly not money (§3.1); Cost is.
type IndustryJob struct {
	Owner                Owner
	JobID                int64
	InstallerID          int64
	FacilityID           int64
	StationID            int64
	ActivityID           int32
	BlueprintID          int64
	BlueprintTypeID      int32
	BlueprintLocationID  int64
	OutputLocationID     int64
	Runs                 int32
	Cost                 *Money
	LicensedRuns         *int32
	Probability          *float64
	ProductTypeID        *int32
	Status               string // open vocabulary
	Duration             int32
	StartDate            time.Time
	EndDate              time.Time
	PauseDate            *time.Time
	CompletedDate        *time.Time
	CompletedCharacterID *int64
	SuccessfulRuns       *int32
}

// Blueprint mirrors app.blueprint. Quantity, Runs, ME/TE are not money.
type Blueprint struct {
	Owner              Owner
	ItemID             int64
	TypeID             int32
	LocationID         int64
	LocationFlag       string
	Quantity           int64
	TimeEfficiency     int16
	MaterialEfficiency int16
	Runs               int32
}

// MiningLedgerEntry mirrors app.mining_ledger (a character's personal
// mining ledger, /characters/{id}/mining). Quantity is not money.
type MiningLedgerEntry struct {
	Owner         Owner
	Date          time.Time
	SolarSystemID int32
	TypeID        int32
	Quantity      int64
}
