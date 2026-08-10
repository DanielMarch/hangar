package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// UrgentWorker executes one provision-urgent job: read back the audit row
// urgent.go already committed, call the driver for its groups_added/
// groups_removed, complete the audit, and persist the resulting
// actual_groups. This is the ONLY place a revocation's platform_call_
// completed_at gets set — a job that never runs (a platform down for
// longer than River's retry budget) leaves that column NULL forever,
// which is exactly what keeps it on the exposure board with its true age
// (roadmap edge case).
type UrgentWorker struct {
	river.WorkerDefaults[UrgentJobArgs]
	Pool    *pgxpool.Pool
	Drivers *Drivers
}

// Work implements river.Worker[UrgentJobArgs].
func (w *UrgentWorker) Work(ctx context.Context, job *river.Job[UrgentJobArgs]) error {
	s := store.New(w.Pool)
	audit, err := s.GetProvisioningAudit(ctx, job.Args.AuditID)
	if err != nil {
		return fmt.Errorf("provisioning: urgent worker: reading audit %s: %w", job.Args.AuditID, err)
	}

	link, err := s.GetProvisioningState(ctx, audit.PlatformID, audit.UserID)
	if err != nil {
		return fmt.Errorf("provisioning: urgent worker: reading provisioning_state for user %s platform %s: %w", audit.UserID, audit.PlatformID, err)
	}

	driver, driverErr := w.Drivers.Lookup(audit.PlatformID.String())
	actual, outcome, callErr := applyToDriver(ctx, driver, driverErr, link, audit.GroupsAdded, audit.GroupsRemoved)

	var errStr *string
	if callErr != nil {
		msg := callErr.Error()
		errStr = &msg
	}
	if err := s.CompleteProvisioningAudit(ctx, audit.AuditID, &outcome, errStr); err != nil {
		return fmt.Errorf("provisioning: urgent worker: completing audit %s: %w", audit.AuditID, err)
	}

	now := time.Now()
	updateParams := gen.UpdateProvisioningStateGroupsParams{
		PlatformID: audit.PlatformID, UserID: audit.UserID,
		DesiredGroups: link.DesiredGroups, ActualGroups: actual, LastReconciledAt: &now,
	}
	if err := s.UpdateProvisioningStateGroups(ctx, updateParams); err != nil {
		return fmt.Errorf("provisioning: urgent worker: persisting actual_groups for user %s platform %s: %w", audit.UserID, audit.PlatformID, err)
	}

	// A platform-call failure still returns nil here: the audit row is
	// already marked "partial_failure"/"failed" with platform_call_
	// completed_at set (River's job itself succeeded — it did what a
	// provision-urgent job is for, which is attempt and honestly record
	// the outcome). Retrying the DRIVER call on a down platform is
	// reconcile.go's job on its next pass, not this job re-running.
	return nil
}

// BulkWorker executes one provision-bulk job: a full ReconcilePlatform
// pass.
type BulkWorker struct {
	river.WorkerDefaults[BulkJobArgs]
	Pool    *pgxpool.Pool
	Drivers *Drivers
}

// Work implements river.Worker[BulkJobArgs].
func (w *BulkWorker) Work(ctx context.Context, job *river.Job[BulkJobArgs]) error {
	return ReconcilePlatform(ctx, w.Pool, w.Drivers, job.Args.PlatformID, time.Now)
}
