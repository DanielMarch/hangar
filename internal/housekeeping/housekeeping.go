// Package housekeeping runs HANGAR's retention sweeps: the periodic
// deletion of rows that are past the point where anything can read them.
//
// ── WHY THIS PACKAGE EXISTS (defect B-2, pre-v1.0 audit) ─────────────────
// db/queries has held DeleteExpiredSessions, DeleteExpiredEsiCacheEntries
// and DeleteStaleReplicas since the phases that created their tables, and
// until this one NONE OF THE THREE HAD A PRODUCTION CALLER. There was no
// janitor loop, no cron and no River periodic job anywhere in the tree, so
// every installation retained every expired row it had ever written. On the
// installation the audit measured, 19 of 22 app.session rows were expired.
//
// ── WHAT THAT WAS, STATED PRECISELY ──────────────────────────────────────
// It is easy to over- and under-sell, so both halves are recorded here.
//
// It was NOT an authentication hole. GetSession filters `expires_at >
// now()` (db/queries/user.sql), so an expired session row cannot
// authenticate — it is unreachable, not dangerous.
//
// It WAS a data-retention defect, and that is the reason this package is
// wired rather than the reason it is convenient. app.session holds
// ip_address, user_agent and pkce_verifier: personal data, retained
// indefinitely, with no deletion path in the product at all.
//
// The other two are smaller and are described honestly rather than
// inherited from the allowlist that named them:
//
//   - app.esi_cache_entry was never unbounded. UpsertEsiCacheEntry is keyed
//     on cache_key and overwrites, so the table is bounded by the number of
//     distinct cache keys in flight. This sweep reclaims the tail of keys
//     that stopped being requested — disk, not growth without limit.
//   - app.esi_replica accumulates one row per force-killed process. Harmless
//     to mode selection, because CountLiveReplicas filters on the heartbeat
//     window and a stale row was already invisible to it.
//
// ── THE RETENTION WINDOWS ────────────────────────────────────────────────
//
//	app.session           until expires_at, plus at most one sweep interval.
//	                      expires_at is set from HANGAR_SESSION_TTL (720h by
//	                      default) when a login completes, and from
//	                      sso.StateTTL (10m) for a pre-auth PKCE row that is
//	                      never completed. There is deliberately no grace
//	                      period beyond expiry: an expired session cannot
//	                      authenticate, cannot be resumed, and the row's
//	                      remaining contents are personal data. app.session
//	                      is not an audit table — §4.9's outbox and
//	                      app.provisioning_audit are.
//	app.esi_cache_entry   until expires_at, plus at most one sweep interval.
//	                      L2 reads already filter on expires_at.
//	app.esi_replica       ReplicaRetention after the last heartbeat, which is
//	                      NOT the liveness threshold. See the guard in Tick.
package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/telemetry"
)

// Store is the narrow slice of the repository this sweep needs. An
// interface rather than *store.Store so the sweeper can be driven against a
// double in a unit test, and so that the three queries it wires are visible
// in one place.
type Store interface {
	DeleteExpiredSessions(ctx context.Context) (int64, error)
	DeleteExpiredEsiCacheEntries(ctx context.Context) (int64, error)
	DeleteStaleReplicas(ctx context.Context, retention time.Duration) (int64, error)
}

// MinReplicaRetention is the floor this package will accept for
// ReplicaRetention, and it is a safety guard rather than a tuning
// preference.
//
// app.esi_replica is the registry CountLiveReplicas reads to choose solo or
// clustered mode. A retention window at or near telemetry.LiveThreshold
// (30s) would delete the registration of a replica whose heartbeat was
// merely LATE — and on a two-replica installation that converts a transient
// stall into a mode flip, in which two replicas each believe they are solo
// and each spend the whole bucket. That is a Governor 1 breach (Gate 1.1)
// manufactured by a janitor. Ten minutes is twenty times the liveness
// threshold; the default is far above even that.
const MinReplicaRetention = 10 * time.Minute

// Sweeper performs one retention pass per Tick.
type Sweeper struct {
	Store Store
	// ReplicaRetention is how long a dead replica's row is kept after its
	// last heartbeat. Must be at least MinReplicaRetention.
	ReplicaRetention time.Duration
}

// Result is what one pass deleted. Reported so a sweep that is running and
// finding nothing is distinguishable from one that is not running — which
// is precisely the state this package was written to end.
type Result struct {
	Sessions     int64
	CacheEntries int64
	Replicas     int64
}

// Total is the row count across all three tables, for the caller that only
// wants to know whether the pass did anything.
func (r Result) Total() int64 { return r.Sessions + r.CacheEntries + r.Replicas }

// ErrRetentionTooShort is returned by Tick when ReplicaRetention is below
// MinReplicaRetention. Refusing is deliberate: the delete this guards can
// cause a Governor 1 breach, so a misconfigured sweeper must do nothing to
// app.esi_replica rather than something destructive.
var ErrRetentionTooShort = errors.New("housekeeping: replica retention is shorter than the safe floor")

// Tick runs one pass of all three sweeps.
//
// The three statements are deliberately NOT in one transaction. They are
// independent reclaims of independent tables with no invariant between
// them, and wrapping them would hold a write transaction open across three
// scans to buy an atomicity nothing reads.
//
// Sessions are swept FIRST, because that is the sweep with a personal-data
// reason behind it; the other two are disk. A failure in a later sweep
// therefore cannot prevent the earlier one from having run, and Result
// carries what did succeed alongside the error.
func (s *Sweeper) Tick(ctx context.Context) (Result, error) {
	var res Result

	sessions, err := s.Store.DeleteExpiredSessions(ctx)
	res.Sessions = sessions
	if err != nil {
		return res, fmt.Errorf("housekeeping: deleting expired sessions: %w", err)
	}

	entries, err := s.Store.DeleteExpiredEsiCacheEntries(ctx)
	res.CacheEntries = entries
	if err != nil {
		return res, fmt.Errorf("housekeeping: deleting expired ESI cache entries: %w", err)
	}

	if s.ReplicaRetention < MinReplicaRetention {
		return res, fmt.Errorf("%w: %s is below the %s floor, which risks deleting a live replica's "+
			"registration and flipping the ledger to solo on every replica at once",
			ErrRetentionTooShort, s.ReplicaRetention, MinReplicaRetention)
	}
	// Negative, because the query adds it to now(): a row is stale when its
	// last_heartbeat is at or before `now() - ReplicaRetention`. The same
	// sign convention CountLiveReplicas' caller uses.
	replicas, err := s.Store.DeleteStaleReplicas(ctx, -s.ReplicaRetention)
	res.Replicas = replicas
	if err != nil {
		return res, fmt.Errorf("housekeeping: deleting stale replicas: %w", err)
	}

	return res, nil
}

// SafeReplicaRetention reports whether d is a retention window this package
// will act on, for a caller that wants to reject a bad configuration at
// boot rather than discover it on the first tick.
//
// cmd/hangar calls it while wiring the sweeper, and NOT internal/config,
// deliberately: that package stays free of dependencies on the subsystems
// it configures (the same reason ESIConfig does not import internal/esi),
// and the alternative — restating MinReplicaRetention as a literal inside
// validate.go — is the second hand-maintained copy of a constant that
// exists to prevent a Governor 1 breach.
func SafeReplicaRetention(d time.Duration) bool { return d >= MinReplicaRetention }

// LivenessThreshold re-exports the window CountLiveReplicas treats as live,
// so a caller reasoning about retention can see both numbers together
// without importing telemetry for one constant.
const LivenessThreshold = telemetry.LiveThreshold
