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

	// Latency, when set, receives Gate 2's measurement for every audit row
	// this worker completes. Nil is supported — internal/provisioning has
	// no business requiring a metrics registry to function, and every
	// existing test constructs an UrgentWorker without one.
	Latency RevocationObserver
}

// RevocationObserver is the metric seam: internal/telemetry's
// RevocationLatency satisfies it. An interface rather than the concrete
// type so this package does not import the Prometheus client, matching how
// internal/esi takes its Observer.
type RevocationObserver interface {
	ObserveRevocation(outcome string, seconds float64)
}

// observe records one completed revocation, tolerating a nil observer.
func observe(o RevocationObserver, outcome string, seconds float64) {
	if o == nil {
		return
	}
	o.ObserveRevocation(outcome, seconds)
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
	// PHASE 20.4, Gate 2 trigger row 8. app.platform.locked_down is read
	// HERE, at the moment of the outbound call, and not at enqueue time.
	// That is the whole point of an incident freeze: a revocation enqueued
	// a second before the operator hit the switch must not go out, and one
	// enqueued during the freeze must go out the moment it lifts. Testing
	// the flag where the job is CREATED would give the opposite behaviour
	// on both counts.
	lockedDown := PlatformIsLockedDown(ctx, s, audit.PlatformID)
	actual, outcome, callErr := applyToDriver(ctx, driver, driverErr, lockedDown, link, audit.GroupsAdded, audit.GroupsRemoved)

	var errStr *string
	if callErr != nil {
		msg := callErr.Error()
		errStr = &msg
	}
	latency, err := s.CompleteProvisioningAudit(ctx, audit.AuditID, &outcome, errStr)
	if err != nil {
		return fmt.Errorf("provisioning: urgent worker: completing audit %s: %w", audit.AuditID, err)
	}
	// Gate 2's measurement, taken where the revocation actually finishes
	// and computed by the statement that wrote its second operand.
	//
	// The URGENT path only. reconcile.go's bulk pass deliberately does not
	// observe, and that is not an omission: it stamps event_at with
	// time.Now() at the top of its own loop, so its
	// (completed_at − event_at) is the duration of the platform call and
	// NOT the time since an originating event. §2.2 measures the latter.
	// Mixing the two would put thousands of near-zero samples from a
	// nightly reconcile into the p99 that is supposed to police
	// revocations, which is 20.2's "operands describing different moments"
	// lesson wearing a different hat — and it would flatter the number.
	observe(w.Latency, outcome, latency)

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
