package worker

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/planner"
)

// DispatchWorker is the single river.Worker[planner.SyncJobArgs] the
// process registers for the "sync_route" job kind — River allows exactly
// one Worker per Kind (river.AddWorker panics on a second registration for
// the same Kind at client build time), so CharacterWorker, CorporationWorker
// and GlobalWorker cannot each be registered directly the way Phase 7's
// single-domain CharacterWorker was. DispatchWorker holds all three and
// routes each job to the one matching its entity_kind — the composition
// point this phase's worker fan-out needed that Phase 6/7 didn't, since
// Phase 7 was the first and only consumer of the job kind.
//
// Each of Character/Corporation/GlobalWorker keeps its own exported
// Work(ctx, job) method (unit- and integration-testable in isolation,
// exactly as Phase 7 left CharacterWorker) — DispatchWorker is purely a
// thin routing shim over them.
type DispatchWorker struct {
	river.WorkerDefaults[planner.SyncJobArgs]

	Character   *CharacterWorker
	Corporation *CorporationWorker
	Global      *GlobalWorker
}

// Work implements river.Worker[planner.SyncJobArgs].
func (w *DispatchWorker) Work(ctx context.Context, job *river.Job[planner.SyncJobArgs]) error {
	switch job.Args.EntityKind {
	case sync.EntityCharacter:
		if w.Character == nil {
			return fmt.Errorf("worker: dispatch received a character subscription %s but no CharacterWorker is configured", job.Args.SubscriptionID)
		}
		return w.Character.Work(ctx, job)
	case sync.EntityCorporation:
		if w.Corporation == nil {
			return fmt.Errorf("worker: dispatch received a corporation subscription %s but no CorporationWorker is configured", job.Args.SubscriptionID)
		}
		return w.Corporation.Work(ctx, job)
	case sync.EntityGlobal:
		if w.Global == nil {
			return fmt.Errorf("worker: dispatch received a global subscription %s but no GlobalWorker is configured", job.Args.SubscriptionID)
		}
		return w.Global.Work(ctx, job)
	default:
		return fmt.Errorf("worker: no worker registered for entity_kind %q (subscription %s)", job.Args.EntityKind, job.Args.SubscriptionID)
	}
}
