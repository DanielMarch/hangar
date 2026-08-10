package provisioning

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/store"
)

// loadPlatformRules gathers everything one platform's entitlement
// evaluation needs beyond a user's world state: the live enabled rule set
// (converted to entitlement.Rule) and the group_id -> remote_ref mapping
// evaluate.go's output (keyed by HANGAR's internal group_id) has to be
// translated through before it means anything to a Driver or to
// app.provisioning_state.desired_groups/actual_groups, which are stored as
// remote refs (Discord role id / TS3 server group / Mumble ACL group) —
// the values an exposure-board admin and a Driver both actually recognise.
func loadPlatformRules(ctx context.Context, s *store.Store, platformID uuid.UUID) ([]entitlement.Rule, map[uuid.UUID]string, error) {
	rows, err := s.ListEntitlementRulesForPlatform(ctx, platformID)
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning: listing entitlement rules for platform %s: %w", platformID, err)
	}
	rules := make([]entitlement.Rule, len(rows))
	for i, r := range rows {
		rules[i] = entitlement.Rule{GroupID: r.GroupID, SourceKind: r.SourceKind, SourceRef: r.SourceRef, Effect: r.Effect}
	}

	groups, err := s.ListPlatformGroups(ctx, platformID)
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning: listing platform groups for platform %s: %w", platformID, err)
	}
	refByID := make(map[uuid.UUID]string, len(groups))
	for _, g := range groups {
		refByID[g.GroupID] = g.RemoteRef
	}
	return rules, refByID, nil
}

// desiredRefs translates evaluate.Evaluate's map[group_id]bool into the
// sorted []string of remote_refs that app.provisioning_state.desired_groups
// stores. Sorted so two independently-computed sets compare equal with a
// plain slice equality check and so Postgres's array <> comparison
// (ListExposedProvisioningStates' WHERE clause) is order-independent in
// practice — a resolution whose set of granted groups hasn't actually
// changed must never look "exposed" purely from map iteration order.
//
// A group_id evaluate.go granted that has no entry in refByID (the
// platform_group row was deleted after the rule was written, or a rule
// targets a group_id from a different platform than the one being
// evaluated — never expected, since a rule's group_id is FK-scoped to
// exactly one platform) is skipped rather than surfaced as an empty
// string: an unresolvable remote ref is not a remote ref.
func desiredRefs(granted map[uuid.UUID]bool, refByID map[uuid.UUID]string) []string {
	out := make([]string, 0, len(granted))
	for groupID := range granted {
		if ref, ok := refByID[groupID]; ok {
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

// diffGroups returns what's in next but not prev (added) and what's in
// prev but not next (removed). Both inputs may be in any order; both
// outputs are sorted for the same determinism reason as desiredRefs.
func diffGroups(prev, next []string) (added, removed []string) {
	// Both start non-nil (never Postgres NULL) — app.provisioning_audit's
	// groups_added/groups_removed and app.provisioning_state's
	// desired_groups/actual_groups are all NOT NULL text[] columns
	// (DEFAULT '{}' only applies when a column is omitted from an INSERT
	// entirely, never when a nil Go slice is explicitly bound to it — pgx
	// encodes a nil slice as SQL NULL, not an empty array).
	added = []string{}
	removed = []string{}

	prevSet := make(map[string]bool, len(prev))
	for _, g := range prev {
		prevSet[g] = true
	}
	nextSet := make(map[string]bool, len(next))
	for _, g := range next {
		nextSet[g] = true
	}
	for g := range nextSet {
		if !prevSet[g] {
			added = append(added, g)
		}
	}
	for g := range prevSet {
		if !nextSet[g] {
			removed = append(removed, g)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// sortedCopy returns a sorted copy of ss — used wherever a set built from
// map iteration (inherently unordered) needs to become a deterministic
// slice before it's persisted or compared.
func sortedCopy(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}
