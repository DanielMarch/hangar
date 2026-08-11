package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
)

// CompatibilityPinSettingKey is app.setting's key for the app-wide
// compatibility date pin (01_ARCHITECTURE.md §5.1's "App pin", `P`).
// Referenced verbatim from 00004_platform_identity.sql's own comment on
// app.setting.
const CompatibilityPinSettingKey = "esi.compatibility_pin"

// DefaultCompatibilityPin is the SRS v3.1-mandated seed value
// (01_ARCHITECTURE.md header: "Compatibility date pin: 2026-08-04").
const DefaultCompatibilityPin = "2026-08-04"

// GetPin reads the app-wide compatibility date pin, seeding it to
// DefaultCompatibilityPin on first boot if app.setting has no row for it
// yet. This is the ONLY place the pin is set without an explicit
// administrator action — every other change goes through AdvancePin.
func GetPin(ctx context.Context, q Store) (time.Time, error) {
	row, err := q.GetSetting(ctx, CompatibilityPinSettingKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			seed, perr := ParseDate(DefaultCompatibilityPin)
			if perr != nil {
				return time.Time{}, fmt.Errorf("catalogue: parsing DefaultCompatibilityPin: %w", perr)
			}
			if err := setPinSetting(ctx, q, seed); err != nil {
				return time.Time{}, err
			}
			return seed, nil
		}
		return time.Time{}, fmt.Errorf("catalogue: reading %s: %w", CompatibilityPinSettingKey, err)
	}
	var s string
	if err := json.Unmarshal(row.Value, &s); err != nil {
		return time.Time{}, fmt.Errorf("catalogue: %s is not a JSON string: %w", CompatibilityPinSettingKey, err)
	}
	pin, err := ParseDate(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("catalogue: %s %q is not a compatibility date: %w", CompatibilityPinSettingKey, s, err)
	}
	return pin, nil
}

func setPinSetting(ctx context.Context, q Store, pin time.Time) error {
	value, err := json.Marshal(FormatDate(pin))
	if err != nil {
		return fmt.Errorf("catalogue: encoding pin: %w", err)
	}
	if err := q.UpsertSetting(ctx, CompatibilityPinSettingKey, value, uuid.NullUUID{}); err != nil {
		return fmt.Errorf("catalogue: writing %s: %w", CompatibilityPinSettingKey, err)
	}
	return nil
}

// DMaxSettingKey is app.setting's key for the newest compatibility date
// /meta/compatibility-dates reported on the last catalogue ingest
// (01_ARCHITECTURE.md §5.1's `D_max`). Boot records it; GetDMax reads it.
//
// [v3.1 — B13] Before Phase 18 nothing persisted D_max at all: Boot
// computed it into BootResult.DMax and dropped it on the floor, which is
// why "reject a candidate newer than D_max" could only ever have been
// implemented client-side. A UI-only bound check is bypassed by any direct
// API call, so it was not the criterion and never was.
const DMaxSettingKey = "esi.d_max"

// OutOfRangeError is returned by AdvancePin when the candidate pin is
// newer than D_max. Typed so the API layer can map it to 422 rather than
// string-matching an error, and so the message carries both bounds — an
// administrator who is refused needs to be told what the ceiling is.
type OutOfRangeError struct {
	Candidate time.Time
	DMax      time.Time
	// DMaxSource is "recorded" when D_max came from the last ingest, or
	// "rollover-today" when no ingest has recorded one and the bound fell
	// back to ESI's own current compatibility date.
	DMaxSource string
}

func (e *OutOfRangeError) Error() string {
	return fmt.Sprintf(
		"catalogue: compatibility date %s is newer than D_max %s (%s) — ESI has not published it, so pinning to it would blind every route",
		FormatDate(e.Candidate), FormatDate(e.DMax), e.DMaxSource)
}

// SetDMax records the D_max observed by an ingest. Called by Boot; not
// exported for general use beyond that and the tests that seed a bound.
func SetDMax(ctx context.Context, q Store, dMax time.Time) error {
	value, err := json.Marshal(FormatDate(dMax))
	if err != nil {
		return fmt.Errorf("catalogue: encoding d_max: %w", err)
	}
	if err := q.UpsertSetting(ctx, DMaxSettingKey, value, uuid.NullUUID{}); err != nil {
		return fmt.Errorf("catalogue: writing %s: %w", DMaxSettingKey, err)
	}
	return nil
}

// GetDMax resolves the ceiling a candidate pin is validated against,
// reporting where it came from.
//
// The recorded value is clamped to ESI's own current compatibility date:
// ESI rejects a future compatibility date outright (01_ARCHITECTURE.md
// §5.1), so a D_max recorded days ago can never license pinning past
// today. When nothing has been recorded — no ingest has run — the bound
// falls back to that same rollover-today value. That fallback is weaker
// than a real D_max but it is still SOUND (D_max <= today always holds),
// and a weaker real bound is worth more than no server-side bound at all,
// which is what the endpoint had before this phase.
func GetDMax(ctx context.Context, q Store, now time.Time) (time.Time, string, error) {
	today := CurrentDate(now)
	row, err := q.GetSetting(ctx, DMaxSettingKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return today, "rollover-today", nil
		}
		return time.Time{}, "", fmt.Errorf("catalogue: reading %s: %w", DMaxSettingKey, err)
	}
	var s string
	if err := json.Unmarshal(row.Value, &s); err != nil {
		return time.Time{}, "", fmt.Errorf("catalogue: %s is not a JSON string: %w", DMaxSettingKey, err)
	}
	recorded, err := ParseDate(s)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("catalogue: %s %q is not a compatibility date: %w", DMaxSettingKey, s, err)
	}
	return ClampToToday(recorded, now), "recorded", nil
}

// PinPreview is the non-mutating answer to "what would happen if I moved
// the pin to this date" — POST /api/v1/admin/esi/catalogue/pin/preview
// (SRS §6.8, [v3.1 — B13]).
type PinPreview struct {
	CurrentPin   string    `json:"current_pin"`
	CandidatePin string    `json:"candidate_pin"`
	DMax         string    `json:"d_max"`
	DMaxSource   string    `json:"d_max_source"`
	WithinBounds bool      `json:"within_bounds"`
	Diff         RouteDiff `json:"diff"`
}

// PreviewPin computes the full route diff for a candidate compatibility
// date WITHOUT changing any state. This is a separate operation from
// AdvancePin, not a flag on it, because Principle 12 requires the
// administrator to see the diff BEFORE the pin moves and one mutating
// endpoint cannot satisfy that.
//
// An out-of-range candidate is previewed rather than refused: the caller
// gets `within_bounds: false` alongside the real diff and the actual
// ceiling, which is strictly more informative than an error, and the
// refusal that matters is AdvancePin's — see its own contract.
func PreviewPin(ctx context.Context, q Store, candidate time.Time, now time.Time) (PinPreview, error) {
	currentPin, err := GetPin(ctx, q)
	if err != nil {
		return PinPreview{}, err
	}
	dMax, source, err := GetDMax(ctx, q, now)
	if err != nil {
		return PinPreview{}, err
	}
	diff, err := ComputeRouteDiff(ctx, q, currentPin, candidate)
	if err != nil {
		return PinPreview{}, err
	}
	return PinPreview{
		CurrentPin:   FormatDate(currentPin),
		CandidatePin: FormatDate(candidate),
		DMax:         FormatDate(dMax),
		DMaxSource:   source,
		WithinBounds: !candidate.After(dMax),
		Diff:         diff,
	}, nil
}

// AdvancePin sets a new app pin and records the advance in
// app.esi_pin_history (old, new, actor, route diff). The pin is NEVER
// advanced automatically (01_ARCHITECTURE.md §5.1) — every caller of this
// function is, by construction, an explicit administrator action; there is
// deliberately no code path anywhere in this package that calls it as a
// side effect of a routine ingest.
//
// [v3.1 — B13] Two guarantees this function did not previously make:
//
//   - The candidate is validated against D_max HERE, server-side, and
//     refused with an *OutOfRangeError. The old signature accepted any
//     date, so the only bound check in the system was the client's, which
//     any direct API call bypasses.
//   - The recorded route_diff is COMPUTED, not supplied. The diff used to
//     be a `json.RawMessage` parameter that the one and only caller passed
//     `nil` for, which this function then substituted with `{}` — so every
//     diff recorded before this phase is empty. Taking `now` instead of a
//     diff means the old mistake no longer compiles.
//
// There is deliberately NO lower bound. Moving the pin backwards is a
// legitimate rollback, and the diff reports what it re-blocks.
func AdvancePin(ctx context.Context, q Store, newPin time.Time, actor string, now time.Time) (gen.AppEsiPinHistory, RouteDiff, error) {
	oldPin, err := GetPin(ctx, q)
	if err != nil {
		return gen.AppEsiPinHistory{}, RouteDiff{}, err
	}
	dMax, source, err := GetDMax(ctx, q, now)
	if err != nil {
		return gen.AppEsiPinHistory{}, RouteDiff{}, err
	}
	if newPin.After(dMax) {
		return gen.AppEsiPinHistory{}, RouteDiff{}, &OutOfRangeError{Candidate: newPin, DMax: dMax, DMaxSource: source}
	}

	// Computed BEFORE the pin moves: the diff describes the transition
	// old -> new, and ListEsiRoutes' own blocked_by_pin column is stale the
	// moment setPinSetting lands (it is only recomputed by the next
	// ingest). Ordering it this way also means a failure to compute the
	// diff leaves the pin untouched rather than advancing it undocumented.
	diff, err := ComputeRouteDiff(ctx, q, oldPin, newPin)
	if err != nil {
		return gen.AppEsiPinHistory{}, RouteDiff{}, err
	}
	routeDiff, err := MarshalRouteDiff(diff)
	if err != nil {
		return gen.AppEsiPinHistory{}, RouteDiff{}, err
	}

	if err := setPinSetting(ctx, q, newPin); err != nil {
		return gen.AppEsiPinHistory{}, RouteDiff{}, err
	}
	rec, err := q.RecordEsiPinAdvance(ctx, gen.RecordEsiPinAdvanceParams{
		OldPin:    pgDate(oldPin),
		NewPin:    pgDate(newPin),
		Actor:     actor,
		RouteDiff: routeDiff,
	})
	if err != nil {
		return gen.AppEsiPinHistory{}, RouteDiff{}, fmt.Errorf("catalogue: recording pin advance: %w", err)
	}
	return rec, diff, nil
}
