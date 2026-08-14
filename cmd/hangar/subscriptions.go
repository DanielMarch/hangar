package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync/subscribe"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// reconcileInterval is how often `serve` re-runs subscription
// reconciliation.
//
// Not tunable, and not frequent. Reconciliation is not a scheduler — the
// planner already owns cadence, and a new subscription's next_due_at
// defaults to now(), so it becomes due the moment it exists. This loop only
// needs to notice CHANGES in the installation's shape: a character
// authorised, a corporation_id that has just been filled in by a character
// sync, a catalogue ingest that added routes, a scope set that widened.
// Five minutes is fast enough that the corporation bootstrap completes
// shortly after the character sheet first syncs, and slow enough that four
// set-based statements every tick are free.
const reconcileInterval = 5 * time.Minute

// newAdminSyncCmd builds `hangar admin sync ...`.
func newAdminSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Inspect and repair the ESI sync subscription set",
	}
	cmd.AddCommand(newAdminSyncReconcileCmd())
	return cmd
}

// newAdminSyncReconcileCmd is the explicit operator action, the counterpart
// to the automatic path in `serve`.
//
// B20's fix established this pattern and it is followed deliberately: one
// command an operator can run, and one automatic caller so a single-box
// installation never has to know the command exists. The command is what
// lets an operator repair an installation — after restoring a database,
// after widening a token's scopes, after an ingest — without writing SQL,
// which was the ONLY way to create a subscription before Phase 20.1.1.
func newAdminSyncReconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile",
		Short: "Create any missing ESI sync subscriptions, and enable/disable them to match granted scopes",
		Long: "Reconciles app.sync_subscription against the route catalogue and each character's\n" +
			"granted scopes. Idempotent: a reconciled installation reports zeroes.\n\n" +
			"Corporation subscriptions can only be created once app.character.corporation_id is\n" +
			"populated, and that column is filled by a CHARACTER route — so on a fresh\n" +
			"installation the first run creates character and global subscriptions, and a later\n" +
			"run (or `serve`'s periodic pass) creates the corporation ones.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			logger := newLogger(cfg)

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			pool, err := pgxpool.New(ctx, cfg.DB.URL.Reveal())
			if err != nil {
				return err
			}
			defer pool.Close()

			result, err := subscribe.All(ctx, store.New(pool))
			if err != nil {
				return err
			}

			cmd.Printf("subscriptions created: %d character, %d corporation, %d global\n",
				result.CharacterCreated, result.CorporationCreated, result.GlobalCreated)
			cmd.Printf("enabled state changed:  %d\n", result.EnabledChanged)
			if result.Empty() {
				cmd.Println("nothing to do — the installation was already reconciled")
			}
			result.Log(ctx, logger, "admin")
			return nil
		},
	}
}

// runSubscriptionReconciler runs reconciliation now and then on a timer,
// until ctx is cancelled.
//
// Runs on EVERY replica rather than behind the planner's leader lock. Every
// statement is a single set-based INSERT ... ON CONFLICT DO NOTHING or a
// conditional UPDATE, so concurrent passes converge instead of conflicting,
// and not gating on leadership means an installation whose leader is
// wedged still schedules newly authorised characters.
//
// A tick failure is logged and swallowed, never fatal: this shares the
// process with the HTTP listener, and a transient database hiccup while
// reconciling must not take the API down. The same reasoning as the alert
// dispatcher's pump.
func runSubscriptionReconciler(ctx context.Context, s *store.Store, logger *slog.Logger) {
	reconcile := func(trigger string) {
		passCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()

		result, err := subscribe.All(passCtx, s)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.ErrorContext(passCtx,
				"sync: subscription reconciliation failed — newly authorised characters and "+
					"newly catalogued routes will not be scheduled until this succeeds; "+
					"run 'hangar admin sync reconcile' to see the error directly",
				"error", err)
			return
		}
		result.Log(passCtx, logger, trigger)
	}

	// Immediately at boot, so an installation restored from a backup, or one
	// upgrading from a build that predates B42's fix, schedules its routes
	// without waiting a full interval.
	//
	// On a genuinely FRESH installation this first pass creates nothing, and
	// that is expected rather than a bug: serve ingests the route catalogue
	// in the background (see runServe), so app.esi_route is still empty when
	// this runs and there is nothing to subscribe to. Measured on the
	// release image — the startup pass created 0, the catalogue then landed
	// 225 routes, and the next pass created the global subscriptions. The
	// visible consequence is that a brand-new installation begins polling
	// within one reconcileInterval rather than instantly; `hangar admin sync
	// reconcile` closes that gap immediately for an operator who does not
	// want to wait.
	reconcile("startup")

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile("periodic")
		}
	}
}
