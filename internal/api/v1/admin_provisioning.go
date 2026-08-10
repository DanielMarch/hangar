// Package v1 is empty apart from this file (see cmd/hangar/openapi.go: "The
// router doesn't exist until Phase 15"). admin_provisioning.go is written
// as a plain Go package whose exported functions ARE the eventual
// POST /api/v1/admin/platforms/{id}/rules/preview and rule-management
// endpoints' logic — already-parsed arguments in, already-shaped results
// out, nothing from net/http in any signature — so Phase 15 can wire Huma
// handlers directly on top with no logic changes, the same "documented
// placeholder seam" precedent Phase 10 left at
// internal/api/middleware/authorize.go's context-user convention.
package v1

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CreateEntitlementRuleInput is the already-validated request shape for
// creating one app.entitlement_rule row. SourceKind/Effect are validated
// against entitlement.go's closed Go-side sets by the caller (a future
// Huma handler's own request validation, or a test) — this function
// trusts them, matching every other Phase 11 file's "no I/O beyond what's
// listed" boundary.
type CreateEntitlementRuleInput struct {
	SourceKind string
	SourceRef  string
	GroupID    uuid.UUID
	Effect     string
}

// CreateEntitlementRule creates one rule.
func CreateEntitlementRule(ctx context.Context, s *store.Store, in CreateEntitlementRuleInput) (gen.AppEntitlementRule, error) {
	return s.CreateEntitlementRule(ctx, gen.CreateEntitlementRuleParams{
		SourceKind: in.SourceKind,
		SourceRef:  in.SourceRef,
		GroupID:    in.GroupID,
		Effect:     in.Effect,
	})
}

// RuleInput is the hypothetical-rule-set shape PreviewPlatformRules takes
// — POST .../rules/preview's body, already parsed. It mirrors
// CreateEntitlementRuleInput minus GroupID's FK requirement (a preview's
// hypothetical rule can target any of the platform's existing groups
// without that row needing to exist yet as an app.entitlement_rule).
type RuleInput struct {
	SourceKind string
	SourceRef  string
	GroupID    uuid.UUID
	Effect     string
}

// PreviewPlatformRules is POST /api/v1/admin/platforms/{id}/rules/preview.
// See internal/provisioning/preview.go's doc comment for why this is
// "trivially correct": it is entitlement.Evaluate run against
// hypotheticalRules instead of the live set, nothing more.
func PreviewPlatformRules(ctx context.Context, s *store.Store, platformID uuid.UUID, hypothetical []RuleInput) ([]provisioning.UserDiff, error) {
	rules := make([]entitlement.Rule, len(hypothetical))
	for i, r := range hypothetical {
		rules[i] = entitlement.Rule{GroupID: r.GroupID, SourceKind: r.SourceKind, SourceRef: r.SourceRef, Effect: r.Effect}
	}
	return provisioning.Preview(ctx, s, platformID, rules)
}

// DeleteEntitlementRule deletes ruleID and drives the roadmap's bulk
// urgent-revocation edge case: "Deleting an entitlement rule reduces
// entitlements for everyone it matched — that is a bulk urgent revocation
// (every affected user individually enqueued to provision-urgent, not a
// single provision-bulk job that happens to touch many users at once),
// since the < 60s SLO applies per-user regardless of how many users one
// rule change affects."
//
// The delete itself commits in its own short transaction; the per-user
// recompute/enqueue loop that follows deliberately does NOT try to wrap
// every affected user's Urgent.HandleUserChange call into that same
// transaction — each user's own recompute already gets its OWN
// atomic (state + audit + enqueue) transaction via HandleUserChange, and
// forcing potentially thousands of users into one giant transaction
// alongside the delete would trade a real, bounded risk (one giant lock
// held for the duration of a full-platform recompute) for a guarantee
// this edge case doesn't actually need: entitlement.Evaluate re-reads the
// rule set fresh inside each user's own transaction, so by the time any
// user's recompute runs, the deleted rule is already gone (deleted in a
// committed transaction before this loop starts) — every user's diff
// reflects the post-delete world regardless of how the deletion and the
// revocations are split across transactions.
func DeleteEntitlementRule(ctx context.Context, pool store.Pool, urgent *provisioning.Urgent, ruleID uuid.UUID, eventAt time.Time, reason string) error {
	var platformID uuid.UUID
	err := store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		deleted, err := s.DeleteEntitlementRule(ctx, ruleID)
		if err != nil {
			return fmt.Errorf("v1: deleting entitlement rule %s: %w", ruleID, err)
		}
		group, err := s.GetPlatformGroup(ctx, deleted.GroupID)
		if err != nil {
			return fmt.Errorf("v1: resolving platform for deleted rule %s's group %s: %w", ruleID, deleted.GroupID, err)
		}
		platformID = group.PlatformID
		return nil
	})
	if err != nil {
		return err
	}

	ro := store.New(pool)
	links, err := ro.ListAllProvisioningStatesForPlatform(ctx, platformID)
	if err != nil {
		return fmt.Errorf("v1: listing users linked to platform %s after rule deletion: %w", platformID, err)
	}

	var firstErr error
	for _, link := range links {
		if err := urgent.HandleUserChange(ctx, pool, link.UserID, eventAt, reason); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("v1: revoking for user %s after rule %s deletion: %w", link.UserID, ruleID, err)
			}
		}
	}
	return firstErr
}

// ExposureBoard is the admin dashboard's read model: every provisioning
// mismatch, cross-referenced with the audit rows still awaiting a
// platform call. Exactly the two queries 02_DATABASE_SCHEMA.md §4.4
// names as the exposure board's mechanism, wrapped for the future handler.
type ExposureBoard struct {
	Mismatched []gen.AppProvisioningState
	Pending    []gen.AppProvisioningAudit
}

// GetExposureBoard returns platformID's current exposure state.
func GetExposureBoard(ctx context.Context, s *store.Store, platformID uuid.UUID) (ExposureBoard, error) {
	mismatched, err := s.ListExposedProvisioningStates(ctx, platformID)
	if err != nil {
		return ExposureBoard{}, fmt.Errorf("v1: listing exposed provisioning states for platform %s: %w", platformID, err)
	}
	pending, err := s.ListPendingProvisioningAudit(ctx)
	if err != nil {
		return ExposureBoard{}, fmt.Errorf("v1: listing pending provisioning audit: %w", err)
	}
	return ExposureBoard{Mismatched: mismatched, Pending: pending}, nil
}
