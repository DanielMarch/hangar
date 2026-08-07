package domain

import "time"

// Killmail mirrors app.killmail. DamageTaken is a raw HP figure — not
// money, despite loss value being a common UI derivation from it.
type Killmail struct {
	Owner             Owner
	KillmailID        int64
	KillmailHash      string
	KillmailTime      time.Time
	SolarSystemID     int32
	MoonID            *int64
	WarID             *int64
	VictimCharacterID *int64
	VictimCorpID      *int64
	VictimAllianceID  *int64
	VictimFactionID   *int32
	VictimShipTypeID  int32
	VictimDamageTaken int64
	VictimX           *float64
	VictimY           *float64
	VictimZ           *float64
}

// KillmailAttacker mirrors app.killmail_attacker. DamageDone is not money.
type KillmailAttacker struct {
	Owner          Owner
	KillmailID     int64
	KillmailTime   time.Time
	RecordID       int64
	CharacterID    *int64
	CorporationID  *int64
	AllianceID     *int64
	FactionID      *int32
	DamageDone     int64
	FinalBlow      bool
	SecurityStatus *float64
	ShipTypeID     *int32
	WeaponTypeID   *int32
}

// KillmailItem mirrors app.killmail_item.
type KillmailItem struct {
	Owner             Owner
	KillmailID        int64
	KillmailTime      time.Time
	RecordID          int64
	ParentRecordID    *int64
	TypeID            int32
	Flag              int32
	QuantityDropped   *int64
	QuantityDestroyed *int64
	Singleton         *int32
}
