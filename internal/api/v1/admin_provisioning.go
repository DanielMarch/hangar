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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
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

// RuleSetPreviewToken is the mechanism that makes "saving an entitlement
// rule set without previewing it" impossible SERVER-side, not merely
// discouraged in the UI (Phase 18: "a rule saved without preview is how an
// accidental mass revocation happens").
//
// It is a digest over the platform and the exact rule set. The preview
// endpoint returns it alongside the diffs; the save endpoint recomputes it
// over the rules actually submitted and refuses anything that does not
// match. So a save can only succeed for a rule set that was previewed, and
// editing even one rule after previewing invalidates the token — which is
// the case that matters, since a stale token would otherwise let an
// operator preview a harmless change and save a catastrophic one.
//
// Stateless on purpose: no server-side "pending preview" row to expire,
// clean up, or leak between administrators, and it works unchanged across
// replicas. It is an integrity check on a workflow, NOT a security
// boundary — the caller already holds provisioning.entitlements.manage —
// so a plain digest is the right primitive and there is no secret to key
// it with.
func RuleSetPreviewToken(platformID uuid.UUID, rules []RuleInput) string {
	// Canonical form: each rule as a tab-separated tuple, the whole set
	// sorted. Sorted because the rule set is a SET — reordering the same
	// rules in the editor must not invalidate a preview that already showed
	// their exact effect. Tab-separated because none of the four fields can
	// contain a tab (source_ref is an EVE id or a HANGAR uuid; effect and
	// source_kind are closed Go-side sets).
	lines := make([]string, len(rules))
	for i, r := range rules {
		lines[i] = strings.Join([]string{r.SourceKind, r.SourceRef, r.GroupID.String(), r.Effect}, "\t")
	}
	sort.Strings(lines)

	h := sha256.New()
	h.Write([]byte(platformID.String()))
	h.Write([]byte{0})
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReplacePlatformRules is PUT /api/v1/admin/platforms/{id}/rules — a full
// replace of one platform's entitlement rule set, in one transaction, so a
// half-applied rule set can never be evaluated against.
//
// PHASE 18. No endpoint has ever written an entitlement rule: Phase 11
// left CreateEntitlementRule/DeleteEntitlementRule here as unwired seams
// and SRS §6.8 lists only the preview and lockdown routes for a platform,
// so "a rule editor that mandates preview confirmation before saving" had
// nothing to save through. Reported at the close of this phase rather than
// reconciled silently.
//
// DELIBERATE LIMITATION, and the reason this does not call
// DeleteEntitlementRule's bulk-urgent-revocation path: enqueueing an
// urgent recompute needs a *provisioning.Urgent, which needs a River
// client, which exists only in the WORKER process (cmd/hangar/work.go),
// not in the API server. A rule change here therefore takes effect on the
// next scheduled bulk reconcile rather than within Gate 2's per-user < 60s
// urgent SLO. That SLO is defined over revocations triggered by an
// originating EVENT (a token invalidation, an owner-hash change) and is
// unaffected; an administrator rewriting rules by hand is not that. Wiring
// the API process to River would be an architectural change well beyond
// this phase, and is recorded as such rather than half-done here.
func ReplacePlatformRules(ctx context.Context, pool store.Pool, platformID uuid.UUID, rules []RuleInput) ([]gen.AppEntitlementRule, error) {
	var created []gen.AppEntitlementRule
	err := store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		groups, err := s.ListPlatformGroups(ctx, platformID)
		if err != nil {
			return fmt.Errorf("v1: listing groups for platform %s: %w", platformID, err)
		}
		owned := make(map[uuid.UUID]bool, len(groups))
		for _, g := range groups {
			owned[g.GroupID] = true
		}
		// Every submitted rule must target a group this platform owns.
		// Without this check a rule set saved against platform A could
		// silently rewrite platform B's entitlements — the replace below
		// only DELETES A's rules, so B's would be added and never removed.
		for _, r := range rules {
			if !owned[r.GroupID] {
				return fmt.Errorf("v1: group %s does not belong to platform %s", r.GroupID, platformID)
			}
		}

		existing, err := s.ListEntitlementRulesForPlatform(ctx, platformID)
		if err != nil {
			return fmt.Errorf("v1: listing existing rules for platform %s: %w", platformID, err)
		}
		for _, e := range existing {
			if _, err := s.DeleteEntitlementRule(ctx, e.RuleID); err != nil {
				return fmt.Errorf("v1: deleting rule %s: %w", e.RuleID, err)
			}
		}
		created = make([]gen.AppEntitlementRule, 0, len(rules))
		for _, r := range rules {
			row, err := s.CreateEntitlementRule(ctx, gen.CreateEntitlementRuleParams{
				SourceKind: r.SourceKind,
				SourceRef:  r.SourceRef,
				GroupID:    r.GroupID,
				Effect:     r.Effect,
			})
			if err != nil {
				return fmt.Errorf("v1: creating rule (%s %s -> %s): %w", r.SourceKind, r.SourceRef, r.GroupID, err)
			}
			created = append(created, row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
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
	// PHASE 18. Scoped to platformID. This was ListPendingProvisioningAudit,
	// which has no platform predicate — so the exposure board for one
	// platform listed every OTHER platform's pending revocations alongside
	// its own, and an operator reading platform A's board could not tell
	// which rows were A's. The unscoped query is still correct for Gate 2's
	// installation-wide latency measurement and is left alone.
	pending, err := s.ListPendingProvisioningAuditForPlatform(ctx, platformID)
	if err != nil {
		return ExposureBoard{}, fmt.Errorf("v1: listing pending provisioning audit for platform %s: %w", platformID, err)
	}
	return ExposureBoard{Mismatched: mismatched, Pending: pending}, nil
}
