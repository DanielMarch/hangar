// Asset sync (Phase 9). Owner-kind-generic per app.asset's own compound PK
// (owner_kind, owner_id, item_id) — the same rationale as wallet.go/
// market.go. Full reconciliation (§3.6 / roadmap edge case): every asset
// ESI returns this page is upserted (a reappearing item_id is RESTORED —
// UpsertAsset already clears deleted_at unconditionally, see asset.sql),
// then anything for this owner NOT in the returned set is soft-deleted,
// never DELETEd. asset_location is a best-effort materialised root-location
// projection, recomputed from the freshly-synced rows via the same
// depth/cycle-bounded walk app.asset's recursive CTE uses (§5.3) so it
// never runs unbounded on a torn sync's cyclic graph either.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// AssetDTO mirrors GET /{owner}/{id}/assets exactly (Principle 13).
type AssetDTO struct {
	IsBlueprintCopy *bool  `json:"is_blueprint_copy,omitempty"`
	IsSingleton     bool   `json:"is_singleton"`
	ItemID          int64  `json:"item_id"`
	LocationFlag    string `json:"location_flag"`
	LocationID      int64  `json:"location_id"`
	LocationType    string `json:"location_type"`
	Quantity        int64  `json:"quantity"`
	TypeID          int32  `json:"type_id"`
}

// AssetNameDTO mirrors POST /{owner}/{id}/assets/names, applied over the
// plain asset list when the caller also fetched names (optional; Sync
// works fine with a nil/empty map).
type AssetNameDTO struct {
	ItemID int64  `json:"item_id"`
	Name   string `json:"name"`
}

func ParseAssets(body []byte) ([]AssetDTO, error) {
	var dto []AssetDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing assets: %w", err)
	}
	return dto, nil
}

func ParseAssetNames(body []byte) ([]AssetNameDTO, error) {
	var dto []AssetNameDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing asset names: %w", err)
	}
	return dto, nil
}

// maxAssetTreeDepth bounds every recursive walk this package performs over
// app.asset — the same bound §5.3's AssetTree query fixture uses. A torn
// sync can produce a cycle in the container graph; the depth bound plus
// AssetTree's own `NOT item_id = ANY(path)` guard are BOTH required so the
// walk degrades to a truncated result instead of running unbounded.
const maxAssetTreeDepth = 25

// SyncAssets upserts the full page of assets for one owner, restoring any
// item_id that reappears (UpsertAsset unconditionally clears deleted_at),
// then soft-deletes anything for this owner not present in the set just
// synced — never a hard DELETE (§3.6). names is optional (nil is fine);
// when supplied it's applied over the same rows in the same pass.
func SyncAssets(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, assets []AssetDTO, names map[int64]string) (SyncResult, error) {
	ids := make([]int64, len(assets))
	for i, a := range assets {
		ids[i] = a.ItemID
		var namePtr *string
		if names != nil {
			if n, ok := names[a.ItemID]; ok {
				namePtr = &n
			}
		}
		if _, err := s.UpsertAsset(ctx, gen.UpsertAssetParams{
			OwnerKind: ownerKind, OwnerID: ownerID, ItemID: a.ItemID, TypeID: a.TypeID,
			LocationID: a.LocationID, LocationType: a.LocationType, LocationFlag: a.LocationFlag,
			Quantity: a.Quantity, IsSingleton: a.IsSingleton, IsBlueprintCopy: a.IsBlueprintCopy,
			Name: namePtr,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting asset %d for %s %d: %w", a.ItemID, ownerKind, ownerID, err)
		}
	}

	// Reconciliation: soft-delete, restore-on-reappear (roadmap edge case;
	// asset.sql's SoftDeleteAssetsNotIn doc comment). An empty page is a
	// legitimate "this owner now has zero assets" state and correctly
	// soft-deletes everything — callers that cannot distinguish "empty
	// page" from "fetch failed" must not call Sync with a failed fetch's
	// nil/empty slice.
	if err := s.SoftDeleteAssetsNotIn(ctx, ownerKind, ownerID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: reconciling assets for %s %d: %w", ownerKind, ownerID, err)
	}

	if err := recomputeAssetLocations(ctx, s, ownerKind, ownerID, assets); err != nil {
		return SyncResult{}, err
	}

	return SyncResult{RowsAffected: int32(len(assets))}, nil
}

// recomputeAssetLocations projects each asset's ROOT location (walking
// location_id -> location_id until it stops resolving to another asset
// item_id in this same set, i.e. it's a station/structure/solar-system,
// not a container) with the same depth bound and cycle guard the SQL
// recursive CTE uses, since this walk is done in Go over the freshly
// synced set rather than re-querying app.asset.
func recomputeAssetLocations(ctx context.Context, s *store.Store, ownerKind string, ownerID int64, assets []AssetDTO) error {
	byItemID := make(map[int64]AssetDTO, len(assets))
	for _, a := range assets {
		byItemID[a.ItemID] = a
	}

	for _, a := range assets {
		rootID := a.LocationID
		rootType := a.LocationType
		visited := map[int64]bool{a.ItemID: true}
		for depth := 0; depth < maxAssetTreeDepth; depth++ {
			parent, isContainer := byItemID[rootID]
			if !isContainer || visited[rootID] {
				break // resolved to a real location, or hit a cycle: stop here
			}
			visited[rootID] = true
			rootID = parent.LocationID
			rootType = parent.LocationType
		}
		if _, err := s.UpsertAssetLocation(ctx, gen.UpsertAssetLocationParams{
			OwnerKind: ownerKind, OwnerID: ownerID, ItemID: a.ItemID,
			RootLocationID: rootID, RootLocationType: rootType, SystemID: nil,
		}); ignoreUnchanged(err) != nil {
			return fmt.Errorf("handlers: recomputing asset_location for item %d of %s %d: %w", a.ItemID, ownerKind, ownerID, err)
		}
	}
	return nil
}
