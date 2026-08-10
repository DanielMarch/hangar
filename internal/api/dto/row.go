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
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Row converts one internal/store/gen struct (or any struct) into a
// snake_case-keyed map suitable for direct JSON encoding. Field order is
// not preserved (map), which is fine — JSON object key order is not part
// of the wire contract. Nested structs recurse; slices of structs recurse
// per element; []byte encodes as a hex string (never raw bytes into JSON,
// and never a struct field named like ciphertext/DEK material — callers
// exclude those before calling Row, this function does not know which
// fields are secret).
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
