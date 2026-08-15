// ESI service health sync — Appendix A capability #45, and Gate 4.6's
// requirement that /meta/status and /status be "present as two distinct
// capabilities".
//
// ── PHASE 20.7 (B48): THE FIELD THAT COULD NEVER SAY NO ──────────────────
// /status (Tranquility's own player count and version) has been delivered
// since Phase 15.1. /meta/status — ESI's per-route health — was not: no
// dispatch entry, no handler, no table. GET /api/v1/meta/esi-status existed
// and answered from the catalogue's blocked-route count with a LITERAL
// `"healthy": true`, so the one field an operator reads to decide whether
// ESI is the problem was asserted rather than measured and could not report
// unhealthy on any installation, in any circumstance.
//
// This closes it by actually asking ESI. The route is unauthenticated, so
// it is a global subscription like /status and /markets/prices.
//
// ── WHY app.setting AND NOT A NEW TABLE ──────────────────────────────────
// Same reasoning sovereignty.go records for the server status: this is a
// single global snapshot overwritten in place at the route's own cadence,
// with no history, no owner and no foreign keys. A per-route health table
// would imply a time series nothing queries and nothing prunes. If a later
// phase wants ESI health HISTORY it should say so and build the table for
// that question, not inherit one by accident from this one.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/store"
)

// EsiStatusSettingKey is the app.setting key the latest ESI per-route
// health snapshot is stored under.
const EsiStatusSettingKey = "esi.meta_status"

// EsiRouteStatusDTO is one route's reported health. `status` is CCP's own
// vocabulary — "Unknown", "OK", "Degraded", "Down", "Recovering" — stored
// verbatim; HANGAR never narrows it to a boolean at the sync layer, because
// the whole defect being fixed here was a boolean asserted in place of a
// measurement.
type EsiRouteStatusDTO struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

// EsiStatusDTO is GET /meta/status's response envelope.
type EsiStatusDTO struct {
	Routes []EsiRouteStatusDTO `json:"routes"`
}

func ParseEsiStatus(body []byte) (EsiStatusDTO, error) {
	var dto EsiStatusDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return EsiStatusDTO{}, fmt.Errorf("handlers: parsing esi meta status: %w", err)
	}
	return dto, nil
}

// EsiStatusHealthy reports whether a snapshot's route statuses describe a
// healthy ESI.
//
// The rule: healthy means NO route reports "Down" and none reports
// "Degraded". "Unknown" does not count against health — CCP returns it for
// routes with too little recent traffic to judge, which is a statement
// about sampling and not about ESI — and neither does "Recovering", which
// is by definition the way back up.
//
// It is exported and takes the decoded statuses rather than reading the
// setting itself so that both the API layer and its tests can exercise the
// rule on values that never went through Postgres.
func EsiStatusHealthy(routes []EsiRouteStatusDTO) bool {
	for _, r := range routes {
		switch strings.ToLower(r.Status) {
		case "down", "degraded":
			return false
		}
	}
	return true
}

// SyncEsiStatus stores the snapshot, plus the derived counts the API layer
// reports so it does not have to re-walk the route list on every request.
//
// An EMPTY routes array is stored as such rather than rejected: it is a
// well-formed answer meaning "ESI is reporting on no routes right now", and
// EsiStatusHealthy reads it as healthy — there is nothing Down. Refusing to
// store it would leave the endpoint serving the PREVIOUS snapshot while
// claiming it was current, which is the failure mode this whole capability
// exists to remove.
func SyncEsiStatus(ctx context.Context, s *store.Store, dto EsiStatusDTO) (SyncResult, error) {
	var down, degraded int
	for _, r := range dto.Routes {
		switch strings.ToLower(r.Status) {
		case "down":
			down++
		case "degraded":
			degraded++
		}
	}
	payload, err := json.Marshal(map[string]any{
		"routes":         dto.Routes,
		"route_count":    len(dto.Routes),
		"down_count":     down,
		"degraded_count": degraded,
		"healthy":        EsiStatusHealthy(dto.Routes),
		"fetched_at":     time.Now().UTC(),
	})
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: encoding esi meta status: %w", err)
	}
	if err := s.UpsertSetting(ctx, EsiStatusSettingKey, payload, uuid.NullUUID{}); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: storing esi meta status: %w", err)
	}
	return SyncResult{RowsAffected: int32(len(dto.Routes))}, nil
}
