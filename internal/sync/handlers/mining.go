package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

// pgDate converts a date-only value into pgtype.Date, mirroring
// internal/esi/catalogue/store.go's pgDate helper (not reused directly —
// that one lives in an internal package with no exported surface for this
// package to import, and the conversion is one line).
func pgDate(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// esiDate unmarshals ESI's `format: date` fields — "2026-08-01", not
// RFC3339 — which time.Time's default json.Unmarshal rejects outright
// ("cannot parse \"\" as \"T\""). All three of this file's date-only
// fields (mining ledger's `date`, mining observer/observer-record's
// `last_updated`) use this type instead of time.Time.
type esiDate struct{ time.Time }

func (d *esiDate) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		d.Time = time.Time{}
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("handlers: parsing date-only value %q: %w", s, err)
	}
	d.Time = t
	return nil
}

func (d esiDate) MarshalJSON() ([]byte, error) {
	if d.Time.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.Time.Format("2006-01-02"))
}

// ---- GET /characters/{character_id}/mining ----
// The character's own personal mining ledger — a character-only concept
// (migration 00016's header), synced by CharacterWorker even though the
// domain itself belongs to Phase 8's "Industry & mining" scope.

type CharacterMiningLedgerEntryDTO struct {
	Date          esiDate `json:"date"`
	Quantity      int64   `json:"quantity"`
	SolarSystemID int32   `json:"solar_system_id"`
	TypeID        int32   `json:"type_id"`
}

func ParseCharacterMiningLedger(body []byte) ([]CharacterMiningLedgerEntryDTO, error) {
	var dto []CharacterMiningLedgerEntryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character mining ledger: %w", err)
	}
	return dto, nil
}

func SyncCharacterMiningLedger(ctx context.Context, s *store.Store, characterID int64, entries []CharacterMiningLedgerEntryDTO) (SyncResult, error) {
	for _, e := range entries {
		if _, err := s.UpsertMiningLedgerEntry(ctx, gen.UpsertMiningLedgerEntryParams{
			OwnerKind: "character", OwnerID: characterID, Date: pgDate(e.Date.Time),
			SolarSystemID: e.SolarSystemID, TypeID: e.TypeID, Quantity: e.Quantity,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mining ledger entry for character %d: %w", characterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(entries))}, nil
}

// ---- GET /corporation/{corporation_id}/mining/extractions ----
// SINGULAR upstream path — "/corporation/...", not "/corporations/..." —
// sourced verbatim from app.esi_route.upstream_path by the worker, never
// constructed here (roadmap edge case).

type CorporationMiningExtractionDTO struct {
	ChunkArrivalTime    time.Time `json:"chunk_arrival_time"`
	ExtractionStartTime time.Time `json:"extraction_start_time"`
	MoonID              int64     `json:"moon_id"`
	NaturalDecayTime    time.Time `json:"natural_decay_time"`
	StructureID         int64     `json:"structure_id"`
}

func ParseCorporationMiningExtractions(body []byte) ([]CorporationMiningExtractionDTO, error) {
	var dto []CorporationMiningExtractionDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation mining extractions: %w", err)
	}
	return dto, nil
}

func SyncCorporationMiningExtractions(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationMiningExtractionDTO) (SyncResult, error) {
	for _, r := range rows {
		if _, err := s.UpsertMiningExtraction(ctx, gen.UpsertMiningExtractionParams{
			CorporationID: corporationID, MoonID: r.MoonID, ExtractionStartTime: r.ExtractionStartTime,
			ChunkArrivalTime: r.ChunkArrivalTime, NaturalDecayTime: r.NaturalDecayTime, StructureID: r.StructureID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mining extraction for corp %d moon %d: %w", corporationID, r.MoonID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporation/{corporation_id}/mining/observers ---- (singular)

type CorporationMiningObserverDTO struct {
	LastUpdated  esiDate `json:"last_updated"`
	ObserverID   int64   `json:"observer_id"`
	ObserverType string  `json:"observer_type"`
}

func ParseCorporationMiningObservers(body []byte) ([]CorporationMiningObserverDTO, error) {
	var dto []CorporationMiningObserverDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation mining observers: %w", err)
	}
	return dto, nil
}

func SyncCorporationMiningObservers(ctx context.Context, s *store.Store, corporationID int64, rows []CorporationMiningObserverDTO) (SyncResult, error) {
	for _, r := range rows {
		lastUpdated := r.LastUpdated.Time
		if _, err := s.UpsertMiningObserver(ctx, gen.UpsertMiningObserverParams{
			CorporationID: corporationID, ObserverID: r.ObserverID, ObserverType: r.ObserverType, LastUpdated: &lastUpdated,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mining observer %d for corp %d: %w", r.ObserverID, corporationID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}

// ---- GET /corporation/{corporation_id}/mining/observers/{observer_id} ---- (singular)

type CorporationMiningObserverRecordDTO struct {
	CharacterID           int64   `json:"character_id"`
	LastUpdated           esiDate `json:"last_updated"`
	Quantity              int64   `json:"quantity"`
	RecordedCorporationID int64   `json:"recorded_corporation_id"`
	TypeID                int32   `json:"type_id"`
}

func ParseCorporationMiningObserverRecords(body []byte) ([]CorporationMiningObserverRecordDTO, error) {
	var dto []CorporationMiningObserverRecordDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing corporation mining observer records: %w", err)
	}
	return dto, nil
}

func SyncCorporationMiningObserverRecords(ctx context.Context, s *store.Store, corporationID, observerID int64, rows []CorporationMiningObserverRecordDTO) (SyncResult, error) {
	for _, r := range rows {
		if _, err := s.UpsertMiningObserverRecord(ctx, gen.UpsertMiningObserverRecordParams{
			CorporationID: corporationID, ObserverID: observerID, CharacterID: r.CharacterID, TypeID: r.TypeID,
			RecordedCorporationID: r.RecordedCorporationID, Quantity: r.Quantity, LastUpdated: r.LastUpdated.Time,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting mining observer record for corp %d observer %d character %d: %w", corporationID, observerID, r.CharacterID, err)
		}
	}
	return SyncResult{RowsAffected: int32(len(rows))}, nil
}
