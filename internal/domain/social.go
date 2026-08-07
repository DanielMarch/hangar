package domain

import "time"

// Contact mirrors app.contact. Owner spans all three OwnerKind values —
// characters, corporations and alliances all expose a contacts endpoint
// (SRS v3.1 §6.2-§6.4). Standing is a fraction (-10..10), not money.
type Contact struct {
	Owner       Owner
	ContactID   int64
	ContactType string // open vocabulary
	Standing    float64
	IsBlocked   *bool
	IsWatched   *bool
	LabelIDs    []int64
}

// ContactLabel mirrors app.contact_label.
type ContactLabel struct {
	Owner   Owner
	LabelID int64
	Name    string
}

// Standing mirrors app.standing (NPC corp/faction standings, distinct from
// player-to-player Contact above).
type Standing struct {
	Owner    Owner
	FromID   int64
	FromType string // open vocabulary
	Standing float64
}

// Medal mirrors app.medal — a corporation-owned medal definition.
type Medal struct {
	CorporationID int64
	MedalID       int64
	Title         string
	Description   *string
	CreatedAt     *time.Time
	CreatorID     *int64
}

// MedalIssued mirrors app.medal_issued — the corp -> character issuance
// record that also answers a character's own /medals endpoint.
type MedalIssued struct {
	CorporationID int64
	MedalID       int64
	CharacterID   int64
	Reason        *string
	Status        *string
	IssuerID      int64
	IssuedAt      time.Time
}
