// Package subscribe creates the app.sync_subscription rows that make the
// ESI sync engine do anything at all.
//
// ── DEFECT B42 ───────────────────────────────────────────────────────────
// Until Phase 20.1.1 nothing in production ever called
// UpsertSyncSubscription. Three integration tests did, and no other code
// anywhere — not a command, not a job, not the SSO callback, and no
// migration or seed. app.sync_subscription was therefore permanently empty
// on every real installation, which meant:
//
//   - the planner acquired leadership, claimed due work, found none, and
//     did that forever;
//   - every River worker registered in cmd/hangar/work.go was never once
//     dispatched;
//   - a character could authorise all 46 scopes and HANGAR would never
//     fetch a single byte on their behalf.
//
// Phases 6, 7, 8 and 9 — the planner, and every route handler they built —
// could not execute. Measured on a live installation before the fix: 225
// catalogued routes, 46 granted scopes, 0 subscriptions.
//
// Nothing failed, because an empty work queue and a fully-drained work
// queue look identical from the outside. That is the B20 pattern, and this
// is its largest instance.
package subscribe

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/worker"
)

// Result reports what one reconciliation pass changed. Every field counts
// ROWS AFFECTED, so a steady-state pass reports zeroes — which is the
// point: reconciliation is idempotent and a non-zero count means the
// installation's shape actually changed.
type Result struct {
	CharacterCreated   int64
	CorporationCreated int64
	AllianceCreated    int64
	GlobalCreated      int64
	EnabledChanged     int64
}

// Total is the number of rows this pass created.
func (r Result) Total() int64 {
	return r.CharacterCreated + r.CorporationCreated + r.AllianceCreated + r.GlobalCreated
}

// Empty reports whether the pass was a no-op, i.e. the installation was
// already reconciled.
func (r Result) Empty() bool { return r.Total() == 0 && r.EnabledChanged == 0 }

// Store is what reconciliation needs. Narrow on purpose: this package
// creates and enables/disables subscriptions and does nothing else, so it
// cannot accidentally grow into a second scheduler.
type Store interface {
	ReconcileCharacterSubscriptions(ctx context.Context, paths []string, characterID int64) (int64, error)
	ReconcileCorporationSubscriptions(ctx context.Context, paths []string) (int64, error)
	ReconcileAllianceSubscriptions(ctx context.Context, paths []string) (int64, error)
	ReconcileGlobalSubscriptions(ctx context.Context, paths []string) (int64, error)
	DisableUnscopedSubscriptions(ctx context.Context) (int64, error)
	ListCharacterIDsWithValidTokens(ctx context.Context) ([]int64, error)
}

var _ Store = (*store.Store)(nil)

// ForCharacter reconciles one character's own subscriptions, and then the
// corporation and global sets.
//
// Called from the SSO callback once the token is persisted, so authorising
// a character schedules it immediately rather than at the next timer tick —
// the first thing a new operator does is log in, and an installation that
// then sits inert for minutes looks broken.
func ForCharacter(ctx context.Context, s Store, characterID int64) (Result, error) {
	var result Result

	created, err := s.ReconcileCharacterSubscriptions(ctx, worker.SubscribablePathsFor(sync.EntityCharacter), characterID)
	if err != nil {
		return result, fmt.Errorf("subscribe: reconciling character %d: %w", characterID, err)
	}
	result.CharacterCreated = created

	rest, err := shared(ctx, s)
	if err != nil {
		return result, err
	}
	result.CorporationCreated = rest.CorporationCreated
	result.AllianceCreated = rest.AllianceCreated
	result.GlobalCreated = rest.GlobalCreated
	result.EnabledChanged = rest.EnabledChanged
	return result, nil
}

// All reconciles every character with a valid token, then the corporation,
// alliance and global sets.
//
// ── WHY THIS RUNS PERIODICALLY AND NOT ONLY AT LOGIN ─────────────────────
// The bootstrap is genuinely ordered and cannot be collapsed into one pass.
// A corporation subscription needs app.character.corporation_id, and that
// column is populated by /characters/{character_id} — a CHARACTER route. So
// at the moment a character first authorises, their corporation is unknown,
// and no amount of care at link time can create the corporation rows. The
// sequence is necessarily:
//
//	login → character subscriptions exist → character sheet syncs →
//	corporation_id known → NEXT pass creates corporation subscriptions
//
// Hence a timer. It is also what repairs an installation after a catalogue
// ingest adds routes, after a token is re-authorised with a wider scope
// set, and after an operator re-enables a character.
//
// Safe on every replica concurrently: every statement is a single
// set-based INSERT ... ON CONFLICT DO NOTHING or a conditional UPDATE, so
// concurrent passes converge rather than conflict.
func All(ctx context.Context, s Store) (Result, error) {
	var result Result

	characterIDs, err := s.ListCharacterIDsWithValidTokens(ctx)
	if err != nil {
		return result, fmt.Errorf("subscribe: listing characters with valid tokens: %w", err)
	}

	paths := worker.SubscribablePathsFor(sync.EntityCharacter)
	for _, characterID := range characterIDs {
		created, err := s.ReconcileCharacterSubscriptions(ctx, paths, characterID)
		if err != nil {
			return result, fmt.Errorf("subscribe: reconciling character %d: %w", characterID, err)
		}
		result.CharacterCreated += created
	}

	rest, err := shared(ctx, s)
	if err != nil {
		return result, err
	}
	result.CorporationCreated = rest.CorporationCreated
	result.AllianceCreated = rest.AllianceCreated
	result.GlobalCreated = rest.GlobalCreated
	result.EnabledChanged = rest.EnabledChanged
	return result, nil
}

// shared does the corporation, alliance, global and enable/disable passes,
// which are installation-wide and identical whichever entry point ran.
func shared(ctx context.Context, s Store) (Result, error) {
	var result Result

	corporation, err := s.ReconcileCorporationSubscriptions(ctx, worker.SubscribablePathsFor(sync.EntityCorporation))
	if err != nil {
		return result, fmt.Errorf("subscribe: reconciling corporations: %w", err)
	}
	result.CorporationCreated = corporation

	// PHASE 20.8 (capability #37). Runs after the corporation pass and is
	// ordered one step deeper than it: an alliance subscription needs
	// app.corporation.alliance_id, which the CORPORATION sheet route fills,
	// which itself needs app.character.corporation_id from a CHARACTER route.
	// An installation therefore reaches its alliance subscriptions on the
	// third reconciliation at the earliest — and an installation whose
	// characters are in no alliance never does, correctly.
	alliance, err := s.ReconcileAllianceSubscriptions(ctx, worker.SubscribablePathsFor(sync.EntityAlliance))
	if err != nil {
		return result, fmt.Errorf("subscribe: reconciling alliances: %w", err)
	}
	result.AllianceCreated = alliance

	global, err := s.ReconcileGlobalSubscriptions(ctx, worker.SubscribablePathsFor(sync.EntityGlobal))
	if err != nil {
		return result, fmt.Errorf("subscribe: reconciling global routes: %w", err)
	}
	result.GlobalCreated = global

	// Runs last, and after the inserts, so a row created moments ago by a
	// scope set that has since been reduced is corrected in the same pass
	// rather than surviving until the next one.
	changed, err := s.DisableUnscopedSubscriptions(ctx)
	if err != nil {
		return result, fmt.Errorf("subscribe: reconciling enabled state: %w", err)
	}
	result.EnabledChanged = changed

	return result, nil
}

// Log writes a reconciliation result at a level chosen by whether anything
// happened. A steady-state pass is DEBUG because it runs on a timer and
// would otherwise bury the log; a pass that changed the installation's
// shape is INFO because it is the record of when a route started being
// polled.
func (r Result) Log(ctx context.Context, log *slog.Logger, trigger string) {
	if r.Empty() {
		log.DebugContext(ctx, "sync: subscriptions already reconciled", "trigger", trigger)
		return
	}
	log.InfoContext(ctx, "sync: subscriptions reconciled",
		"trigger", trigger,
		"character_created", r.CharacterCreated,
		"corporation_created", r.CorporationCreated,
		"alliance_created", r.AllianceCreated,
		"global_created", r.GlobalCreated,
		"enabled_changed", r.EnabledChanged)
}
