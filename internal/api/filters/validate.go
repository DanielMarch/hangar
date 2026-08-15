package filters

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidFilter is what Validate returns for any rejected field —
// unknown name, wrong type, or a value that fails the defense-in-depth
// SQL-metacharacter check. internal/api's FilterError wraps it as a 422.
type ErrInvalidFilter struct {
	Field  string
	Reason string
}

func (e *ErrInvalidFilter) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// Value is one validated, typed filter value: int64, string, uuid.UUID,
// bool or time.Time depending on the field's declared FieldType.
type Value any

// sqlMeta is the defense-in-depth character set rejected on FieldString
// values. Every filter value is always bound as a parameter downstream,
// never interpolated into a query string, so this is not what prevents SQL
// injection — it exists so an obviously adversarial payload (a quote, a
// statement terminator, a backslash escape) is rejected at the API layer
// with a 422 rather than silently passed through to become a literal
// string match that merely returns zero rows.
var sqlMeta = []rune{';', '\'', '"', '\\', '\x00'}

// Validate checks raw (a request's decoded query parameters, one value per
// key — repeated keys are the caller's problem, take the last) against
// spec: every key must be a whitelisted field, and its value must coerce to
// that field's type. Returns the fully typed filter set or the first
// rejection encountered as *ErrInvalidFilter.
func Validate(spec Spec, raw map[string]string) (map[string]Value, error) {
	out := make(map[string]Value, len(raw))
	for k, v := range raw {
		field, ok := spec.Fields[k]
		if !ok {
			return nil, &ErrInvalidFilter{Field: k, Reason: "unknown filter field for " + spec.Resource}
		}
		val, err := coerce(field.Type, v)
		if err != nil {
			return nil, &ErrInvalidFilter{Field: k, Reason: err.Error()}
		}
		out[k] = val
	}
	return out, nil
}

func coerce(t FieldType, v string) (Value, error) {
	switch t {
	case FieldOpaque:
		// Whitelisted by name only — see FieldOpaque's doc comment. The
		// value's own parser (api.ParsePageRequest for a cursor) rejects a
		// malformed one with a 400, and it is the only thing that can tell.
		return v, nil
	case FieldString:
		if containsSQLMeta(v) {
			return nil, fmt.Errorf("contains disallowed characters")
		}
		return v, nil
	case FieldInt:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("not an integer")
		}
		return n, nil
	case FieldUUID:
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("not a uuid")
		}
		return id, nil
	case FieldBool:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("not a bool")
		}
		return b, nil
	case FieldTime:
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("not an RFC3339 timestamp")
		}
		return ts, nil
	default:
		return nil, fmt.Errorf("unsupported field type")
	}
}

func containsSQLMeta(v string) bool {
	if strings.ContainsAny(v, string(sqlMeta)) {
		return true
	}
	// A bare SQL comment marker or a classic tautology fragment is still
	// worth rejecting outright even without a quote character present.
	lower := strings.ToLower(v)
	for _, frag := range []string{"--", "/*", "*/", " or ", " and ", "union select", "drop table"} {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}
