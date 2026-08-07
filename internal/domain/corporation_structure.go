package domain

import "time"

// StarbaseDetail mirrors app.starbase_detail (02_DATABASE_SCHEMA.md §5.3,
// verbatim) — the fuel bay data source behind the
// 'corporation.starbase.fuel_low' alert. AttackStandingThreshold is a
// standing fraction, not money.
type StarbaseDetail struct {
	CorporationID           int64
	StarbaseID              int64
	SystemID                int32
	State                   *string // open vocabulary
	FuelBayView             *string // open vocabulary
	AllowAllianceMembers    *bool
	AllowCorporationMembers *bool
	UseAllianceStandings    *bool
	AttackStandingThreshold *float64
	Fuels                   []StarbaseFuel
	ReinforcedUntil         *time.Time
}

// StarbaseFuel is one element of StarbaseDetail.Fuels ([]{type_id, quantity}
// as stored in the fuels jsonb column). Quantity is not money.
type StarbaseFuel struct {
	TypeID   int32 `json:"type_id"`
	Quantity int64 `json:"quantity"`
}

// CorporationStructure mirrors app.corporation_structure.
type CorporationStructure struct {
	CorporationID int64
	StructureID   int64
	TypeID        int32
	SystemID      int32
	ProfileID     *int32
	FuelExpires   *time.Time
	State         *string // open vocabulary
}

// CorporationProjectFuelLowSourceRoute is the build-time-checked route
// operation ID that app.alert_type('corporation.starbase.fuel_low')
// references (02_DATABASE_SCHEMA.md §5.3's comment on starbase_detail).
// Declared here so a future Phase 14 test can assert the alert's
// source_route_id resolves to this operation without hand-typing the
// string in two places.
const CorporationStarbaseFuelLowSourceOperationID = "get_corporations_corporation_id_starbases_starbase_id"
