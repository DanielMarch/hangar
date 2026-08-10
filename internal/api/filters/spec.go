// Package filters implements SRS §6 / roadmap Phase 15's whitelisted query
// filter specifications: every list endpoint that accepts filter query
// parameters validates them against a closed Spec before they ever reach a
// store query. An unknown field name, a type-confused value, or a value
// carrying SQL-metacharacters is a 422, never a best-effort pass-through —
// "the API layer must not reintroduce" the kind of unbounded query surface
// OFFSET pagination and free-text filtering both represent.
package filters

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
