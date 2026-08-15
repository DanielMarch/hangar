package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
)

// CharacterSheetDTO is GET /characters/{character_id} on the 2026-08-04
// pin (internal/esi/catalogue/embedded/openapi.snapshot.json,
// CharactersCharacterIdGet — there is no top-level operationId name for
// this one in the spec, it's keyed by path). Two fields the roadmap
// summarised as "title_id renamed corporation_title_id" do not match what
// the spec actually declares — see the field comments below and
// db/migrations/00030_phase7_character_fixups.sql's header for the full
// account.
type CharacterSheetDTO struct {
	AchievementScore int64     `json:"achievement_score"`
	AllianceID       *int64    `json:"alliance_id,omitempty"`
	Birthday         time.Time `json:"birthday"`
	BloodlineID      int32     `json:"bloodline_id"`
	// CharacterTitleID: "Character's equipped cosmetic title ID" —
	// $ref: UUID. Not a corporation-title concept at all, and not a
	// bigint despite the "_id" suffix (Principle 13's exact trap).
	CharacterTitleID *uuid.UUID `json:"character_title_id,omitempty"`
	CorporationID    int64      `json:"corporation_id"`
	// CorporationTitle: "Character's corporation title" — a plain
	// string (the title's display name), not an identifier. There is no
	// `title_id` field anywhere in this response; the roadmap's "renamed
	// to corporation_title_id" claim does not match the live spec.
	CorporationTitle *string  `json:"corporation_title,omitempty"`
	Description      *string  `json:"description,omitempty"`
	FactionID        *int32   `json:"faction_id,omitempty"`
	Gender           string   `json:"gender"`
	Name             string   `json:"name"`
	RaceID           int32    `json:"race_id"`
	SecurityStatus   *float64 `json:"security_status,omitempty"`
}

// ParseCharacterSheet unmarshals a raw /characters/{id} response.
func ParseCharacterSheet(body []byte) (CharacterSheetDTO, error) {
	var dto CharacterSheetDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterSheetDTO{}, fmt.Errorf("handlers: parsing character sheet: %w", err)
	}
	return dto, nil
}

// SyncCharacterSheet upserts the enrichment fields GET /characters/{id}
// carries beyond Phase 5's minimal SSO-callback row — deliberately never
// touching user_id or owner_hash, which stay Phase 5's alone.
//
// app.character.corporation_id/alliance_id are foreign keys, and this is
// the first phase that ever sets them (Phase 5's own SSO-callback upsert
// leaves both untouched, precisely to avoid this) — a corporation or
// alliance this installation has never seen before must get a stub row
// first (name/ticker empty; the ON CONFLICT DO NOTHING these two queries
// use, per their own doc comments, means an existing real row is never
// overwritten by an empty placeholder). Phase 8/9 fill in the real
// name/ticker.
//
// ── PHASE 20.4: THIS ROUTE IS GATE 2 TRIGGER ROW 6'S PRODUCER ────────────
// Writing app.character.corporation_id is how HANGAR learns a character has
// LEFT a corporation, and until now nothing acted on it: `grep -rln
// provisioning internal/sync` returned nothing, so a character who left the
// corporation that granted their Discord roles kept them until the next
// nightly bulk reconcile, against §2.1's 60-second p99 bound. The previous
// affiliation is therefore read BEFORE the write and compared after — see
// AffiliationChangedHook in hooks.go for why internal/rbac's hook does not
// and cannot cover this.
func SyncCharacterSheet(ctx context.Context, s *store.Store, characterID int64, dto CharacterSheetDTO) (SyncResult, error) {
	// Read first, write second. One extra SELECT per character-sheet sync —
	// the cheapest of this handler's several round trips, on a route with a
	// long TTL — buys the ONE thing the write itself destroys: what the
	// affiliation used to be. Threading the old values out of the UPDATE's
	// RETURNING instead would need a CTE and a wider generated row type,
	// for a saving that is not measurable here.
	//
	// A character with no row yet has no previous affiliation, which is not
	// a departure. Any other error is left to the write below to report, so
	// this read cannot invent a new failure mode for a handler that already
	// has to survive a half-populated database.
	var previousCorporationID, previousAllianceID *int64
	if existing, err := s.GetCharacter(ctx, characterID); err == nil {
		previousCorporationID = existing.CorporationID
		previousAllianceID = existing.AllianceID
	}

	if err := s.UpsertCorporationStub(ctx, dto.CorporationID, "", ""); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: stubbing corporation %d for character %d: %w", dto.CorporationID, characterID, err)
	}

	// ── DEFECT B44: THE ACTING-CHARACTER BOOTSTRAP DEADLOCK ──────────────
	// app.corporation_member is the ONLY candidate pool internal/sync's
	// DBElector considers, and until now its only writer was
	// SyncCorporationMembers — a CORPORATION-scoped route, which cannot run
	// until an acting character has been elected, which cannot happen until
	// the pool is non-empty. Election needed members; members needed
	// election. No corporation route could ever run on any installation,
	// which is why all 32 corporation subscriptions completed silently and
	// wrote nothing.
	//
	// The character sheet breaks it, and is entitled to: ESI has just told
	// us, authoritatively, which corporation THIS character belongs to. That
	// is a membership fact, and recording one member is all the elector
	// needs to have a candidate. SyncCorporationMembers still owns the full
	// roster once a corporation route can run — this only guarantees the
	// pool is never empty for a corporation HANGAR has a token in.
	if _, err := s.UpsertCorporationMember(ctx, dto.CorporationID, characterID); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: recording character %d as a member of corporation %d: %w",
			characterID, dto.CorporationID, err)
	}
	if dto.AllianceID != nil {
		if err := s.UpsertAllianceStub(ctx, *dto.AllianceID, ""); err != nil {
			return SyncResult{}, fmt.Errorf("handlers: stubbing alliance %d for character %d: %w", *dto.AllianceID, characterID, err)
		}
	}

	var titleID uuid.NullUUID
	if dto.CharacterTitleID != nil {
		titleID = uuid.NullUUID{UUID: *dto.CharacterTitleID, Valid: true}
	}
	corporationID := dto.CorporationID

	_, err := s.SyncCharacterSheet(ctx, gen.SyncCharacterSheetParams{
		CharacterID:      characterID,
		CorporationID:    &corporationID,
		AllianceID:       dto.AllianceID,
		FactionID:        dto.FactionID,
		SecurityStatus:   dto.SecurityStatus,
		Birthday:         nilIfZero(dto.Birthday),
		Gender:           &dto.Gender,
		RaceID:           &dto.RaceID,
		BloodlineID:      &dto.BloodlineID,
		Description:      dto.Description,
		Title:            dto.CorporationTitle,
		CharacterTitleID: titleID,
		AchievementScore: dto.AchievementScore,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// SyncCharacterSheet's WHERE ... IS DISTINCT FROM guard
			// suppressed the UPDATE — either genuinely unchanged data, or
			// character_id doesn't exist yet at all. Disambiguate rather
			// than silently treating the latter as "0 rows changed"
			// (internal/esi/catalogue/sync.go's UpsertEsiRoute establishes
			// this exact pattern).
			if _, getErr := s.GetCharacter(ctx, characterID); getErr != nil {
				return SyncResult{}, fmt.Errorf("handlers: character %d has no row to sync a sheet onto: %w", characterID, getErr)
			}
			return SyncResult{RowsAffected: 0}, nil
		}
		return SyncResult{}, fmt.Errorf("handlers: syncing character sheet: %w", err)
	}

	// The sheet changed. Whether the AFFILIATION changed is a separate
	// question — most sheet updates are a security status or a title — so
	// the hook is only offered a change that actually moved one of the two,
	// rather than being handed every sheet write to filter for itself.
	if err := notifyAffiliationChange(ctx, AffiliationChange{
		CharacterID:           characterID,
		PreviousCorporationID: previousCorporationID,
		CorporationID:         dto.CorporationID,
		PreviousAllianceID:    previousAllianceID,
		AllianceID:            dto.AllianceID,
	}); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{RowsAffected: 1}, nil
}

// notifyAffiliationChange offers a real corporation or alliance move to
// AffiliationChangedHook. A first sighting (no previous corporation
// recorded) is not a move, and neither is a sheet update that left both
// unchanged.
func notifyAffiliationChange(ctx context.Context, change AffiliationChange) error {
	if AffiliationChangedHook == nil {
		return nil
	}
	if !change.CorporationChanged() && !change.AllianceChanged() {
		return nil
	}
	if err := AffiliationChangedHook(ctx, change); err != nil {
		return fmt.Errorf("handlers: revoking entitlements after character %d changed affiliation: %w",
			change.CharacterID, err)
	}
	return nil
}
