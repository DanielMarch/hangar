package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ---- GET /corporations/{corporation_id} ----

// CorporationSheetDTO is GET /corporations/{corporation_id}
// (CorporationsCorporationIdGet). `palette`/`tax_rates` are nested objects
// parsed for field-loss coverage; only tax_rates.isk maps onto
// app.corporation.tax_rate (a loyalty-point tax rate has no column — no
// capability in Appendix A needs it).
type CorporationSheetDTO struct {
	AllianceID        *int64    `json:"alliance_id,omitempty"`
	CeoID             int64     `json:"ceo_id"`
	CreatorID         *int64    `json:"creator_id,omitempty"`
	DateFounded       time.Time `json:"date_founded,omitempty"`
	Description       string    `json:"description"`
	EnlistedFactionID *int32    `json:"enlisted_faction_id,omitempty"`
	FriendlyFire      string    `json:"friendly_fire"`
	HomeStationID     int64     `json:"home_station_id"`
	// MemberCount: the live spec declares `format: int64` (CCP applies that
	// format liberally to every integer field, not just genuinely large
	// ones), but app.corporation.member_count is `integer` (int32) — the
	// same int32-for-counts convention every Phase 1b table uses for
	// type_id/system_id (max real EVE values are well under 2^31). Not a
	// bigint/int32 identifier mismatch in the Phase 7 sense (member_count
	// isn't an "_id" column at all), so kept as-is rather than widened.
	MemberCount int32           `json:"member_count"`
	Name        string          `json:"name"`
	Palette     *CorpPaletteDTO `json:"palette,omitempty"`
	Shares      int64           `json:"shares"`
	State       string          `json:"state"`
	TaxRates    CorpTaxRatesDTO `json:"tax_rates"`
	Ticker      string          `json:"ticker"`
	Type        string          `json:"type"`
	URL         *string         `json:"url,omitempty"`
	WarEligible bool            `json:"war_eligible"`
}

type CorpPaletteDTO struct {
	MainColor      *string `json:"main_color,omitempty"`
	SecondaryColor *string `json:"secondary_color,omitempty"`
	TertiaryColor  *string `json:"tertiary_color,omitempty"`
}

type CorpTaxRatesDTO struct {
	ISK          float64  `json:"isk"`
	LoyaltyPoint *float64 `json:"loyalty_point,omitempty"`
}

func ParseCorporationSheet(body []byte) (CorporationSheetDTO, error) {
	var dto CorporationSheetDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationSheetDTO{}, fmt.Errorf("handlers: parsing corporation sheet: %w", err)
	}
	return dto, nil
}

func SyncCorporationSheet(ctx context.Context, s *store.Store, corporationID int64, dto CorporationSheetDTO) (SyncResult, error) {
	// Same reasoning as SyncCharacterSheet (character_identity.go):
	// app.corporation.alliance_id is a foreign key, and this is the first
	// sync to ever set it for a corp fetched independently of any member
	// character — a corp whose alliance HANGAR has never seen needs a stub
	// row first (UpsertAllianceStub's ON CONFLICT DO NOTHING never
	// overwrites a real row Phase 8's own alliance sync already wrote).
	if dto.AllianceID != nil {
		if err := s.UpsertAllianceStub(ctx, *dto.AllianceID, ""); err != nil {
			return SyncResult{}, fmt.Errorf("handlers: stubbing alliance %d for corporation %d: %w", *dto.AllianceID, corporationID, err)
		}
	}

	taxRate := dto.TaxRates.ISK
	if _, err := s.UpsertCorporation(ctx, gen.UpsertCorporationParams{
		CorporationID: corporationID, Name: dto.Name, Ticker: dto.Ticker,
		CeoID: &dto.CeoID, AllianceID: dto.AllianceID,
		MemberCount: &dto.MemberCount, Description: &dto.Description, TaxRate: &taxRate, DateFounded: nilIfZero(dto.DateFounded),
		CreatorID: dto.CreatorID, Url: dto.URL, FactionID: dto.EnlistedFactionID,
		HomeStationID: &dto.HomeStationID, Shares: &dto.Shares, WarEligible: &dto.WarEligible,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting corporation sheet %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// ---- GET /corporations/{corporation_id}/members ----
// Bare array of character_id — no wrapper object at all.

func ParseCorporationMembers(body []byte) ([]int64, error) {
	var dto []int64
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation members: %w", err)
	}
	return dto, nil
}

func SyncCorporationMembers(ctx context.Context, s *store.Store, corporationID int64, memberIDs []int64) (SyncResult, error) {
	for _, characterID := range memberIDs {
		if _, err := s.UpsertCorporationMember(ctx, corporationID, characterID); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting corporation member %d for corp %d: %w", characterID, corporationID, err)
		}
	}
	if err := s.DeleteCorporationMembersNotIn(ctx, corporationID, memberIDs); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale members for corp %d: %w", corporationID, err)
	}
	return SyncResult{RowsAffected: int32(len(memberIDs))}, nil
}

// ---- GET /corporations/{corporation_id}/membertracking (needs Director role) ----

type CorporationMemberTrackingDTO struct {
	BaseID      *int64    `json:"base_id,omitempty"`
	CharacterID int64     `json:"character_id"`
	LocationID  *int64    `json:"location_id,omitempty"`
	LogoffDate  time.Time `json:"logoff_date,omitempty"`
	LogonDate   time.Time `json:"logon_date,omitempty"`
	ShipTypeID  *int32    `json:"ship_type_id,omitempty"`
	StartDate   time.Time `json:"start_date,omitempty"`
}

func ParseCorporationMemberTracking(body []byte) ([]CorporationMemberTrackingDTO, error) {
	var dto []CorporationMemberTrackingDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation member tracking: %w", err)
	}
	return dto, nil
}

func SyncCorporationMemberTracking(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationMemberTrackingDTO) (SyncResult, error) {
	for _, r := range rows {
		if _, err := s.UpsertCorporationMemberTracking(ctx, gen.UpsertCorporationMemberTrackingParams{
			CorporationID: corporationID, CharacterID: r.CharacterID, BaseID: r.BaseID,
			LocationID: r.LocationID, LogoffDate: nilIfZero(r.LogoffDate), LogonDate: nilIfZero(r.LogonDate),
			ShipTypeID: r.ShipTypeID, StartDate: nilIfZero(r.StartDate),
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting member tracking for character %d: %w", r.CharacterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/members/titles ----

type CorporationMemberTitlesDTO struct {
	CharacterID int64   `json:"character_id"`
	Titles      []int64 `json:"titles"`
}

func ParseCorporationMemberTitles(body []byte) ([]CorporationMemberTitlesDTO, error) {
	var dto []CorporationMemberTitlesDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation member titles: %w", err)
	}
	return dto, nil
}

func SyncCorporationMemberTitles(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationMemberTitlesDTO) (SyncResult, error) {
	n := int32(0)
	for _, r := range rows {
		for _, titleID := range r.Titles {
			if _, err := s.ReplaceCorporationMemberTitle(ctx, corporationID, titleID, r.CharacterID); ignoreUnchanged(err) != nil {
				return SyncResult{}, fmt.Errorf("handlers: assigning title %d to character %d: %w", titleID, r.CharacterID, err)
			}
			n++
		}
	}
	return SyncResult{RowsAffected: n}, nil
}

// ---- GET /corporations/{corporation_id}/titles ----
// The endpoint carries per-title role grants (roles/grantable_roles and
// their _at_hq/_at_base/_at_other variants); app.corporation_title only
// models title identity (corporation_id, title_id, name) — Phase 1b's
// schema has no column for "which roles this title grants". Parsed below
// for field-loss coverage, deliberately not persisted, same precedent as
// Phase 7's medal "graphics" field (character_medals.go). Reported as a
// specification gap rather than worked around by inventing new columns
// outside this phase's authorized schema surface.
type CorporationTitleDTO struct {
	GrantableRoles        []string `json:"grantable_roles,omitempty"`
	GrantableRolesAtBase  []string `json:"grantable_roles_at_base,omitempty"`
	GrantableRolesAtHQ    []string `json:"grantable_roles_at_hq,omitempty"`
	GrantableRolesAtOther []string `json:"grantable_roles_at_other,omitempty"`
	Name                  *string  `json:"name,omitempty"`
	Roles                 []string `json:"roles,omitempty"`
	RolesAtBase           []string `json:"roles_at_base,omitempty"`
	RolesAtHQ             []string `json:"roles_at_hq,omitempty"`
	RolesAtOther          []string `json:"roles_at_other,omitempty"`
	TitleID               *int64   `json:"title_id,omitempty"`
}

func ParseCorporationTitles(body []byte) ([]CorporationTitleDTO, error) {
	var dto []CorporationTitleDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation titles: %w", err)
	}
	return dto, nil
}

func SyncCorporationTitles(ctx context.Context, s *store.Store, corporationID int64, titles []CorporationTitleDTO) (SyncResult, error) {
	n := int32(0)
	for _, t := range titles {
		if t.TitleID == nil {
			continue
		}
		name := ""
		if t.Name != nil {
			name = *t.Name
		}
		if _, err := s.UpsertCorporationTitle(ctx, corporationID, *t.TitleID, name); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting title %d for corp %d: %w", *t.TitleID, corporationID, err)
		}
		n++
	}
	return SyncResult{RowsAffected: n}, nil
}

// ---- GET /corporations/{corporation_id}/roles ----

type CorporationRolesDTO struct {
	CharacterID           int64    `json:"character_id"`
	GrantableRoles        []string `json:"grantable_roles,omitempty"`
	GrantableRolesAtBase  []string `json:"grantable_roles_at_base,omitempty"`
	GrantableRolesAtHQ    []string `json:"grantable_roles_at_hq,omitempty"`
	GrantableRolesAtOther []string `json:"grantable_roles_at_other,omitempty"`
	Roles                 []string `json:"roles,omitempty"`
	RolesAtBase           []string `json:"roles_at_base,omitempty"`
	RolesAtHQ             []string `json:"roles_at_hq,omitempty"`
	RolesAtOther          []string `json:"roles_at_other,omitempty"`
}

func ParseCorporationRoles(body []byte) ([]CorporationRolesDTO, error) {
	var dto []CorporationRolesDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation roles: %w", err)
	}
	return dto, nil
}

// corpRoleRow is one (role, grantable, at_hq, at_base, at_other) tuple —
// app.corporation_role's whole primary key beyond (corporation_id,
// character_id), designed for exactly this 8-way flattening.
type corpRoleRow struct {
	Role                             string
	Grantable, AtHQ, AtBase, AtOther bool
}

func SyncCorporationRoles(ctx context.Context, s *store.Store, corporationID int64, entries []CorporationRolesDTO) (SyncResult, error) {
	n := int32(0)
	for _, e := range entries {
		rows := make([]corpRoleRow, 0)
		for _, r := range e.Roles {
			rows = append(rows, corpRoleRow{Role: r})
		}
		for _, r := range e.RolesAtHQ {
			rows = append(rows, corpRoleRow{Role: r, AtHQ: true})
		}
		for _, r := range e.RolesAtBase {
			rows = append(rows, corpRoleRow{Role: r, AtBase: true})
		}
		for _, r := range e.RolesAtOther {
			rows = append(rows, corpRoleRow{Role: r, AtOther: true})
		}
		for _, r := range e.GrantableRoles {
			rows = append(rows, corpRoleRow{Role: r, Grantable: true})
		}
		for _, r := range e.GrantableRolesAtHQ {
			rows = append(rows, corpRoleRow{Role: r, Grantable: true, AtHQ: true})
		}
		for _, r := range e.GrantableRolesAtBase {
			rows = append(rows, corpRoleRow{Role: r, Grantable: true, AtBase: true})
		}
		for _, r := range e.GrantableRolesAtOther {
			rows = append(rows, corpRoleRow{Role: r, Grantable: true, AtOther: true})
		}
		for _, rr := range rows {
			if _, err := s.ReplaceCorporationRole(ctx, gen.ReplaceCorporationRoleParams{
				CorporationID: corporationID, CharacterID: e.CharacterID, Role: rr.Role,
				Grantable: rr.Grantable, AtHq: rr.AtHQ, AtBase: rr.AtBase, AtOther: rr.AtOther,
			}); ignoreUnchanged(err) != nil {
				return SyncResult{}, fmt.Errorf("handlers: upserting role %q for character %d: %w", rr.Role, e.CharacterID, err)
			}
			n++
		}
	}
	return SyncResult{RowsAffected: n}, nil
}

// ---- GET /corporations/{corporation_id}/roles/history ----

type CorporationRoleHistoryDTO struct {
	ChangedAt   time.Time `json:"changed_at"`
	CharacterID int64     `json:"character_id"`
	IssuerID    int64     `json:"issuer_id"`
	NewRoles    []string  `json:"new_roles"`
	OldRoles    []string  `json:"old_roles"`
	RoleType    string    `json:"role_type"`
}

func ParseCorporationRoleHistory(body []byte) ([]CorporationRoleHistoryDTO, error) {
	var dto []CorporationRoleHistoryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation role history: %w", err)
	}
	return dto, nil
}

// SyncCorporationRoleHistory inserts every entry keyed by a synthetic
// record_id (ESI's role-history entries carry no id of their own — unlike
// character_corporation_history/corporation_alliance_history, which use a
// CCP-issued record_id). A stable synthetic key is derived from
// (changed_at, character_id, role_type) via a deterministic hash so a
// re-synced page is a genuine no-op (ON CONFLICT DO NOTHING) rather than a
// duplicate row on every sync.
func SyncCorporationRoleHistory(ctx context.Context, s *store.Store, corporationID int64, entries []CorporationRoleHistoryDTO) (SyncResult, error) {
	n := int32(0)
	for _, e := range entries {
		recordID := syntheticRecordID(corporationID, e.ChangedAt, e.CharacterID, e.RoleType)
		if _, err := s.InsertCorporationRoleHistory(ctx, gen.InsertCorporationRoleHistoryParams{
			CorporationID: corporationID, RecordID: recordID, CharacterID: e.CharacterID,
			ChangedAt: e.ChangedAt, IssuerID: e.IssuerID, RoleType: e.RoleType,
			OldRoles: e.OldRoles, NewRoles: e.NewRoles,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: inserting role history for character %d: %w", e.CharacterID, err)
		}
		n++
	}
	return SyncResult{RowsAffected: n}, nil
}

// ---- GET /corporations/{corporation_id}/divisions ----

type CorporationDivisionsDTO struct {
	Hangar []CorporationDivisionEntryDTO `json:"hangar,omitempty"`
	Wallet []CorporationDivisionEntryDTO `json:"wallet,omitempty"`
}

type CorporationDivisionEntryDTO struct {
	Division int16   `json:"division"`
	Name     *string `json:"name,omitempty"`
}

func ParseCorporationDivisions(body []byte) (CorporationDivisionsDTO, error) {
	var dto CorporationDivisionsDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CorporationDivisionsDTO{}, fmt.Errorf("handlers: parsing corporation divisions: %w", err)
	}
	return dto, nil
}

func SyncCorporationDivisions(ctx context.Context, s *store.Store, corporationID int64, dto CorporationDivisionsDTO) (SyncResult, error) {
	n := int32(0)
	for _, d := range dto.Hangar {
		if _, err := s.UpsertCorporationDivision(ctx, gen.UpsertCorporationDivisionParams{
			CorporationID: corporationID, DivisionKind: "hangar", Division: d.Division, Name: d.Name,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting hangar division %d for corp %d: %w", d.Division, corporationID, err)
		}
		n++
	}
	for _, d := range dto.Wallet {
		if _, err := s.UpsertCorporationDivision(ctx, gen.UpsertCorporationDivisionParams{
			CorporationID: corporationID, DivisionKind: "wallet", Division: d.Division, Name: d.Name,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting wallet division %d for corp %d: %w", d.Division, corporationID, err)
		}
		n++
	}
	return SyncResult{RowsAffected: n}, nil
}

// ---- GET /corporations/{corporation_id}/shareholders ----

type CorporationShareholderDTO struct {
	ShareCount      int64  `json:"share_count"`
	ShareholderID   int64  `json:"shareholder_id"`
	ShareholderType string `json:"shareholder_type"`
}

func ParseCorporationShareholders(body []byte) ([]CorporationShareholderDTO, error) {
	var dto []CorporationShareholderDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation shareholders: %w", err)
	}
	return dto, nil
}

func SyncCorporationShareholders(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationShareholderDTO) (SyncResult, error) {
	for _, r := range rows {
		if _, err := s.UpsertCorporationShareholder(ctx, gen.UpsertCorporationShareholderParams{
			CorporationID: corporationID, ShareholderID: r.ShareholderID,
			ShareholderType: r.ShareholderType, ShareCount: r.ShareCount,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting shareholder %d for corp %d: %w", r.ShareholderID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/facilities ----

type CorporationFacilityDTO struct {
	FacilityID int64 `json:"facility_id"`
	SystemID   int32 `json:"system_id"`
	TypeID     int32 `json:"type_id"`
}

func ParseCorporationFacilities(body []byte) ([]CorporationFacilityDTO, error) {
	var dto []CorporationFacilityDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation facilities: %w", err)
	}
	return dto, nil
}

func SyncCorporationFacilities(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationFacilityDTO) (SyncResult, error) {
	for _, r := range rows {
		systemID, typeID := r.SystemID, r.TypeID
		if _, err := s.UpsertCorporationFacility(ctx, gen.UpsertCorporationFacilityParams{
			CorporationID: corporationID, FacilityID: r.FacilityID, SystemID: &systemID, TypeID: &typeID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting facility %d for corp %d: %w", r.FacilityID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/customs_offices ----

type CorporationCustomsOfficeDTO struct {
	AllianceTaxRate          *float64 `json:"alliance_tax_rate,omitempty"`
	AllowAccessWithStandings bool     `json:"allow_access_with_standings"`
	AllowAllianceAccess      bool     `json:"allow_alliance_access"`
	BadStandingTaxRate       *float64 `json:"bad_standing_tax_rate,omitempty"`
	CorporationTaxRate       *float64 `json:"corporation_tax_rate,omitempty"`
	ExcellentStandingTaxRate *float64 `json:"excellent_standing_tax_rate,omitempty"`
	GoodStandingTaxRate      *float64 `json:"good_standing_tax_rate,omitempty"`
	NeutralStandingTaxRate   *float64 `json:"neutral_standing_tax_rate,omitempty"`
	OfficeID                 int64    `json:"office_id"`
	ReinforceExitEnd         int16    `json:"reinforce_exit_end"`
	ReinforceExitStart       int16    `json:"reinforce_exit_start"`
	StandingLevel            *string  `json:"standing_level,omitempty"`
	SystemID                 int32    `json:"system_id"`
	TerribleStandingTaxRate  *float64 `json:"terrible_standing_tax_rate,omitempty"`
	TypeID                   *int32   `json:"type_id,omitempty"`
}

func ParseCorporationCustomsOffices(body []byte) ([]CorporationCustomsOfficeDTO, error) {
	var dto []CorporationCustomsOfficeDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation customs offices: %w", err)
	}
	return dto, nil
}

func SyncCorporationCustomsOffices(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationCustomsOfficeDTO) (SyncResult, error) {
	for _, r := range rows {
		systemID := r.SystemID
		reinforceStart, reinforceEnd := r.ReinforceExitStart, r.ReinforceExitEnd
		allowStandings, allowAlliance := r.AllowAccessWithStandings, r.AllowAllianceAccess
		if _, err := s.UpsertCorporationCustomsOffice(ctx, gen.UpsertCorporationCustomsOfficeParams{
			CorporationID: corporationID, OfficeID: r.OfficeID, SystemID: &systemID,
			ReinforceExitStart: &reinforceStart, ReinforceExitEnd: &reinforceEnd,
			AllowAccessWithStandings: &allowStandings, AllowAllianceAccess: &allowAlliance,
			StandingLevel: r.StandingLevel, AllianceTaxRate: r.AllianceTaxRate,
			CorporationTaxRate: r.CorporationTaxRate, ExcellentStandingTaxRate: r.ExcellentStandingTaxRate,
			GoodStandingTaxRate: r.GoodStandingTaxRate, NeutralStandingTaxRate: r.NeutralStandingTaxRate,
			TerribleStandingTaxRate: r.TerribleStandingTaxRate, BadStandingTaxRate: r.BadStandingTaxRate,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting customs office %d for corp %d: %w", r.OfficeID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporations/{corporation_id}/containers/logs ----
// Retained upstream for a limited window only — a gap in the sequence is
// normal (roadmap edge case), so this sync only ever inserts what the
// current page shows; there is no prune-not-in step, unlike a live-state
// list domain.

type CorporationContainerLogDTO struct {
	Action           string    `json:"action"`
	CharacterID      int64     `json:"character_id"`
	ContainerID      int64     `json:"container_id"`
	ContainerTypeID  int32     `json:"container_type_id"`
	LocationFlag     string    `json:"location_flag"`
	LocationID       int64     `json:"location_id"`
	LoggedAt         time.Time `json:"logged_at"`
	NewConfigBitmask *int32    `json:"new_config_bitmask,omitempty"`
	OldConfigBitmask *int32    `json:"old_config_bitmask,omitempty"`
	PasswordType     *string   `json:"password_type,omitempty"`
	Quantity         *int64    `json:"quantity,omitempty"`
	TypeID           *int32    `json:"type_id,omitempty"`
}

func ParseCorporationContainerLog(body []byte) ([]CorporationContainerLogDTO, error) {
	var dto []CorporationContainerLogDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation container logs: %w", err)
	}
	return dto, nil
}

func SyncCorporationContainerLog(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationContainerLogDTO) (SyncResult, error) {
	for _, r := range rows {
		containerTypeID := r.ContainerTypeID
		if _, err := s.InsertCorporationContainerLog(ctx, gen.InsertCorporationContainerLogParams{
			CorporationID: corporationID, LoggedAt: r.LoggedAt, Action: r.Action, CharacterID: r.CharacterID,
			ContainerID: r.ContainerID, ContainerTypeID: &containerTypeID, LocationID: &r.LocationID,
			LocationFlag: &r.LocationFlag, NewConfigBitmask: r.NewConfigBitmask, OldConfigBitmask: r.OldConfigBitmask,
			PasswordType: r.PasswordType, Quantity: r.Quantity, TypeID: r.TypeID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: inserting container log entry for corp %d: %w", corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}
