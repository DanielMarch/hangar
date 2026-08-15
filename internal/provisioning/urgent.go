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

// EnqueuePlatformReconcile queues a full reconcile of one platform —
// Phase 20.4's answer to Gate 2 trigger row 8, "Admin platform lockdown".
//
// ── THE SPECIFICATION QUESTION, AND HOW IT IS SETTLED ────────────────────
// §2.3's matrix marks the lockdown row "✓ must enqueue urgent" and does not
// say what should be enqueued. Phase 20.3 recorded the dilemma rather than
// guessing: a lockdown FREEZES outbound provisioning, so enqueueing
// revocations the freeze would then refuse to perform is either exactly
// wrong or precisely the point.
//
// It is settled as follows, and the settlement turns on what a freeze
// MEANS rather than on what is convenient to implement.
//
// LOCKING enqueues nothing. A freeze is "stop touching this platform", not
// "revoke everybody". Reading it as the latter would mean an operator
// containing a compromised bot token also, silently, queued the removal of
// every group every member holds — a far larger and less reversible act
// than the one they asked for, performed at the worst possible moment.
// Entitlement changes that occur DURING the freeze still enqueue from
// their own triggers, still attempt, and are recorded
// OutcomeSkippedLockedDown with actual_groups untouched, so each stays
// visible on the exposure board with its true age. That is §2.4 condition
// 2.3's requirement for a platform that is down, applied to the case where
// it is down on purpose.
//
// UNLOCKING enqueues this: one full reconcile of the platform, urgently.
// Everything that changed while the platform was frozen is owed the moment
// the freeze lifts, and an operator who has just declared an incident over
// must not have to wait for the nightly bulk pass to find out whether
// their platform is correct again. That is the transition where "must
// enqueue urgent" is both true and right.
//
// It goes to provision-bulk rather than provision-urgent because the unit
// of work is a whole platform, not one user — the same choice B32's
// entitlement-rule deletion made, and for the same reason §9.2 gives
// provision-urgent its own worker pool: a platform-wide sweep must never
// be able to starve a single user's revocation.
func (u *Urgent) EnqueuePlatformReconcile(ctx context.Context, platformID uuid.UUID) error {
	if u == nil || u.River == nil {
		return fmt.Errorf("provisioning: enqueueing reconcile for platform %s: no river client", platformID)
	}
	if _, err := u.River.Insert(ctx, BulkJobArgs{PlatformID: platformID}, nil); err != nil {
		return fmt.Errorf("provisioning: enqueueing reconcile for platform %s: %w", platformID, err)
	}
	return nil
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

// HandleCharacterChange is the character-scoped entry point for the
// triggers 01_ARCHITECTURE.md §9.2 names by character ID rather than by
// user: token invalidation and an `owner` hash change
// (internal/sso.Refresher's OnInvalidGrant/OnOwnerHashChanged hooks), a
// login that reduced a character's granted scopes, and — since Phase 20.4
// — §2.3 trigger row 6, a character leaving the corporation or alliance
// that its entitlements were derived from
// (internal/sync/handlers.AffiliationChangedHook). All are wired in
// cmd/hangar/revocation.go.
//
// PHASE 20.4 renamed this from HandleCharacterTokenChange. The body was
// never token-specific — it resolves the character's user and recomputes,
// with `reason` carrying what actually happened — and the old name made
// the affiliation trigger look like it needed a second, near-identical
// method. It did not.
//
// The two SSO triggers fire AFTER the token-invalidating transaction
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
func (u *Urgent) HandleCharacterChange(ctx context.Context, pool store.Pool, characterID int64, reason string) error {
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
