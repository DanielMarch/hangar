package domain

import "time"

// CalendarEvent mirrors app.calendar_event.
type CalendarEvent struct {
	CharacterID   int64
	EventID       int64
	Title         string
	EventDate     time.Time
	EventResponse *string // open vocabulary
	Importance    *int32
}

// CalendarEventDetail mirrors app.calendar_event_detail.
type CalendarEventDetail struct {
	CharacterID int64
	EventID     int64
	Text        *string
	OwnerID     *int64
	OwnerName   *string
	OwnerType   *string // open vocabulary
	Duration    *int32
}

// PlanetColony mirrors app.planet_colony. UpgradeLevel and NumPins are not
// money.
type PlanetColony struct {
	CharacterID   int64
	PlanetID      int64
	SolarSystemID int32
	PlanetType    string // open vocabulary
	OwnerID       int64
	LastUpdate    time.Time
	UpgradeLevel  int32
	NumPins       int32
}

// SovereigntyCampaign mirrors app.sovereignty_campaign. Scores are not
// money.
type SovereigntyCampaign struct {
	CampaignID      int64
	ConstellationID int32
	SolarSystemID   int32
	StructureID     *int64
	DefenderID      *int64
	EventType       string // open vocabulary
	StartTime       time.Time
	AttackersScore  *float64
	DefenderScore   *float64
}

// SovereigntySystem mirrors app.sovereignty_system.
type SovereigntySystem struct {
	SystemID      int32
	AllianceID    *int64
	CorporationID *int64
	FactionID     *int32
}
