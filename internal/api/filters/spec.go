// Package filters implements SRS §6 / roadmap Phase 15's whitelisted query
// filter specifications: every list endpoint that accepts filter query
// parameters validates them against a closed Spec before they ever reach a
// store query. An unknown field name, a type-confused value, or a value
// carrying SQL-metacharacters is a 422, never a best-effort pass-through —
// "the API layer must not reintroduce" the kind of unbounded query surface
// OFFSET pagination and free-text filtering both represent.
package filters

import (
	"reflect"
	"strings"
	"time"
)

// FieldType is the closed set of filter value types this package can
// coerce and validate. Every value still travels to the store layer as a
// bind parameter (never string-interpolated SQL) — FieldType only decides
// how validate.go parses the raw query-string value.
type FieldType int

const (
	FieldString FieldType = iota
	FieldInt
	FieldUUID
	FieldBool
	FieldTime

	// FieldOpaque is a value HANGAR itself minted and will decode with its
	// own parser — today, only the `after`/`before` keyset cursors.
	//
	// It exists because the FieldString path applies a SQL-metacharacter
	// heuristic, and a cursor is base64url: its alphabet includes `-`, so a
	// perfectly valid cursor can contain `--` and would be rejected as an
	// injection attempt. The whitelist half still applies — an opaque field
	// must be DECLARED to be accepted — and the value is then handed to
	// api.ParsePageRequest, which is the only thing that knows what a valid
	// cursor is. Widening FieldString's rules instead would have weakened
	// the check for every genuine string filter to accommodate one encoding.
	FieldOpaque
)

// Field is one whitelisted filter parameter: its wire name and the type
// its value must coerce to.
type Field struct {
	Name string
	Type FieldType
}

// Spec is one resource's closed set of filterable fields. Constructed once
// per list endpoint (package-level vars in internal/api/v1) and passed to
// Validate on every request.
type Spec struct {
	Resource string
	Fields   map[string]Field
}

// New builds a Spec from its field list.
func New(resource string, fields ...Field) Spec {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return Spec{Resource: resource, Fields: m}
}

// ── PHASE 20.5, DEFECT B33 ───────────────────────────────────────────────
//
// New and Validate had no production caller. The consequence was the third
// of the three possible answers to a hostile filter, and the worst one: a
// query parameter no endpoint declares was SILENTLY IGNORED and the endpoint
// answered 200 with the whole collection. `GET /api/v1/characters/{id}/
// contacts?standing=-10` returned every contact, and
// `?$filter=standing lt 0` did too — which is precisely the danger
// APPENDIX_C_MIGRATION.md §6.6 spells out for the /api/v2 shim ("a filter
// that narrows a result set and is then dropped returns MORE data than was
// asked for") and then only defends the shim against.
//
// The fix does not require every endpoint to declare a Spec by hand. Each
// operation already declares its accepted query parameters, in the `query:`
// struct tags of its own input type, and huma builds the OpenAPI document
// from exactly those. SpecFromQueryTags reads the same tags, so the closed
// set can never drift from the documented one — a filter surface maintained
// in two places is how B33 would come back.

// SpecFromQueryTags builds a Spec from the `query:` struct tags of an
// operation's input type. `in` may be a struct or a pointer to one;
// anything else yields an empty spec, which is the correct closed set for
// an operation that takes no query parameters at all.
//
// The FieldType is inferred from the Go type, with two refinements taken
// from tags huma also reads: `format:"uuid"` makes a string a FieldUUID,
// and the two cursor parameters are FieldOpaque (see its doc comment).
func SpecFromQueryTags(resource string, in any) Spec {
	t := reflect.TypeOf(in)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return New(resource)
	}

	var fields []Field
	for i := range t.NumField() {
		f := t.Field(i)
		name, ok := f.Tag.Lookup("query")
		if !ok {
			continue
		}
		// huma accepts `query:"name,omitempty"`; only the name matters here.
		if comma := strings.IndexByte(name, ','); comma >= 0 {
			name = name[:comma]
		}
		if name == "" {
			continue
		}
		fields = append(fields, Field{Name: name, Type: fieldTypeOf(name, f)})
	}
	return New(resource, fields...)
}

// cursorParams are the two query parameters whose values HANGAR minted.
var cursorParams = map[string]bool{"after": true, "before": true}

func fieldTypeOf(name string, f reflect.StructField) FieldType {
	if cursorParams[name] {
		return FieldOpaque
	}
	if f.Tag.Get("format") == "uuid" {
		return FieldUUID
	}
	switch f.Type.Kind() {
	case reflect.String:
		return FieldString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return FieldInt
	case reflect.Bool:
		return FieldBool
	default:
		if f.Type == reflect.TypeOf(time.Time{}) {
			return FieldTime
		}
		// An input shape this package has no rule for is treated as a
		// declared-but-unchecked parameter rather than an undeclared one:
		// refusing it would break a documented endpoint over a gap in THIS
		// file, and huma's own binder still type-checks it.
		return FieldOpaque
	}
}
