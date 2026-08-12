// Package v2shim serves the read-only `/api/v2` sunset shim: SRS §10's
// translation layer that maps legacy SeAT request and response shapes onto
// HANGAR's data, so a third-party integration written against legacy has
// something to keep working while it migrates.
//
// encode.go is the part that makes "byte-compatible" achievable at all.
// Gate 7 is not "the same values" — the roadmap is explicit that it is
// "field order and JSON formatting too". Legacy is PHP: its JSON objects
// come out in insertion order, its `/` characters are escaped, its
// non-ASCII is `\uXXXX`, and its numbers are formatted by a C `%G` with
// precision 17 over the shortest round-tripping digits. Go's encoding/json
// agrees with none of those, and a Go `map[string]any` has no order at all.
// So this file provides an ordered object and an encoder that reproduces
// PHP's rules, and testdata/legacy-api-v2/ is what proves it.
package v2shim

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Obj is a JSON object that remembers its insertion order.
//
// The whole point: legacy's field order is not alphabetical and not
// arbitrary — for a Resource-backed route it is the order of the literal
// array in the PHP `toArray()`, and for a raw-model route it is the
// physical column order of a MySQL table shaped by 472 migrations. Either
// way it is data, and a Go map would destroy it.
type Obj struct {
	keys []string
	vals []any
}

// NewObj builds an object with capacity for n fields.
func NewObj(n int) *Obj { return &Obj{keys: make([]string, 0, n), vals: make([]any, 0, n)} }

// Set appends a field. A repeated key overwrites in place, keeping its
// original position — the same thing PHP's `$array['k'] = v` does.
func (o *Obj) Set(key string, value any) *Obj {
	for i, k := range o.keys {
		if k == key {
			o.vals[i] = value
			return o
		}
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, value)
	return o
}

// Get returns a field's value and whether it was present.
func (o *Obj) Get(key string) (any, bool) {
	for i, k := range o.keys {
		if k == key {
			return o.vals[i], true
		}
	}
	return nil, false
}

// Keys is the field order.
func (o *Obj) Keys() []string { return o.keys }

// Arr is an ordered JSON array. A distinct type from []any so that a nil
// Arr can be told apart from an empty one at encode time: legacy emits
// `[]` for an empty collection and the shim must never turn that into
// `null`. Phase 18 found an empty success is easy to mistake for a
// failure; this is where that mistake would be silent.
type Arr []any

// Num is a JSON number that must be formatted the way PHP formats a
// double. Used for every legacy money field (which is a MySQL DOUBLE
// upstream) and for the float columns — positions, standings, tax rates.
type Num float64

// Int is a JSON integer. Distinct from Num because PHP prints an integer
// column as an integer with no float formatting involved at all.
type Int int64

// Raw is bytes spliced in verbatim, for a value already encoded elsewhere.
type Raw []byte

// Encode renders v using PHP's json_encode default flags.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeValue(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeValue(buf *bytes.Buffer, v any) error {
	switch value := v.(type) {
	case nil:
		buf.WriteString("null")
	case *Obj:
		if value == nil {
			buf.WriteString("null")
			return nil
		}
		buf.WriteByte('{')
		for i, key := range value.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encodeString(buf, key)
			buf.WriteByte(':')
			if err := encodeValue(buf, value.vals[i]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case Arr:
		if value == nil {
			// A nil Arr is `null`; an empty non-nil Arr is `[]`. Callers
			// must mean one or the other — see Arr's doc comment.
			buf.WriteString("null")
			return nil
		}
		buf.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case Raw:
		buf.Write(value)
	case string:
		encodeString(buf, value)
	case *string:
		if value == nil {
			buf.WriteString("null")
		} else {
			encodeString(buf, *value)
		}
	case bool:
		if value {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case *bool:
		if value == nil {
			buf.WriteString("null")
		} else {
			return encodeValue(buf, *value)
		}
	case Int:
		buf.WriteString(strconv.FormatInt(int64(value), 10))
	case *Int:
		if value == nil {
			buf.WriteString("null")
		} else {
			buf.WriteString(strconv.FormatInt(int64(*value), 10))
		}
	case Num:
		s, err := formatPHPDouble(float64(value))
		if err != nil {
			return err
		}
		buf.WriteString(s)
	case *Num:
		if value == nil {
			buf.WriteString("null")
		} else {
			return encodeValue(buf, *value)
		}
	default:
		return fmt.Errorf("v2shim: cannot encode %T — every value must be an explicit shim type so its JSON form is deliberate", v)
	}
	return nil
}

// encodeString applies PHP json_encode's DEFAULT escaping, which differs
// from Go's in both directions:
//
//   - PHP escapes `/` as `\/` (no JSON_UNESCAPED_SLASHES). Every URL in a
//     legacy pagination envelope is affected, so this is not a corner case.
//   - PHP escapes all non-ASCII as \uXXXX (no JSON_UNESCAPED_UNICODE).
//   - PHP does NOT escape `<`, `>`, `&` or `'` by default; Go's
//     encoding/json escapes the first three unless HTML escaping is
//     switched off.
func encodeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '/':
			buf.WriteString(`\/`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(buf, `\u%04x`, r)
			case r < utf8.RuneSelf:
				buf.WriteByte(byte(r))
			case r > 0xFFFF:
				// Outside the BMP: PHP emits a UTF-16 surrogate pair.
				high, low := utf16.EncodeRune(r)
				fmt.Fprintf(buf, `\u%04x\u%04x`, high, low)
			default:
				fmt.Fprintf(buf, `\u%04x`, r)
			}
		}
	}
	buf.WriteByte('"')
}

// phpSerializePrecisionDigits is the precision C's %G semantics use to
// decide between plain and exponent notation. PHP's default
// serialize_precision is -1, which selects the SHORTEST round-tripping
// digit string — but the plain-vs-exponent decision is still made as if
// the precision were 17.
//
// Measured against PHP 8.2.33 rather than derived from the source:
//
//	1e16   => 10000000000000000     (plain)
//	1e17   => 1.0e+17               (exponent)
//	1e-4   => 0.0001                (plain)
//	1e-5   => 1.0e-5                (exponent)
const phpSerializePrecisionDigits = 17

// formatPHPDouble renders f the way PHP's json_encode does.
//
// Getting this wrong is the single most likely cause of a byte mismatch,
// because the obvious Go spelling is wrong in a way that looks right:
// strconv.FormatFloat(f, 'g', -1, 64) turns 9007199254741000 into
// "9.007199254741e+15", and FormatFloat(f, 'f', -1, 64) turns 1e21 into
// twenty-two characters of digits. PHP does neither.
func formatPHPDouble(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		// PHP's json_encode fails on these rather than inventing a
		// representation, and so does this — a silently-substituted 0
		// would be a wrong number presented as a real one.
		return "", fmt.Errorf("v2shim: %v has no JSON representation", f)
	}
	if f == 0 {
		// Covers -0.0 too: PHP prints "0", not "-0".
		return "0", nil
	}

	// Shortest round-tripping digits, in a form that hands over the
	// mantissa and decimal exponent separately.
	shortest := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exponent, err := splitExponentForm(shortest)
	if err != nil {
		return "", err
	}

	if exponent < -4 || exponent >= phpSerializePrecisionDigits {
		return phpExponentForm(mantissa, exponent), nil
	}
	return strconv.FormatFloat(f, 'f', -1, 64), nil
}

// splitExponentForm takes strconv's "-1.2345e+06" and returns the digit
// string with its sign ("-1.2345") and the decimal exponent (6).
func splitExponentForm(s string) (mantissa string, exponent int, err error) {
	index := strings.IndexByte(s, 'e')
	if index < 0 {
		return "", 0, fmt.Errorf("v2shim: %q is not in exponent form", s)
	}
	exponent, err = strconv.Atoi(s[index+1:])
	if err != nil {
		return "", 0, fmt.Errorf("v2shim: parsing exponent of %q: %w", s, err)
	}
	return s[:index], exponent, nil
}

// phpExponentForm renders PHP's exponent notation: a mantissa that ALWAYS
// carries a fractional part ("1.0e+17", never "1e+17"), a lower-case 'e',
// an explicit sign, and no zero padding on the exponent ("1.0e-5", not
// "1.0e-05").
func phpExponentForm(mantissa string, exponent int) string {
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	sign := "+"
	if exponent < 0 {
		sign, exponent = "-", -exponent
	}
	return mantissa + "e" + sign + strconv.Itoa(exponent)
}
