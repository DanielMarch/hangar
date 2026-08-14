package planner

import (
	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// QueueSync is the River queue Phase 6 enqueues onto. It maps onto
// .env.example's HANGAR_WORKER_QUEUES entry for background ESI polling
// ("esi-bulk"), as distinct from "esi-high" (reserved for user-triggered,
// latency-sensitive refreshes a later phase may add).
const QueueSync = "esi-bulk"

// KindSyncRoute is the River job kind Phase 6 defines. No Worker is
// registered for it yet — executing the job (calling the ESI gateway,
// normalising the response, upserting domain rows) is Phase 7+'s route
// handlers; Phase 6's job is only to define the payload shape and enqueue
// it transactionally with the claim.
const KindSyncRoute = "sync_route"

// SyncJobArgs is the River job payload enqueued for every claimed
// subscription. It carries only identifiers — a Phase 7+ worker re-reads
// the subscription and route rows itself at work time rather than trusting
// a payload that could go stale between enqueue and execution (the route's
// cache_mode, or the subscription's enabled flag, may have changed).
type SyncJobArgs struct {
	SubscriptionID uuid.UUID       `json:"subscription_id"`
	EntityKind     sync.EntityKind `json:"entity_kind" river:"unique"`
	EntityID       int64           `json:"entity_id" river:"unique"`
	RouteID        uuid.UUID       `json:"route_id" river:"unique"`
}

// Kind implements river.JobArgs.
func (SyncJobArgs) Kind() string { return KindSyncRoute }

// InsertOpts implements river.JobArgsWithInsertOpts, pinning the
// uniqueness contract 01_ARCHITECTURE.md §6.1 asks for: keyed on
// (route_id, entity_kind, entity_id), the second line of defence behind
// the claim transaction's own lease (LeaseSyncSubscriptions in the same
// transaction). ByArgs together with the per-field `river:"unique"` tags
// above scopes the uniqueness hash to exactly those three fields,
// deliberately excluding subscription_id — app.sync_subscription's own
// UNIQUE(entity_kind, entity_id, route_id) constraint means subscription_id
// is 1:1 determined by the other three, so including it would be
// redundant, not additive.
func (SyncJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue: QueueSync,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			// ── DEFECT B47: ByState IS NOT OPTIONAL HERE ─────────────────
			// This was omitted, and River's default for ByState INCLUDES
			// JobStateCompleted. River's own documentation is explicit about
			// what that means: "if a unique job has `completed`, you still
			// can't insert a duplicate, at least not until the job cleaner
			// maintenance process eventually removes the completed job from
			// the river_job table" — and CompletedJobRetentionPeriodDefault
			// is 24 HOURS.
			//
			// So every route synced exactly once and then could not be
			// re-enqueued for a day, no matter what next_due_at said. The
			// whole of §6.2's scheduling policy — x-cache-age, the TTL
			// floor, the adaptive 1.5^n backoff, PlanNextDueAt in its
			// entirety — was silently overridden by a job-cleaner interval.
			// A route declaring a 300-second cache age was polled once per
			// 24 hours.
			//
			// Measured before the fix on a live installation: the planner
			// claimed 70 due subscriptions and River inserted ZERO jobs,
			// because all 70 already had a completed job with the same
			// (kind, route_id, entity_kind, entity_id) hash.
			//
			// The four states below are the ones River REQUIRES when
			// ByState is set at all (Available, Pending, Running,
			// Scheduled); Retryable is kept deliberately, because a job
			// waiting to retry is still in flight and must not be
			// duplicated. Completed, Cancelled and Discarded are excluded —
			// a finished attempt must never block the next scheduled one.
			//
			// This preserves the actual intent recorded above (§6.1's
			// second line of defence against concurrent duplicate work) and
			// drops the accidental behaviour of preventing all future work.
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
				rivertype.JobStateRetryable,
			},
		},
	}
}
