// urgent.go is the < 60 s revocation path (01_ARCHITECTURE.md §9.2):
// recompute one user's entitlements, and for every platform where the
// recomputed set has LOST a group relative to what's currently persisted,
// write the new desired_groups, an app.provisioning_audit row, and enqueue
// a provision-urgent job — all three in the SAME transaction as each
// other. Losing the enqueue while keeping the state change is a security
// failure (roadmap), so Urgent never opens a transaction it doesn't also
// enqueue inside.
//
// A pure gain (no group lost on any platform) is deliberately NOT routed
// through this path — that's reconcile.go's job on its own schedule. Only
// a loss is urgent; the SLO exists to bound how long a revoked group stays
// live on a remote platform, not how long a new grant takes to show up.
package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// Urgent bundles what the revocation path needs to enqueue transactionally
// — just a River client, since every write goes through the *store.Store
// the caller's transaction already provides.
type Urgent struct {
	River *river.Client[pgx.Tx]
}

// HandleUserChange opens its own transaction and recomputes/enqueues for
// userID — the entry point for triggers that don't already have one open
// (SSO token invalidation/owner-hash-change hooks, an admin's manual
// lockdown action, the per-user loop inside a rule deletion's bulk
// revocation). eventAt is the ORIGINATING event's own timestamp, not
// time.Now() at call time — 01_ARCHITECTURE.md §9.2 / Gate 2 explicitly
// measures from when the triggering condition became true, and a caller
// that already knows that moment (e.g. the token-invalidation transaction
// that just committed) must pass it through rather than let this function
// re-stamp "now".
func (u *Urgent) HandleUserChange(ctx context.Context, pool store.Pool, userID uuid.UUID, eventAt time.Time, reason string) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		return u.HandleUserChangeTx(ctx, s, userID, eventAt, reason)
	})
}

// HandleUserChangeTx is HandleUserChange's counterpart for a caller that is
// ALREADY inside a transaction — the internal/rbac.PermissionsChangedHook
// wiring (cmd/hangar) uses this directly so an RBAC-triggered revocation
// enqueues in the exact same transaction as the RBAC mutation itself,
// without internal/rbac ever importing this package (see
// internal/rbac/hook.go).
func (u *Urgent) HandleUserChangeTx(ctx context.Context, s *store.Store, userID uuid.UUID, eventAt time.Time, reason string) error {
	tx, ok := s.DBTX().(pgx.Tx)
	if !ok {
		return fmt.Errorf("provisioning: HandleUserChangeTx requires a Store bound to an open pgx.Tx (got %T)", s.DBTX())
	}

	links, err := s.ListProvisioningStatesForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("provisioning: listing platform links for user %s: %w", userID, err)
	}
	if len(links) == 0 {
		return nil // not linked to anything — nothing to possibly revoke
	}

	// World state is the same across every platform this user is linked
	// to — gathered once, not once per platform.
	world, err := entitlement.GatherWorldState(ctx, s, userID)
	if err != nil {
		return fmt.Errorf("provisioning: gathering world state for user %s: %w", userID, err)
	}

	for _, link := range links {
		rules, refByID, err := loadPlatformRules(ctx, s, link.PlatformID)
		if err != nil {
			return err
		}
		newDesired := desiredRefs(entitlement.Evaluate(world, rules), refByID)
		added, removed := diffGroups(link.DesiredGroups, newDesired)
		if len(removed) == 0 {
			continue // no loss on this platform — reconcile.go's concern, not urgent's
		}

		updateParams := gen.UpdateProvisioningStateGroupsParams{
			PlatformID:       link.PlatformID,
			UserID:           userID,
			DesiredGroups:    newDesired,
			ActualGroups:     link.ActualGroups, // unchanged — the driver call hasn't happened yet
			LastReconciledAt: link.LastReconciledAt,
		}
		if err := s.UpdateProvisioningStateGroups(ctx, updateParams); err != nil {
			return fmt.Errorf("provisioning: persisting desired groups for user %s platform %s: %w", userID, link.PlatformID, err)
		}

		auditParams := gen.RecordProvisioningAuditParams{
			PlatformID:    link.PlatformID,
			UserID:        userID,
			Action:        "revoke",
			Reason:        reason,
			GroupsAdded:   added,
			GroupsRemoved: removed,
			EventAt:       eventAt,
		}
		audit, err := s.RecordProvisioningAudit(ctx, auditParams)
		if err != nil {
			return fmt.Errorf("provisioning: recording audit for user %s platform %s: %w", userID, link.PlatformID, err)
		}

		if _, err := u.River.InsertTx(ctx, tx, UrgentJobArgs{AuditID: audit.AuditID}, nil); err != nil {
			return fmt.Errorf("provisioning: enqueueing urgent revocation for audit %s: %w", audit.AuditID, err)
		}
	}
	return nil
}

// HandleCharacterTokenChange is the character-scoped entry point for the
// two SSO-layer triggers 01_ARCHITECTURE.md §9.2 names by ID rather than
// by user: token invalidation and an `owner` hash change
// (internal/sso.Refresher's OnInvalidGrant/OnOwnerHashChanged hooks,
// wired in cmd/hangar). Both fire AFTER the token-invalidating transaction
// has already committed (internal/sso/refresh.go's existing Phase 5
// shape — out of Phase 11's file list to change), so this necessarily
// opens its OWN transaction rather than joining one that no longer exists
// by the time this runs; HandleUserChangeTx's same-transaction guarantee
// still holds for what IS atomic here — the entitlement recompute, the
// provisioning_state/provisioning_audit writes, and the provision-urgent
// enqueue are one unit, even though that unit is necessarily separate from
// the SSO token write it was triggered by. Strict Mode re-reads live token
// state at evaluation time regardless (entitlement.GatherWorldState), so
// this window costs at most one extra recompute cycle's latency, never a
// missed revocation.
func (u *Urgent) HandleCharacterTokenChange(ctx context.Context, pool store.Pool, characterID int64, reason string) error {
	s := store.New(pool)
	char, err := s.GetCharacter(ctx, characterID)
	if err != nil {
		return fmt.Errorf("provisioning: resolving user for character %d: %w", characterID, err)
	}
	if !char.UserID.Valid {
		return nil // an unlinked character affects nobody's entitlements
	}
	return u.HandleUserChange(ctx, pool, char.UserID.UUID, time.Now(), reason)
}
