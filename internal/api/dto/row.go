// Package dto holds the response body shapes internal/api/v1 hands to
// Huma. Most Phase 15 endpoints (§6.2/§6.3's long tail of small,
// naturally-bounded per-owner sub-resources — skills, clones, standings,
// titles, roles and the like) don't warrant a hand-typed struct: the
// underlying internal/store/gen row already has the right fields, it just
// lacks JSON tags (sqlc emits none — see internal/store/gen/models.go) and
// uses PascalCase, where SRS §6's wire format is snake_case throughout.
// Row and RowSlice bridge that gap generically so those endpoints don't
// each need a hand-maintained mirror struct.
//
// Endpoints where field-level OpenAPI documentation or money-specific
// handling actually matters (auth, wallets, search, admin boards, squads)
// use their own hand-typed struct instead, alongside this file's
// converter — see money.go and the resource-specific files in this
// package.
package dto

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// Row converts one internal/store/gen struct (or any struct) into a
// snake_case-keyed map suitable for direct JSON encoding. Field order is
// not preserved (map), which is fine — JSON object key order is not part
// of the wire contract. Nested structs recurse; slices of structs recurse
// per element.
//
// A `json.RawMessage` field — what sqlc.yaml's two jsonb overrides
// generate for every `jsonb` column, nullable or not — is emitted as
// NESTED JSON, verbatim. A plain `[]byte` field — a genuinely binary
// `bytea` column: a hash, ciphertext, wrapped key material — encodes as a
// hex string (never raw bytes into JSON), and callers exclude those before
// calling Row anyway; this function does not know which fields are secret.
//
// The distinction is by TYPE, not by inspecting the bytes, which is the
// whole point of SRS §6's rule [v3.1 — B12]: `json.RawMessage` IS `[]byte`
// at the language level, so before this change every structured column
// (starbase fuels, skyhook/sov-hub reagents, structure services, planetary
// colony pins/links/routes, esi_pin_history.route_diff — 42 fields plus
// the three nullable ones sqlc.yaml previously missed) reached the wire as
// an opaque hex string that no client could render without decoding a
// HANGAR response field. Sniffing the content instead would be worse, not
// better: a 3-byte hash whose bytes spell "123" is valid JSON.
func Row(v any) map[string]any {
	return rowValue(reflect.ValueOf(v))
}

// RowSlice applies Row to every element of a slice.
func RowSlice[T any](rows []T) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = Row(r)
	}
	return out
}

func rowValue(v reflect.Value) map[string]any {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		out[toSnakeCase(f.Name)] = fieldJSONValue(v.Field(i))
	}
	return out
}

func fieldJSONValue(fv reflect.Value) any {
	switch iv := fv.Interface().(type) {
	case decimal.Decimal, decimal.NullDecimal, time.Time, uuid.UUID, uuid.NullUUID:
		return iv // these all implement json.Marshaler with the correct wire shape
	case json.RawMessage:
		return rawJSON(iv)

	// PHASE 18. pgtype.Date and pgtype.Interval do NOT implement
	// json.Marshaler, so without these two cases the generic struct branch
	// below recurses into their fields and a date reaches the wire as
	// `{"time":"...","infinity_modifier":0,"valid":true}` — the same class
	// of defect as B12, found the same way (a Phase 18 screen was the first
	// thing that had to render one).
	//
	// Both types exist in the generated models only because sqlc.yaml's
	// overrides do not cover them: `date` has no override at all, and the
	// `interval -> time.Duration` override has no nullable variant, so
	// app.esi_route.cache_age/.rate_limit_window fall back to the driver
	// type (see internal/esi/catalogue/sync.go's durationToInterval, which
	// documents the same gap from the write side).
	case pgtype.Date:
		return pgDate(iv)
	case pgtype.Interval:
		return pgInterval(iv)
	}
	switch fv.Kind() {
	case reflect.Pointer:
		if fv.IsNil() {
			return nil
		}
		return fieldJSONValue(fv.Elem())
	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.Uint8 {
			return hex.EncodeToString(fv.Bytes())
		}
		n := fv.Len()
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = fieldJSONValue(fv.Index(i))
		}
		return out
	case reflect.Struct:
		return rowValue(fv)
	default:
		return fv.Interface()
	}
}

// rawJSON prepares one jsonb column's bytes for embedding in the response
// map. Returning the json.RawMessage itself (rather than unmarshalling
// into `any`) keeps the document VERBATIM: no key reordering, and — the
// reason this matters beyond aesthetics — no number is round-tripped
// through float64, which for a jsonb document carrying an ISK amount
// would be a Principle 9 violation introduced by the DTO layer.
//
// Two normalisations:
//
//   - Empty (NULL jsonb, or a zero-length value) becomes JSON `null`.
//     json.RawMessage's own MarshalJSON returns its bytes unchanged, and
//     zero bytes is not a JSON document — encoding/json reports
//     "unexpected end of JSON input" and the whole response 500s.
//   - Bytes that are not valid JSON fall back to hex. Postgres validates
//     jsonb on write so this is unreachable through the store, but the
//     converter is generic and a 500 on the whole response would be a
//     worse failure than one oddly-rendered field.
func rawJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	if !json.Valid(b) {
		return hex.EncodeToString(b)
	}
	return b
}

// pgDate renders a Postgres `date` as the same "YYYY-MM-DD" string every
// other compatibility date in this system uses (ESI's own wire format,
// internal/esi/catalogue.FormatDate). NULL is JSON null.
//
// A date is deliberately NOT rendered as a timestamp: a compatibility
// date has no time and no zone, and emitting "2026-08-11T00:00:00Z" would
// invite a client to apply a local-time conversion that shifts it a day.
func pgDate(d pgtype.Date) any {
	if !d.Valid {
		return nil
	}
	return d.Time.UTC().Format("2006-01-02")
}

// pgInterval renders a Postgres `interval` as whole seconds. Every
// interval column in this schema is a duration (a route's cache_age, a
// rate-limit window, a coalescing window), never a calendar span, so
// months are converted at Postgres's own 30-day convention rather than
// being dropped.
func pgInterval(i pgtype.Interval) any {
	if !i.Valid {
		return nil
	}
	const secondsPerDay = 24 * 60 * 60
	return i.Microseconds/1_000_000 +
		int64(i.Days)*secondsPerDay +
		int64(i.Months)*30*secondsPerDay
}

// toSnakeCase converts a PascalCase Go identifier (sqlc's field naming,
// including runs of capitals like "ID"/"ISK"/"IP") to snake_case.
func toSnakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper {
			prevLower := i > 0 && (runes[i-1] < 'A' || runes[i-1] > 'Z')
			nextLower := i+1 < len(runes) && (runes[i+1] < 'A' || runes[i+1] > 'Z')
			if i > 0 && (prevLower || (nextLower && !prevLower)) {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
