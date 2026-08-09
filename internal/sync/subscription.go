package sync

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// EntityKind is HANGAR's own closed vocabulary for what a subscription is
// scoped to (app.sync_subscription.entity_kind). It is HANGAR's own
// invention, not a CCP vocabulary, so the "external vocabularies stay
// opaque text" rule (01_ARCHITECTURE.md invariant 3 / defect register B11)
// does not apply here — contrast internal/scopes, which must never do this.
type EntityKind string

const (
	EntityCharacter   EntityKind = "character"
	EntityCorporation EntityKind = "corporation"
	EntityAlliance    EntityKind = "alliance"
	EntityGlobal      EntityKind = "global"
)

// RouteCacheConfig is the subset of app.esi_route a due-time computation
// needs: the declared cache contract. BlockedByPin is carried for callers
// that assemble this from a joined row, even though the planner's claim
// query (db/queries/sync_subscription.sql) already excludes blocked routes
// at the SQL level — never as a post-claim filter (§6.1 edge case).
type RouteCacheConfig struct {
	CacheMode    string // raw x-cache-mode; "" = absent
	CacheAge     time.Duration
	BlockedByPin bool
}

// PolicyConfig bundles the two installation-wide knobs the cache-mode
// policy needs (internal/config.SyncConfig / ESIConfig): the TTL floor
// applied regardless of what the spec declares, and the adaptive-backoff
// ceiling.
type PolicyConfig struct {
	TTLFloor   time.Duration
	BackoffCap time.Duration
}

// ErrNoCacheNotOptedIn is returned by PlanNextDueAt when a route's cache
// mode is no-cache but its subscription has not set opt_in_no_cache — §6.2:
// "only for subscriptions that explicitly opt in".
var ErrNoCacheNotOptedIn = errors.New("sync: route is no-cache but subscription has not opted in")

// IntervalToDuration converts a nullable Postgres interval (app.esi_route's
// cache_age, scanned as pgtype.Interval because it's a nullable column —
// see internal/esi/catalogue/sync.go's durationToInterval for the inverse
// and why sqlc's non-nullable time.Duration override doesn't apply here) to
// a time.Duration. Days/months are converted using calendar approximations
// (24h/30d) purely as a defensive fallback: every ESI x-cache-age value
// observed is sub-day, expressed entirely in the Microseconds field.
func IntervalToDuration(iv pgtype.Interval) time.Duration {
	if !iv.Valid {
		return 0
	}
	d := time.Duration(iv.Microseconds) * time.Microsecond
	d += time.Duration(iv.Days) * 24 * time.Hour
	d += time.Duration(iv.Months) * 30 * 24 * time.Hour
	return d
}

// ActingCharacterElector is the §6.3 seam Phase 6 exposes but does not
// fully implement: for corp-scoped subscriptions, deterministically elect
// the healthiest director token per the ordering in
// 01_ARCHITECTURE.md §6.3 (token valid -> has required scopes -> has
// required corp roles -> fewest recent 403s -> lowest character_id
// tiebreak). That ordering needs the token store and per-route
// x-required-roles data Phase 7+ introduces; Phase 6 defines only the
// contract so corp-scoped subscription creation has something to compile
// and test against, and db/queries/sync_subscription.sql's
// ElectActingCharacter is the persistence half already in place since
// Phase 1a.
type ActingCharacterElector interface {
	// Elect returns the character_id that should act for (entityKind,
	// entityID) on routeID, or an error if no eligible character exists.
	Elect(ctx context.Context, entityKind EntityKind, entityID int64, routeID uuid.UUID) (characterID int64, err error)
}

// ErrElectionNotImplemented is returned by StubElector, the placeholder
// ActingCharacterElector Phase 6 wires nowhere by default (nothing in this
// phase's scope calls it) but exposes so a Phase 7+ caller has a
// compile-time-safe placeholder before the real health-ordered elector
// lands.
var ErrElectionNotImplemented = errors.New("sync: acting-character election requires Phase 7+ token/role data")

// StubElector is the Phase 6 seam implementation of ActingCharacterElector:
// it always fails, deliberately, rather than guess. A silent wrong guess
// here would make 403 debugging non-deterministic — exactly what §6.3
// warns against — so the stub refuses instead of fabricating an answer.
type StubElector struct{}

func (StubElector) Elect(context.Context, EntityKind, int64, uuid.UUID) (int64, error) {
	return 0, ErrElectionNotImplemented
}
