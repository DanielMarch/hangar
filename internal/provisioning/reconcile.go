// reconcile.go is the bulk path (runs on provision-bulk): for every user
// linked to a platform, recompute desired_groups from the live rule set,
// diff against actual_groups, and drive the platform directly — no further
// job hop, since a bulk reconcile job is already the async unit of work
// (unlike urgent.go, which enqueues a SEPARATE job so the triggering
// transaction never waits on a platform API call).
package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ReconcilePlatform recomputes and applies entitlements for every user
// linked to platformID (every app.provisioning_state row, matched or not —
// unlike the exposure board's ListExposedProvisioningStates, which only
// returns rows already known to disagree). Each user is handled in its own
// transaction so one user's failure (a bad rule, a transient DB error)
// never aborts the whole platform's run, and so a crash mid-reconcile
// loses at most the in-flight user, not the ones already committed.
func ReconcilePlatform(ctx context.Context, pool store.Pool, drivers *Drivers, platformID uuid.UUID, now func() time.Time) error {
	// Rules/groups are read once, outside any per-user transaction — they
	// don't change mid-reconcile in any way this function needs to react
	// to (an admin edit mid-run simply wins or loses the race the same way
	// any concurrent edit would against a long-running batch job).
	ro := store.New(pool)
	rules, refByID, err := loadPlatformRules(ctx, ro, platformID)
	if err != nil {
		return err
	}
	driver, driverErr := drivers.Lookup(platformID.String()) // may be ErrNoDriver — handled per user below

	links, err := ro.ListAllProvisioningStatesForPlatform(ctx, platformID)
	if err != nil {
		return fmt.Errorf("provisioning: listing links for platform %s: %w", platformID, err)
	}

	var firstErr error
	for _, link := range links {
		if err := reconcileOneUser(ctx, pool, rules, refByID, driver, driverErr, link, now()); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// Keep going — one user's platform-call failure must not stop
			// the rest of the reconcile (it stays visible on the exposure
			// board via its own, still-pending provisioning_audit row).
		}
	}
	return firstErr
}

func reconcileOneUser(ctx context.Context, pool store.Pool, rules []entitlement.Rule, refByID map[uuid.UUID]string, driver Driver, driverErr error, link gen.AppProvisioningState, eventAt time.Time) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		world, err := entitlement.GatherWorldState(ctx, s, link.UserID)
		if err != nil {
			return err
		}
		newDesired := desiredRefs(entitlement.Evaluate(world, rules), refByID)
		added, removed := diffGroups(link.ActualGroups, newDesired)
		if len(added) == 0 && len(removed) == 0 {
			// Already converged — still worth stamping last_reconciled_at
			// so the exposure board and any "last checked" UI stay honest.
			return s.UpdateProvisioningStateGroups(ctx, gen.UpdateProvisioningStateGroupsParams{
				PlatformID: link.PlatformID, UserID: link.UserID,
				DesiredGroups: newDesired, ActualGroups: link.ActualGroups, LastReconciledAt: &eventAt,
			})
		}

		audit, err := s.RecordProvisioningAudit(ctx, gen.RecordProvisioningAuditParams{
			PlatformID: link.PlatformID, UserID: link.UserID,
			Action: "reconcile", Reason: "bulk_reconcile",
			GroupsAdded: added, GroupsRemoved: removed, EventAt: eventAt,
		})
		if err != nil {
			return fmt.Errorf("provisioning: recording reconcile audit for user %s platform %s: %w", link.UserID, link.PlatformID, err)
		}

		actual, outcome, callErr := applyToDriver(ctx, driver, driverErr, link, added, removed)

		errStr := (*string)(nil)
		if callErr != nil {
			s := callErr.Error()
			errStr = &s
		}
		// The returned latency is deliberately discarded here — see
		// UrgentWorker.Work for why the bulk pass must not feed Gate 2's
		// histogram.
		if _, err := s.CompleteProvisioningAudit(ctx, audit.AuditID, &outcome, errStr); err != nil {
			return fmt.Errorf("provisioning: completing reconcile audit %s: %w", audit.AuditID, err)
		}

		return s.UpdateProvisioningStateGroups(ctx, gen.UpdateProvisioningStateGroupsParams{
			PlatformID: link.PlatformID, UserID: link.UserID,
			DesiredGroups: newDesired, ActualGroups: actual, LastReconciledAt: &eventAt,
		})
	})
}

// Outcome* are the closed set of values applyToDriver can produce, and the
// values that reach app.provisioning_audit.outcome. They are exported so
// cmd/hangar can pre-initialise Gate 2's histogram with every one of them —
// see KnownOutcomes.
const (
	OutcomeSuccess         = "success"
	OutcomePartialFailure  = "partial_failure"
	OutcomeFailed          = "failed"
	OutcomeSkippedUnlinked = "skipped_unlinked"
)

// KnownOutcomes is that set, in the order a dashboard reads best.
//
// It exists because a Prometheus HistogramVec with no observations exports
// NO SERIES AT ALL — not even a zero — so a `/metrics` scrape of a healthy,
// idle installation was indistinguishable from one where the revocation
// path is not wired. That is 20.1's "a missing reading is never a zero"
// lesson running the other way: for a COUNT OF EVENTS, zero is a true and
// useful reading, and its absence is the misleading one. Pre-initialising
// every label value makes an idle installation say so.
func KnownOutcomes() []string {
	return []string{OutcomeSuccess, OutcomePartialFailure, OutcomeFailed, OutcomeSkippedUnlinked}
}

// applyToDriver drives the platform for one user's added/removed groups,
// best-effort per group so a single failing Grant/Revoke doesn't lose
// progress already made on the others, and returns the resulting actual
// group set (link.ActualGroups plus every add that succeeded, minus every
// remove that succeeded) — never the target set outright, since that would
// silently claim success a partial failure didn't earn.
func applyToDriver(ctx context.Context, driver Driver, driverErr error, link gen.AppProvisioningState, added, removed []string) (actual []string, outcome string, err error) {
	actualSet := make(map[string]bool, len(link.ActualGroups))
	for _, g := range link.ActualGroups {
		actualSet[g] = true
	}

	if link.RemoteIdentity == nil {
		return link.ActualGroups, OutcomeSkippedUnlinked, nil
	}
	if driverErr != nil {
		return link.ActualGroups, OutcomeFailed, fmt.Errorf("provisioning: no driver for platform %s: %w", link.PlatformID, driverErr)
	}

	var callErr error
	for _, ref := range added {
		if e := driver.Grant(ctx, *link.RemoteIdentity, ref); e != nil {
			if callErr == nil {
				callErr = e
			}
			continue
		}
		actualSet[ref] = true
	}
	for _, ref := range removed {
		if e := driver.Revoke(ctx, *link.RemoteIdentity, ref); e != nil {
			if callErr == nil {
				callErr = e
			}
			continue
		}
		delete(actualSet, ref)
	}

	out := make([]string, 0, len(actualSet))
	for g := range actualSet {
		out = append(out, g)
	}
	if callErr != nil {
		return sortedCopy(out), OutcomePartialFailure, callErr
	}
	return sortedCopy(out), OutcomeSuccess, nil
}
