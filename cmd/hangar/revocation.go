package main

// revocation.go assembles 01_ARCHITECTURE.md §9.2's revocation path — the
// wiring that turns an entitlement-reducing EVENT into a provision-urgent
// job — and it does so for BOTH process roles, which is the point.
//
// ── THE DEFECT THIS FILE CLOSES ──────────────────────────────────────────
// Until Phase 20.3 every producer in 04_RELEASE_GATES.md §2.3's trigger
// matrix was wired in cmd/hangar/work.go and nowhere else, because that is
// where the River client lived. But `serve` is the process that actually
// performs most of those mutations:
//
//   - internal/rbac.PermissionsChangedHook is a package-level var. work.go
//     sets it; serve.go did not. 20.2 mounted the whole RBAC mutation
//     surface (admin_roles.go: role create/delete, grant add/remove, user
//     role assign/revoke, and the squad endpoints) in the API server — so
//     every one of those mutations ran with the hook still nil. Gate 2
//     trigger rows "RBAC role revoked" and "Squad membership removed" had a
//     producer only for mutations performed by the worker process, and the
//     worker process performs none of them. Revoking somebody's admin role
//     through the API recomputed app.effective_permission and wrote the
//     §4.9 outbox row, and enqueued nothing: the platform groups that role
//     granted stayed live until the next bulk reconcile.
//   - the SSO login flow (owner-hash change, scope reduction) runs in
//     `serve` too, and had no revocation wiring at all.
//   - B32's entitlement-rule writes are API-side by definition.
//
// ── WHY AN INSERT-ONLY RIVER CLIENT IS NOT AN ARCHITECTURAL CHANGE ───────
// internal/api/v1/admin_provisioning.go recorded, in Phase 18, that
// "enqueueing an urgent recompute needs a *provisioning.Urgent, which needs
// a River client, which exists only in the WORKER process ... Wiring the
// API process to River would be an architectural change well beyond this
// phase". That was an overstatement, and it is worth correcting explicitly
// rather than quietly: River supports insert-only clients as a first-class
// mode — a client built with no Queues and no Workers, on which Start is
// never called, inserts jobs and consumes nothing. It runs no pool, claims
// no work, and cannot starve anything. Producing in one process and
// consuming in another is the standard River topology, not a departure
// from it.
//
// What WOULD have been an architectural change is running provisioning
// WORKERS in `serve`, and that is still not done: cmd/hangar/work.go
// remains the only process with a provision-urgent pool, and §9.2's
// "provision-urgent never shares a worker pool with provision-bulk" is
// untouched.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync/handlers"
)

// newInsertOnlyRiverClient builds a River client that can enqueue and can
// never consume. `serve` uses it; `work` passes its real client to
// wireRevocationTriggers instead.
//
// Start() is deliberately never called on the returned client. A River
// client that has not been started does not claim jobs, does not run a
// producer per queue, and does not listen — insertion is the only thing it
// can do, which is exactly the authority the API process should hold over
// a queue whose workers live somewhere else.
func newInsertOnlyRiverClient(pool *pgxpool.Pool) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("hangar: building insert-only river client: %w", err)
	}
	return client, nil
}

// wireRevocationTriggers installs every §9.2 trigger this process can
// observe, and returns the Urgent the caller may need for its own routes
// (B32's entitlement-rule deletion).
//
// It is called by BOTH `serve` and `work`, with each process's own River
// client. The hooks it sets are package-level or struct-level seams that
// were previously set in exactly one of the two.
func wireRevocationTriggers(riverClient *river.Client[pgx.Tx], pool store.Pool) *provisioning.Urgent {
	urgent := &provisioning.Urgent{River: riverClient}

	// Wires internal/rbac's PermissionsChangedHook (internal/rbac/hook.go)
	// to the revocation path: an RBAC-triggered permission change (direct
	// role grant/revoke, squad_role change, squad membership change, role
	// deletion) recomputes and, if it reduced any platform's entitlements,
	// enqueues a provision-urgent job in the SAME transaction as the RBAC
	// mutation. This is the wiring point, not internal/rbac itself, so
	// internal/rbac compiles and tests with zero knowledge that
	// internal/provisioning exists (roadmap: "a cleaner seam... avoids a
	// new Phase 11 dependency inside a Phase 10 package").
	//
	// Setting a package-level var from two process roles is safe because
	// each role is its own process; within one process this runs once,
	// during startup, before any request is served.
	rbac.PermissionsChangedHook = func(ctx context.Context, s *store.Store, userID uuid.UUID) error {
		return urgent.HandleUserChangeTx(ctx, s, userID, time.Now(), "rbac_change")
	}

	// ── PHASE 20.4: §2.3 TRIGGER ROW 6, "Corporation / alliance departure"
	//
	// The row had NO producer at all — `grep -rln provisioning internal/sync`
	// was empty — and it is not reachable through the hook above, because a
	// departure is not an RBAC mutation. CCP changed a fact about the world,
	// handlers.SyncCharacterSheet wrote the new app.character.corporation_id,
	// nothing recomputed, and a character who had left the corporation that
	// granted their Discord roles kept them until the next nightly bulk
	// reconcile — against §2.1's 60-second p99 bound.
	//
	// Wired HERE rather than in work.go, even though the sync handlers only
	// ever run in `work` today, for exactly the reason this file exists: a
	// trigger wired into one role's boot sequence is a trigger the next role
	// silently does not have, which is the defect 20.3 found and this
	// function was created to make structurally impossible. `serve` setting
	// a hook it never fires costs one assignment.
	//
	// The reason string distinguishes the two moves in app.provisioning_audit
	// so an operator reading the audit can tell a corp departure from an
	// alliance one without joining back to the character's history.
	handlers.AffiliationChangedHook = func(ctx context.Context, change handlers.AffiliationChange) error {
		reason := "alliance_departure"
		if change.CorporationChanged() {
			reason = "corporation_departure"
		}
		return urgent.HandleCharacterChange(ctx, pool, change.CharacterID, reason)
	}

	return urgent
}

// revocationNotifier bridges internal/sso's RevocationNotifier to
// internal/provisioning — the seam Phase 5 left with a Noop default and the
// comment "the call site exists now so Phase 11 only has to implement the
// interface". Phase 11 implemented no such thing; 20.3 does.
//
// NotifyRevocation cannot return an error (the interface is fire-and-
// forget, because a token invalidation must not be undone by a failure to
// recompute entitlements), so a failure here is LOUD in the log and
// nowhere else. That is the correct trade — the invalidation itself has
// already committed, and the periodic reconcile is the backstop — but it
// is also why the log line says so.
type revocationNotifier struct {
	urgent *provisioning.Urgent
	pool   store.Pool
	logger *slog.Logger
}

func (n revocationNotifier) NotifyRevocation(ctx context.Context, characterID int64, reason string) {
	if err := n.urgent.HandleCharacterChange(ctx, n.pool, characterID, reason); err != nil {
		n.logger.ErrorContext(ctx,
			"provisioning: urgent revocation could not be enqueued — the token state HAS changed, so this "+
				"user's platform groups are now stale until the next bulk reconcile",
			"character_id", characterID, "reason", reason, "error", err)
	}
}

// buildTokenLifecycle assembles internal/sso.Lifecycle over the store and
// the revocation bridge. See internal/sso/lifecycle.go's header for which
// paths use it and which invalidate inline instead.
func buildTokenLifecycle(s *store.Store, urgent *provisioning.Urgent, pool store.Pool, logger *slog.Logger) *sso.Lifecycle {
	return sso.NewLifecycle(s, revocationNotifier{urgent: urgent, pool: pool, logger: logger})
}

// notifyScopesReduced is Gate 2 trigger row 3's producer: a login that
// re-authorised a character with fewer scopes than it held before.
//
// No token is invalidated — the token is still valid and still refreshable,
// it can simply read less — so this does not go through Lifecycle. What it
// does is exactly what every other reducing event does: recompute this
// character's owner's entitlements and enqueue whatever that lost.
func notifyScopesReduced(urgent *provisioning.Urgent, pool store.Pool, logger *slog.Logger) func(context.Context, int64, []string) {
	return func(ctx context.Context, characterID int64, removed []string) {
		logger.WarnContext(ctx,
			"sso: a login reduced this character's granted scope set — entitlements derived from the "+
				"withdrawn scopes are being revoked",
			"character_id", characterID, "removed_scopes", removed)
		if err := urgent.HandleCharacterChange(ctx, pool, characterID, "scopes_reduced"); err != nil {
			logger.ErrorContext(ctx,
				"provisioning: urgent revocation after a scope reduction could not be enqueued — this "+
					"user's platform groups are now stale until the next bulk reconcile",
				"character_id", characterID, "error", err)
		}
	}
}
