// Character fitting sync — Appendix A capability #8 "Fittings (+ EFT
// export)".
//
// ── PHASE 20.7 (B48) ─────────────────────────────────────────────────────
// app.character_fitting and app.character_fitting_item have existed since
// Phase 15, three /api/v1 handlers read them (the list, the detail, and the
// EFT export), and all three of UpsertCharacterFitting,
// UpsertCharacterFittingItem and DeleteCharacterFittingsNotIn had no
// production caller. The EFT export in particular has been rendering an
// empty document on every installation ever deployed.
//
// ── SCOPE ────────────────────────────────────────────────────────────────
// GET /characters/{character_id}/fittings requires
// esi-fittings.read_fittings.v1, which was NOT in the 47-scope grant this
// installation's token carried when the handler was written. See
// docs/SSO_APPLICATION.md — the derived scope set is what tells an operator
// which scopes to enable, and this route is one of five that moved it.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// FittingItemDTO is one module/charge slot in a saved fitting. `flag` is
// CCP's slot vocabulary as a STRING ("HiSlot0", "LoSlot3", "Cargo",
// "DroneBay", ...) and app.character_fitting_item.flag is text to match —
// unlike app.killmail_item.flag, which ESI reports as an integer on that
// route. The two are genuinely different upstream types for the same idea
// and are deliberately not unified.
type FittingItemDTO struct {
	Flag     string `json:"flag"`
	Quantity int64  `json:"quantity"`
	TypeID   int32  `json:"type_id"`
}

// FittingDTO is one saved fitting.
type FittingDTO struct {
	Description string           `json:"description"`
	FittingID   int64            `json:"fitting_id"`
	Items       []FittingItemDTO `json:"items"`
	Name        string           `json:"name"`
	ShipTypeID  int32            `json:"ship_type_id"`
}

func ParseFittings(body []byte) ([]FittingDTO, error) {
	var dto []FittingDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing fittings: %w", err)
	}
	return dto, nil
}

// SyncFittings replaces this character's fitting set with the one ESI just
// returned: upsert every fitting and its items, prune fittings ESI no
// longer lists, and prune items no longer in each surviving fitting.
//
// ── record_id IS SYNTHETIC, AND HAS TO BE ────────────────────────────────
// ESI gives a fitting's items no id of their own, but
// app.character_fitting_item's primary key needs one. It is derived from
// (flag, type_id) rather than from the item's position in the array,
// because array order is not guaranteed stable across responses and a
// position-derived key would make an unchanged fitting look rewritten every
// time CCP reordered the list — defeating the IS DISTINCT FROM guard the
// whole schema is built around.
//
// (flag, type_id) is unique within one fitting: a slot flag names exactly
// one slot, and the cargo/drone-bay flags aggregate identical types into a
// single entry with a quantity rather than repeating them.
//
// RowsAffected counts FITTINGS, not items — it is the count of the
// collection ESI returned, which is what internal/sync/normalize compares
// against.
func SyncFittings(ctx context.Context, s *store.Store, characterID int64, fittings []FittingDTO) (SyncResult, error) {
	keepFittings := make([]int64, len(fittings))
	for i, f := range fittings {
		keepFittings[i] = f.FittingID

		description := f.Description
		if _, err := s.UpsertCharacterFitting(ctx, gen.UpsertCharacterFittingParams{
			CharacterID: characterID,
			FittingID:   f.FittingID,
			Name:        f.Name,
			Description: &description,
			ShipTypeID:  f.ShipTypeID,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting fitting %d for character %d: %w", f.FittingID, characterID, err)
		}

		keepItems := make([]int64, len(f.Items))
		for j, it := range f.Items {
			recordID := syntheticRecordID(it.Flag, it.TypeID)
			keepItems[j] = recordID
			if _, err := s.UpsertCharacterFittingItem(ctx, gen.UpsertCharacterFittingItemParams{
				CharacterID: characterID,
				FittingID:   f.FittingID,
				RecordID:    recordID,
				TypeID:      it.TypeID,
				Flag:        it.Flag,
				Quantity:    it.Quantity,
			}); ignoreUnchanged(err) != nil {
				return SyncResult{}, fmt.Errorf("handlers: upserting item %d of fitting %d for character %d: %w", it.TypeID, f.FittingID, characterID, err)
			}
		}
		if err := s.DeleteCharacterFittingItemsNotIn(ctx, characterID, f.FittingID, keepItems); err != nil {
			return SyncResult{}, fmt.Errorf("handlers: pruning items of fitting %d for character %d: %w", f.FittingID, characterID, err)
		}
	}

	if err := s.DeleteCharacterFittingsNotIn(ctx, characterID, keepFittings); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale fittings for character %d: %w", characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(fittings))}, nil
}
