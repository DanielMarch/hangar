package domain

import "time"

// CharacterSkill mirrors app.character_skill. Skillpoints is a unit count,
// not money — despite skill injectors having an ISK price, the SP figure
// itself never is.
type CharacterSkill struct {
	CharacterID  int64
	SkillID      int32
	ActiveLevel  int16
	TrainedLevel int16
	Skillpoints  int64
}

// CharacterSkillqueueEntry mirrors app.character_skillqueue.
type CharacterSkillqueueEntry struct {
	CharacterID     int64
	QueuePosition   int32
	SkillID         int32
	FinishedLevel   int16
	TrainingStartSP *int64
	LevelStartSP    *int64
	LevelEndSP      *int64
	StartDate       *time.Time
	FinishDate      *time.Time
}

// CharacterAttributes mirrors app.character_attributes.
type CharacterAttributes struct {
	CharacterID              int64
	Charisma                 int32
	Intelligence             int32
	Memory                   int32
	Perception               int32
	Willpower                int32
	BonusRemaps              *int32
	LastRemapDate            *time.Time
	AccruedRemapCooldownDate *time.Time
}

// CharacterClone mirrors app.character_clone.
type CharacterClone struct {
	CharacterID           int64
	JumpCloneID           int64
	LocationID            int64
	LocationType          string // open vocabulary
	Name                  *string
	Implants              []int32
	IsHomeClone           bool
	LastCloneJumpDate     *time.Time
	LastStationChangeDate *time.Time
}

// CharacterLoyaltyPoint mirrors app.character_loyalty_point. Points are not
// money despite LP stores selling ISK-priced goods.
type CharacterLoyaltyPoint struct {
	CharacterID   int64
	CorporationID int64
	LoyaltyPoints int64
}

// CharacterAgentResearch mirrors app.character_agent_research. Points are
// not money.
type CharacterAgentResearch struct {
	CharacterID     int64
	AgentID         int64
	SkillTypeID     int32
	StartedAt       time.Time
	PointsPerDay    float64
	RemainderPoints float64
}

// CharacterLocation mirrors app.character_location — the collapsed current
// state behind /location, /online and /ship.
type CharacterLocation struct {
	CharacterID   int64
	SolarSystemID int32
	StationID     *int64
	StructureID   *int64
	IsOnline      *bool
	LastLogin     *time.Time
	LastLogout    *time.Time
	Logins        *int64
	ShipItemID    *int64
	ShipTypeID    *int32
	ShipName      *string
}
