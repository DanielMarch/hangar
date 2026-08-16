package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hangar-project/hangar/internal/telemetry"
)

// Mode is the ledger's execution mode, selected automatically from the
// replica registry — never configured (§5.6's rejected-mitigation note).
type Mode string

const (
	ModeSolo      Mode = "solo"
	ModeClustered Mode = "clustered"
)

// ReplicaCounter is the narrow slice of the replica registry mode selection
// needs. *gen.Queries (and *store.Store) satisfy it directly.
type ReplicaCounter interface {
	CountLiveReplicas(ctx context.Context, liveThreshold time.Duration) (int64, error)
}

// Governor1 is the top-level Governor 1 façade: it owns both ledger
// implementations, decides which one is live from the replica registry, and
// performs the two-directional flush a mode transition requires (§5.6). It
// itself implements Ledger, so callers never branch on mode.
type Governor1 struct {
	mu sync.Mutex

	solo      *LedgerSolo
	clustered *LedgerClustered
	mode      Mode

	// modeObserved records whether the replica registry has ever been read
	// successfully. Until it has, `mode` below holds an ASSUMPTION — see
	// Mode's doc for defect B-10.
	modeObserved bool

	replicas      ReplicaCounter
	liveThreshold time.Duration
	// modeCheckInterval throttles how often the replica registry is
	// re-queried; 0 means "check on every call" (what the exit tests
	// use, for a deterministic instant transition). Production wiring
	// sets this to a few seconds so Acquire doesn't add a DB round-trip
	// to the solo fast path's whole reason for existing.
	modeCheckInterval time.Duration
	lastModeCheck     time.Time
	clock             Clock

	log *slog.Logger

	// onModeChange is called (if set) after a transition completes,
	// after -> before mode strings, for metrics/logging wiring.
	onModeChange func(from, to Mode)

	// testFlushStore, when set, replaces g.clustered.directStore() as the
	// flush target — lets mode_test.go exercise flush.go's two
	// transition directions against an in-memory fakeStore, with no real
	// pgx pool at all. Production code never sets this.
	testFlushStore Store
}

// NewGovernor1 constructs the mode-selecting façade. It starts in solo mode
// optimistically; the first Acquire call re-evaluates against the replica
// registry before doing anything else.
func NewGovernor1(clustered *LedgerClustered, replicas ReplicaCounter, clock Clock, log *slog.Logger) *Governor1 {
	if clock == nil {
		clock = SystemClock
	}
	if log == nil {
		log = slog.Default()
	}
	return &Governor1{
		solo:              NewLedgerSolo(clock),
		clustered:         clustered,
		mode:              ModeSolo,
		replicas:          replicas,
		liveThreshold:     -telemetry.LiveThreshold,
		modeCheckInterval: 2 * time.Second,
		clock:             clock,
		log:               log.With("component", "esi.ratelimit.governor1"),
	}
}

// SetModeCheckInterval overrides the throttle on replica-registry checks.
// Exposed for tests that need deterministic, immediate mode transitions.
func (g *Governor1) SetModeCheckInterval(d time.Duration) { g.modeCheckInterval = d }

// Mode returns the currently active mode and whether it has ever been
// OBSERVED — that is, whether the replica registry has been read at least
// once. Until it has, the returned mode is solo because the ledger has to
// start somewhere, and that is an assumption rather than a reading.
//
// ── PHASE 22, DEFECT B-10: WHY THE SECOND RETURN VALUE EXISTS ────────────
//
// NewGovernor1 starts in ModeSolo optimistically and only consults the
// registry inside ensureMode, which Acquire calls. Between a process
// starting and its first ESI request, esi_ledger_mode therefore reported
// an assumption in exactly the same shape as a genuine reading — and there
// is no value of the gauge that could tell the two apart.
//
// NO REQUEST IS EVER SERVED IN THE WRONG MODE, and that bounds the defect
// precisely: Acquire calls ensureMode BEFORE choosing a ledger, and on the
// first call the throttle cannot skip it (lastModeCheck is the zero time).
// The N=3 replica logs corroborate it — three replicas, three `mode
// transition from=solo to=clustered live_replicas=3`, none ever back. The
// defect was in the GAUGE, not in the ledger.
//
// What it broke was the gate that reads the gauge. Condition 1.8 requires
// `clustered` throughout an N=3 run; §1.4 requires a mid-run replica
// restart; a restarted replica reported `solo` until its first request. No
// implementation could satisfy both, which is why 04_RELEASE_GATES.md
// §1.2 carried a Phase 21 amendment excluding samples taken in the window
// after a restart. The amendment is REVERTED in Phase 22, because its
// entire justification was this defect.
//
// The fix follows 20.4.1's, one metric over: give "not measured" a value
// distinct from "measured". esi_ledger_divergence emits no sample for a
// group the server has not reported on; esi_error_limit_remaining emits no
// sample until the budget row exists; esi_ledger_mode now emits no sample
// until the registry has been read. A gauge with no reading is silent.
func (g *Governor1) Mode() (Mode, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mode, g.modeObserved
}

// ensureMode re-evaluates the replica registry (subject to the throttle)
// and performs a flush transition if the live count crosses the solo/
// clustered boundary. Must be called with g.mu held.
func (g *Governor1) ensureMode(ctx context.Context) error {
	now := g.clock.Now()
	if g.modeCheckInterval > 0 && now.Sub(g.lastModeCheck) < g.modeCheckInterval {
		return nil
	}
	g.lastModeCheck = now

	count, err := g.replicas.CountLiveReplicas(ctx, g.liveThreshold)
	if err != nil {
		// Registry unreachable: stay on the current mode rather than
		// guess. Staying clustered is the safe direction (§5.6's "a
		// replica that dies without deregistering" note applies the
		// same logic to "can't tell how many are live" too); staying
		// solo when already solo is simply correct.
		// And the mode stays UNOBSERVED if it never was: holding an
		// assumption because the registry is unreachable is still holding
		// an assumption, and the gauge must not start claiming otherwise
		// (defect B-10).
		g.log.WarnContext(ctx, "governor1: replica count unavailable; holding current mode", "error", err, "mode", g.mode)
		return nil
	}
	g.modeObserved = true

	want := ModeSolo
	if count >= 2 {
		want = ModeClustered
	}
	if want == g.mode {
		return nil
	}
	if g.clustered == nil && g.testFlushStore == nil {
		// No shared backend wired (e.g. a unit test exercising solo
		// only): never transition away from solo.
		return nil
	}

	from := g.mode
	if err := g.transition(ctx, from, want); err != nil {
		return err
	}
	g.mode = want
	g.log.InfoContext(ctx, "governor1: mode transition", "from", from, "to", want, "live_replicas", count)
	if g.onModeChange != nil {
		g.onModeChange(from, want)
	}
	return nil
}

func (g *Governor1) flushStore() Store {
	if g.testFlushStore != nil {
		return g.testFlushStore
	}
	return g.clustered.directStore()
}

func (g *Governor1) transition(ctx context.Context, from, to Mode) error {
	switch {
	case from == ModeSolo && to == ModeClustered:
		// Flush BEFORE admitting any further request — g.mu is held for
		// the whole transition, so no Acquire can interleave.
		return flushSoloToClustered(ctx, g.solo, g.flushStore())
	case from == ModeClustered && to == ModeSolo:
		solo, err := flushClusteredToSolo(ctx, g.flushStore(), g.clock)
		if err != nil {
			return err
		}
		g.solo = solo
		return nil
	default:
		return fmt.Errorf("ratelimit: unsupported mode transition %s->%s", from, to)
	}
}

// forceModeCheck re-evaluates the replica registry immediately, bypassing
// the throttle — used by tests that need a deterministic transition point.
func (g *Governor1) forceModeCheck(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastModeCheck = time.Time{}
	return g.ensureMode(ctx)
}

// Acquire implements Ledger: check/transition mode, then delegate.
func (g *Governor1) Acquire(ctx context.Context, req AcquireRequest) (*Reservation, error) {
	g.mu.Lock()
	if err := g.ensureMode(ctx); err != nil {
		g.mu.Unlock()
		return nil, err
	}
	active := g.activeLedger()
	g.mu.Unlock()
	return active.Acquire(ctx, req)
}

// Settle implements Ledger. It does not re-check mode: a reservation
// settles against whichever ledger issued it (flush.go's transition logic
// is what guarantees a reservation's fate travels with it across a mode
// change, not Settle re-routing after the fact).
func (g *Governor1) Settle(ctx context.Context, res *Reservation, cost int16, respondedAt time.Time) error {
	g.mu.Lock()
	active := g.activeLedger()
	g.mu.Unlock()
	return active.Settle(ctx, res, cost, respondedAt)
}

// Reconcile implements Ledger.
func (g *Governor1) Reconcile(ctx context.Context, group, userKey string, maxTokens int, serverRemaining int) error {
	g.mu.Lock()
	active := g.activeLedger()
	g.mu.Unlock()
	return active.Reconcile(ctx, group, userKey, maxTokens, serverRemaining)
}

// SoloReadings returns the in-process ledger's last reconcile pair per
// bucket when solo mode is ACTIVE, and ok=false otherwise.
//
// PHASE 20.4.1. The two readers of esi_ledger_divergence read
// app.esi_ledger_bucket, which only the clustered path writes — so in solo
// mode the gate's own metric had no source. ok=false means "ask the
// database", which is correct in clustered mode and correct for a process
// that builds no gateway at all; ok=true with an empty slice means "solo,
// and nothing has been reconciled yet", which is a real answer and not the
// same thing.
func (g *Governor1) SoloReadings() ([]BucketReading, bool) {
	g.mu.Lock()
	mode, solo := g.mode, g.solo
	g.mu.Unlock()
	if mode != ModeSolo || solo == nil {
		return nil, false
	}
	return solo.Readings(), true
}

func (g *Governor1) activeLedger() Ledger {
	if g.mode == ModeClustered && g.clustered != nil {
		return g.clustered
	}
	return g.solo
}

var _ Ledger = (*Governor1)(nil)
