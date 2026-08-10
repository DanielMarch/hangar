package provisioning

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// QueueUrgent and QueueBulk are the two River queues Phase 11 defines
// (01_ARCHITECTURE.md §9.2 / §4.4's own budget table: "provision-urgent
// never shares a worker pool with provision-bulk. A nightly full
// reconciliation must not be able to starve a revocation."). cmd/hangar's
// work command registers both with river.Config's Queues map, the same
// pattern internal/sync/planner.QueueSync established in Phase 6.
const (
	QueueUrgent = "provision-urgent"
	QueueBulk   = "provision-bulk"
)

// KindProvisionUrgent is the job kind enqueued by urgent.go, transactionally
// with the app.provisioning_state/app.provisioning_audit writes that
// represent the entitlement-reducing event. UrgentWorker (worker.go) is the
// only registered Worker for it.
const KindProvisionUrgent = "provision_urgent"

// UrgentJobArgs carries only the audit row's id — the worker re-reads
// app.provisioning_audit for the groups_added/groups_removed/platform_id/
// user_id it needs, rather than trusting a payload that could go stale
// between enqueue and execution (the same "re-read at work time" principle
// internal/sync/planner.SyncJobArgs uses).
type UrgentJobArgs struct {
	AuditID uuid.UUID `json:"audit_id"`
}

// Kind implements river.JobArgs.
func (UrgentJobArgs) Kind() string { return KindProvisionUrgent }

// InsertOpts implements river.JobArgsWithInsertOpts. No ByArgs uniqueness
// constraint — unlike sync's per-route uniqueness, two revocations for the
// same user/platform in quick succession are two independent, both-valid
// audit trails (each audit_id is already unique), not a duplicate of the
// same claim.
func (UrgentJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueUrgent}
}

// KindProvisionBulk is the job kind a scheduler (outside Phase 11's own
// scope — a periodic trigger is Phase 14/ops territory) enqueues to run a
// full reconcile of one platform. BulkWorker is the only registered Worker
// for it.
const KindProvisionBulk = "provision_bulk"

// BulkJobArgs names the platform to reconcile in full.
type BulkJobArgs struct {
	PlatformID uuid.UUID `json:"platform_id"`
}

// Kind implements river.JobArgs.
func (BulkJobArgs) Kind() string { return KindProvisionBulk }

// InsertOpts implements river.JobArgsWithInsertOpts. ByArgs-unique on
// platform_id: a second full reconcile for the same platform queued while
// one is still pending is redundant, not additive.
func (BulkJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:      QueueBulk,
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}
