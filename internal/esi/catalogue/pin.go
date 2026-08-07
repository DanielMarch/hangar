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

// AdvancePin sets a new app pin and records the advance in
// app.esi_pin_history (old, new, actor, route diff). The pin is NEVER
// advanced automatically (01_ARCHITECTURE.md §5.1) — every caller of this
// function is, by construction, an explicit administrator action; there is
// deliberately no code path anywhere in this package that calls it as a
// side effect of a routine ingest.
func AdvancePin(ctx context.Context, q Store, newPin time.Time, actor string, routeDiff json.RawMessage) (gen.AppEsiPinHistory, error) {
	oldPin, err := GetPin(ctx, q)
	if err != nil {
		return gen.AppEsiPinHistory{}, err
	}
	if routeDiff == nil {
		routeDiff = json.RawMessage(`{}`)
	}
	if err := setPinSetting(ctx, q, newPin); err != nil {
		return gen.AppEsiPinHistory{}, err
	}
	oldPinPG := pgDate(oldPin)
	rec, err := q.RecordEsiPinAdvance(ctx, gen.RecordEsiPinAdvanceParams{
		OldPin:    oldPinPG,
		NewPin:    pgDate(newPin),
		Actor:     actor,
		RouteDiff: routeDiff,
	})
	if err != nil {
		return gen.AppEsiPinHistory{}, fmt.Errorf("catalogue: recording pin advance: %w", err)
	}
	return rec, nil
}
