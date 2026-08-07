package domain

import "fmt"

// Asset mirrors app.asset's shape (02_DATABASE_SCHEMA.md §5.3) at the
// domain layer. Quantity, x/y/z are explicitly NOT money (Principle 9) —
// quantity is a unit count and x/y/z are 3D position coordinates.
type Asset struct {
	Owner           Owner
	ItemID          int64
	TypeID          int32
	LocationID      int64
	LocationType    string // open vocabulary
	LocationFlag    string // open vocabulary
	Quantity        int64
	IsSingleton     bool
	IsBlueprintCopy *bool
	Name            *string
	X, Y, Z         *float64
	Deleted         bool
}

// AssetTreeMaxDepth is the hard bound applied by both the SQL recursive CTE
// (app.asset's "Single-query tree" query, db/queries/asset.sql) and the
// in-process equivalent below. 02_DATABASE_SCHEMA.md §5.3: "Depth 5 in
// under 2 seconds is the Phase 17 target, and a cycle introduced by a torn
// sync must degrade to a truncated tree, not an unbounded query" — the
// bound itself is set generously above that target so a legitimately deep
// container nest (freighter -> container -> container -> ...) still
// resolves, while a cyclic graph from a torn sync still terminates.
const AssetTreeMaxDepth = 32

// AssetTreeNode is one level of a resolved asset tree, mirroring the shape
// the recursive CTE returns (depth + path alongside the asset itself).
type AssetTreeNode struct {
	Asset Asset
	Depth int
	Path  []int64 // item_id path from the tree root, inclusive
}

// BuildAssetTree walks `items` (already the flat result of a single owner's
// assets) starting at `rootLocationID`, applying the same depth bound and
// cycle guard as the SQL CTE. It exists as a pure, dependency-free
// reference implementation the SQL query is tested against
// (TestAssetTreeRecursiveCTE also runs this against the same fixture) and
// as an in-process fallback should the recursive CTE ever need to be
// bypassed (e.g. explaining a truncated tree to an administrator).
func BuildAssetTree(items []Asset, ownerFilter Owner, rootLocationID int64) ([]AssetTreeNode, error) {
	if err := ownerFilter.Validate(); err != nil {
		return nil, err
	}

	byLocation := make(map[int64][]Asset)
	for _, a := range items {
		if a.Owner != ownerFilter || a.Deleted {
			continue
		}
		byLocation[a.LocationID] = append(byLocation[a.LocationID], a)
	}

	var out []AssetTreeNode
	var walk func(locationID int64, depth int, path []int64)
	walk = func(locationID int64, depth int, path []int64) {
		if depth > AssetTreeMaxDepth {
			return // depth bound: containers can cycle after a bad sync
		}
		for _, child := range byLocation[locationID] {
			if containsID(path, child.ItemID) {
				continue // cycle guard: never revisit an item already on this path
			}
			childPath := append(append([]int64{}, path...), child.ItemID)
			out = append(out, AssetTreeNode{Asset: child, Depth: depth, Path: childPath})
			walk(child.ItemID, depth+1, childPath)
		}
	}
	walk(rootLocationID, 1, nil)
	return out, nil
}

func containsID(path []int64, id int64) bool {
	for _, p := range path {
		if p == id {
			return true
		}
	}
	return false
}

// ValidateAsset enforces the invariants the recursive tree query depends
// on: quantity is never negative (a negative-quantity row would silently
// corrupt any UI sum), and a container cannot be its own location.
func ValidateAsset(a Asset) error {
	if err := a.Owner.Validate(); err != nil {
		return err
	}
	if a.Quantity < 0 {
		return fmt.Errorf("domain: asset %d has negative quantity %d", a.ItemID, a.Quantity)
	}
	if a.LocationID == a.ItemID {
		return fmt.Errorf("domain: asset %d cannot be its own location", a.ItemID)
	}
	return nil
}
