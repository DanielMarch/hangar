package planner

import (
	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/riverqueue/river"
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
		},
	}
}
