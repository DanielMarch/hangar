// Killmail sync — Appendix A capability #39 "Killmails (character,
// corporation, detail)".
//
// ── PHASE 20.7 (B48): THE TWO-STAGE ONE ──────────────────────────────────
// app.killmail / killmail_attacker / killmail_item have existed since Phase
// 15 and GET /api/v1/{characters,corporations}/{id}/killmails have read them
// since; UpsertKillmail, UpsertKillmailAttacker and UpsertKillmailItem had
// no production caller.
//
// ── WHY STAGE 1 CANNOT BE PERSISTED ON ITS OWN ───────────────────────────
// Every other detail fan-out in HANGAR persists the parent list first and
// enumerates the detail calls from the stored rows. Killmails cannot work
// that way, and the schema is what forbids it: the recent-list route returns
// only {killmail_id, killmail_hash}, while app.killmail requires
// killmail_time, solar_system_id, victim_ship_type_id and
// victim_damage_taken NOT NULL — and killmail_time is the PARTITION KEY, so
// a stub row has no partition to land in. There is therefore no such thing
// as a killmail row that exists before its detail has been fetched.
//
// The consequence is recorded in worker/unmapped.go: the detail route is
// fetched inside the recent route's own sync, not by a subscription of its
// own, because a subscription for it would have nothing to enumerate.
//
// ── legacy's attacker_hash IS NOT MODELLED, DELIBERATELY ─────────────────
// SeAT gave each attacker row an `attacker_hash` — a surrogate key it
// computed itself to deduplicate attackers, not anything CCP sends.
// app.killmail_attacker has no column for it and does not need one: the
// same job is done by record_id, derived below from the attacker's own
// identifying fields. Adding a column to carry a foreign system's internal
// surrogate would be importing SeAT's implementation, not its data.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// KillmailRefDTO is one element of the recent-killmails list — the whole of
// stage 1. The hash is not an id: it is the capability token CCP requires in
// the detail route's path, and it is stored on app.killmail so a killmail
// can be re-fetched or verified later without re-reading the list.
type KillmailRefDTO struct {
	KillmailID   int64  `json:"killmail_id"`
	KillmailHash string `json:"killmail_hash"`
}

func ParseKillmailRefs(body []byte) ([]KillmailRefDTO, error) {
	var dto []KillmailRefDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing killmail refs: %w", err)
	}
	return dto, nil
}

// KillmailAttackerDTO is one attacker on a killmail. Only damage_done,
// final_blow and security_status are required upstream: an NPC or structure
// attacker carries no character_id, and a solo NPC gank carries no
// corporation_id either, so every identity field is a pointer.
type KillmailAttackerDTO struct {
	AllianceID     *int64   `json:"alliance_id,omitempty"`
	CharacterID    *int64   `json:"character_id,omitempty"`
	CorporationID  *int64   `json:"corporation_id,omitempty"`
	DamageDone     int64    `json:"damage_done"`
	FactionID      *int32   `json:"faction_id,omitempty"`
	FinalBlow      bool     `json:"final_blow"`
	SecurityStatus *float64 `json:"security_status,omitempty"`
	ShipTypeID     *int32   `json:"ship_type_id,omitempty"`
	WeaponTypeID   *int32   `json:"weapon_type_id,omitempty"`
}

// KillmailItemDTO is one destroyed/dropped item. `items` is the nested
// contents of a container — ESI nests exactly one level, and
// app.killmail_item.parent_record_id is the column that models it.
//
// `flag` is an INTEGER here, unlike a fitting item's string flag
// (fitting.go). That difference is upstream's, not HANGAR's.
type KillmailItemDTO struct {
	Flag              int32             `json:"flag"`
	ItemTypeID        int32             `json:"item_type_id"`
	Items             []KillmailItemDTO `json:"items,omitempty"`
	QuantityDestroyed *int64            `json:"quantity_destroyed,omitempty"`
	QuantityDropped   *int64            `json:"quantity_dropped,omitempty"`
	Singleton         *int32            `json:"singleton,omitempty"`
}

// KillmailVictimDTO is the victim half of a killmail detail.
type KillmailVictimDTO struct {
	AllianceID    *int64            `json:"alliance_id,omitempty"`
	CharacterID   *int64            `json:"character_id,omitempty"`
	CorporationID *int64            `json:"corporation_id,omitempty"`
	DamageTaken   int64             `json:"damage_taken"`
	FactionID     *int32            `json:"faction_id,omitempty"`
	Items         []KillmailItemDTO `json:"items,omitempty"`
	Position      *KillmailPosition `json:"position,omitempty"`
	ShipTypeID    int32             `json:"ship_type_id"`
}

// KillmailPosition is the victim's location at the moment of the kill.
type KillmailPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// KillmailDetailDTO is GET /killmails/{killmail_id}/{killmail_hash}.
// Note that the response does NOT echo the hash — it is path input only,
// which is why SyncKillmail takes it as a separate argument.
type KillmailDetailDTO struct {
	Attackers     []KillmailAttackerDTO `json:"attackers"`
	KillmailID    int64                 `json:"killmail_id"`
	KillmailTime  time.Time             `json:"killmail_time"`
	MoonID        *int64                `json:"moon_id,omitempty"`
	SolarSystemID int32                 `json:"solar_system_id"`
	Victim        KillmailVictimDTO     `json:"victim"`
	WarID         *int64                `json:"war_id,omitempty"`
}

func ParseKillmailDetail(body []byte) (KillmailDetailDTO, error) {
	var dto KillmailDetailDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return KillmailDetailDTO{}, fmt.Errorf("handlers: parsing killmail detail: %w", err)
	}
	return dto, nil
}

// SyncKillmail writes one killmail, its attackers and its items.
//
// The hash comes from the recent list rather than from this response, so it
// is passed in; a killmail whose detail was fetched with a hash is stored
// with that same hash, never with an empty string.
//
// Attackers and items are inserted ON CONFLICT DO NOTHING (see killmail.sql)
// because a killmail is immutable: once the rows exist they are correct
// forever, and there is nothing to update. RowsAffected therefore counts 1
// for the killmail itself — the unit the caller asked for — rather than the
// attacker/item totals, which would inflate the number reported for a
// single kill into the dozens.
func SyncKillmail(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, hash string, dto KillmailDetailDTO) (SyncResult, error) {
	var x, y, z *float64
	if dto.Victim.Position != nil {
		x, y, z = &dto.Victim.Position.X, &dto.Victim.Position.Y, &dto.Victim.Position.Z
	}

	if _, err := s.UpsertKillmail(ctx, gen.UpsertKillmailParams{
		OwnerKind: ownerKind, OwnerID: ownerID,
		KillmailID: dto.KillmailID, KillmailHash: hash, KillmailTime: dto.KillmailTime,
		SolarSystemID: dto.SolarSystemID, MoonID: dto.MoonID, WarID: dto.WarID,
		VictimCharacterID: dto.Victim.CharacterID, VictimCorporationID: dto.Victim.CorporationID,
		VictimAllianceID: dto.Victim.AllianceID, VictimFactionID: dto.Victim.FactionID,
		VictimShipTypeID: dto.Victim.ShipTypeID, VictimDamageTaken: dto.Victim.DamageTaken,
		VictimX: x, VictimY: y, VictimZ: z,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting killmail %d for %s %d: %w", dto.KillmailID, ownerKind, ownerID, err)
	}

	// record_id is synthetic: ESI gives attackers no id. It is derived from
	// the attacker's identifying fields rather than from array position,
	// for the same reason fitting items are (see fitting.go) — position is
	// not a stable identity. damage_done and final_blow are included because
	// two NPCs of the same ship type in the same fleet are otherwise
	// indistinguishable, and collapsing them would silently drop an
	// attacker row.
	for _, a := range dto.Attackers {
		if _, err := s.UpsertKillmailAttacker(ctx, gen.UpsertKillmailAttackerParams{
			OwnerKind: ownerKind, OwnerID: ownerID,
			KillmailID: dto.KillmailID, KillmailTime: dto.KillmailTime,
			RecordID: syntheticRecordID(
				derefOr(a.CharacterID, 0), derefOr(a.CorporationID, 0), derefOr(a.AllianceID, 0),
				derefOr(a.ShipTypeID, 0), derefOr(a.WeaponTypeID, 0), a.DamageDone, a.FinalBlow,
			),
			CharacterID: a.CharacterID, CorporationID: a.CorporationID, AllianceID: a.AllianceID,
			FactionID: a.FactionID, DamageDone: a.DamageDone, FinalBlow: a.FinalBlow,
			SecurityStatus: a.SecurityStatus, ShipTypeID: a.ShipTypeID, WeaponTypeID: a.WeaponTypeID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting attacker on killmail %d: %w", dto.KillmailID, err)
		}
	}

	if err := syncKillmailItems(ctx, s, ownerKind, ownerID, dto, dto.Victim.Items, nil); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{RowsAffected: 1}, nil
}

// syncKillmailItems writes one level of the victim's item tree and recurses
// into container contents, threading each container's record_id down as the
// child's parent_record_id.
//
// ESI nests exactly one level today. The recursion is written generally
// anyway because the cost is nothing and the alternative — two near-copies
// of this loop — is how the nested half comes to disagree with the flat one.
func syncKillmailItems(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, dto KillmailDetailDTO, items []KillmailItemDTO, parent *int64) error {
	for _, it := range items {
		recordID := syntheticRecordID(derefOr(parent, 0), it.Flag, it.ItemTypeID, derefOr(it.Singleton, 0))
		if _, err := s.UpsertKillmailItem(ctx, gen.UpsertKillmailItemParams{
			OwnerKind: ownerKind, OwnerID: ownerID,
			KillmailID: dto.KillmailID, KillmailTime: dto.KillmailTime,
			RecordID: recordID, ParentRecordID: parent,
			TypeID: it.ItemTypeID, Flag: it.Flag,
			QuantityDropped: it.QuantityDropped, QuantityDestroyed: it.QuantityDestroyed,
			Singleton: it.Singleton,
		}); ignoreUnchanged(err) != nil {
			return fmt.Errorf("handlers: upserting item on killmail %d: %w", dto.KillmailID, err)
		}
		if len(it.Items) > 0 {
			child := recordID
			if err := syncKillmailItems(ctx, s, ownerKind, ownerID, dto, it.Items, &child); err != nil {
				return err
			}
		}
	}
	return nil
}

// derefOr renders an optional value for syntheticRecordID's byte stream,
// substituting a zero for absence. It is deliberately NOT a general-purpose
// deref helper: it exists so that "absent" and "present and zero" hash the
// same way on purpose, which is safe here only because these fields are ids
// and CCP does not issue id 0.
func derefOr[T any](p *T, zero T) T {
	if p == nil {
		return zero
	}
	return *p
}
