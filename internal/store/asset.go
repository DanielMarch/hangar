package store

import (
	"context"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// AssetTree runs the single-query recursive tree CTE (db/queries/asset.sql,
// 02_DATABASE_SCHEMA.md §5.3) and converts the flat gen rows into
// domain.AssetTreeNode, preserving the depth the SQL query already
// computed rather than recomputing it in Go.
func (s *Store) AssetTree(ctx context.Context, owner domain.Owner, locationID int64, maxDepth int32) ([]domain.AssetTreeNode, error) {
	rows, err := s.Queries.AssetTree(ctx, gen.AssetTreeParams{
		OwnerKind:  string(owner.Kind),
		OwnerID:    owner.ID,
		LocationID: locationID,
		MaxDepth:   maxDepth,
	})
	if err != nil {
		return nil, err
	}

	out := make([]domain.AssetTreeNode, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.AssetTreeNode{
			Asset: domain.Asset{
				Owner:           domain.Owner{Kind: domain.OwnerKind(r.OwnerKind), ID: r.OwnerID},
				ItemID:          r.ItemID,
				TypeID:          r.TypeID,
				LocationID:      r.LocationID,
				LocationType:    r.LocationType,
				LocationFlag:    r.LocationFlag,
				Quantity:        r.Quantity,
				IsSingleton:     r.IsSingleton,
				IsBlueprintCopy: r.IsBlueprintCopy,
				Name:            r.Name,
				X:               r.X,
				Y:               r.Y,
				Z:               r.Z,
				Deleted:         r.DeletedAt != nil,
			},
			Depth: int(r.Depth),
		})
	}
	return out, nil
}
