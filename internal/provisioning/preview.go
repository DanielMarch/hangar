package provisioning

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/store"
)

// UserDiff is one user's exact gain/loss under a hypothetical rule set —
// never a count (roadmap edge case: "Preview must return exact gains and
// losses, per user, not counts"). Groups are remote_ref strings, the same
// vocabulary app.provisioning_state.desired_groups already uses, so a
// preview reads directly against what an admin would see on the exposure
// board.
type UserDiff struct {
	UserID uuid.UUID
	Gained []string
	Lost   []string
}

// Preview is POST /api/v1/admin/platforms/{id}/rules/preview's entire
// logic (internal/api/v1/admin_provisioning.go is the future HTTP
// wrapper — Phase 15, per READ FIRST #5). It is "trivially correct"
// exactly because it is entitlement.Evaluate run against hypotheticalRules
// instead of the live rule set — the same GatherWorldState, the same
// Evaluate, no second implementation to drift from the real one.
//
// hypotheticalRules replaces platformID's ENTIRE live rule set for this
// computation (not merged with it) — an admin editing rules previews what
// the platform would look like after saving, not a merge of old and new.
// Scoped to platformID: only that platform's currently-linked users
// (app.provisioning_state rows) are evaluated, matching the roadmap's
// route shape (`/platforms/{id}/rules/preview`).
func Preview(ctx context.Context, s *store.Store, platformID uuid.UUID, hypotheticalRules []entitlement.Rule) ([]UserDiff, error) {
	groups, err := s.ListPlatformGroups(ctx, platformID)
	if err != nil {
		return nil, fmt.Errorf("provisioning: preview: listing groups for platform %s: %w", platformID, err)
	}
	refByID := make(map[uuid.UUID]string, len(groups))
	for _, g := range groups {
		refByID[g.GroupID] = g.RemoteRef
	}

	links, err := s.ListAllProvisioningStatesForPlatform(ctx, platformID)
	if err != nil {
		return nil, fmt.Errorf("provisioning: preview: listing links for platform %s: %w", platformID, err)
	}

	var diffs []UserDiff
	for _, link := range links {
		world, err := entitlement.GatherWorldState(ctx, s, link.UserID)
		if err != nil {
			return nil, fmt.Errorf("provisioning: preview: gathering world state for user %s: %w", link.UserID, err)
		}
		hypothetical := desiredRefs(entitlement.Evaluate(world, hypotheticalRules), refByID)
		gained, lost := diffGroups(link.DesiredGroups, hypothetical)
		if len(gained) == 0 && len(lost) == 0 {
			continue
		}
		diffs = append(diffs, UserDiff{UserID: link.UserID, Gained: gained, Lost: lost})
	}
	return diffs, nil
}
