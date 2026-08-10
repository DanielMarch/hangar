// Package telemetry provides the redacting slog handler, OpenTelemetry
// wiring, Prometheus registry, and the app.esi_replica heartbeat loop
// (SRS v3.0 §3.2; added in v3.1 because these had no owner otherwise).
package telemetry

import (
	"reflect"
	"regexp"

	"github.com/hangar-project/hangar/internal/config"
)

const redacted = "[REDACTED]"

// sensitiveKey matches struct field names and map keys that must be redacted
// even when the value is not wrapped in config.Secret — a defense-in-depth
// backstop so a raw string logged under a suggestive key never leaks.
var sensitiveKey = regexp.MustCompile(`(?i)(secret|password|passwd|token|api[_-]?key|master[_-]?key|client[_-]?secret|authorization|credential|refresh[_-]?token|access[_-]?token|private[_-]?key)`)

var secretType = reflect.TypeOf(config.Secret(""))

// redactAny walks v recursively — through maps, slices, arrays, pointers and
// structs — and returns a copy with every config.Secret value and every
// name-matched sensitive string field replaced by "[REDACTED]". It is the
// primitive the handler in slog.go calls for every attribute value that
// slog itself hands over as a plain `any` (map[string]any, a nested struct,
// a slice) rather than as a nested slog.Group.
func redactAny(v any) any {
	if v == nil {
		return nil
	}

	// ── PHASE 14.1 FIX: errors must survive redaction ───────────────────
	// An error is rendered by its Error() method, not by its fields, and
	// almost every error in Go is a struct with ONLY unexported fields
	// (fmt.Errorf returns *fmt.wrapError{msg, err}; errors.New returns
	// *errors.errorString{s}). The reflection walk below rebuilds structs
	// field by field and necessarily skips fields it cannot set — see the
	// `!dst.CanSet()` branch in redactReflect — so an error came out the
	// other side with an empty message and rendered as `error=""`.
	//
	// That silently blanked the error text of EVERY `logger.Error(...,
	// "error", err)` call in the product. It was found in Phase 14 when a
	// deliberately unreachable webhook produced a WARN line whose reason
	// was empty while the same text was written correctly to the database.
	//
	// Returning the MESSAGE STRING (rather than a rebuilt error value) is
	// deliberate: slog's TextHandler and JSONHandler both render a string
	// identically and unambiguously, whereas an error value's treatment
	// differs between them and a marshalling handler can turn a value with
	// no exported fields into `{}` — the same silent blanking in a new
	// costume.
	//
	// Redaction is not weakened. The walk never scanned free text for
	// secrets — it matches config.Secret values and sensitive FIELD NAMES,
	// neither of which survives inside an already-formatted message. Any
	// secret in an error's text must be kept out at construction time,
	// which is what internal/alerting/channels.scrubURL does for webhook
	// URLs. Blanking the whole message was never a secret-safety measure;
	// it was an accident of reflection.
	if err, ok := v.(error); ok {
		return err.Error()
	}

	out := redactReflect(reflect.ValueOf(v))
	if !out.IsValid() {
		return v
	}
	return out.Interface()
}

// redactString reports whether the sentinel can be produced for a value of
// kind k without a panicking Convert (only string-shaped kinds qualify).
func redactableKind(k reflect.Kind) bool {
	return k == reflect.String || k == reflect.Interface
}

func redactReflect(rv reflect.Value) reflect.Value {
	if !rv.IsValid() {
		return rv
	}

	if rv.Type() == secretType {
		return reflect.ValueOf(redacted)
	}

	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		return redactReflect(rv.Elem())

	case reflect.Pointer:
		if rv.IsNil() {
			return rv
		}
		inner := redactReflect(rv.Elem())
		out := reflect.New(inner.Type())
		out.Elem().Set(inner)
		return out

	case reflect.Map:
		if rv.IsNil() {
			return rv
		}
		elemType := rv.Type().Elem()
		outType := rv.Type()
		out := reflect.MakeMapWithSize(outType, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key()
			val := iter.Value()
			forceRedact := k.Kind() == reflect.String && sensitiveKey.MatchString(k.String()) && redactableKind(elemType.Kind())
			var newVal reflect.Value
			if forceRedact {
				newVal = coerce(reflect.ValueOf(redacted), elemType)
			} else {
				newVal = coerce(redactReflect(val), elemType)
			}
			out.SetMapIndex(k, newVal)
		}
		return out

	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(coerce(redactReflect(rv.Index(i)), rv.Type().Elem()))
		}
		return out

	case reflect.Array:
		out := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(coerce(redactReflect(rv.Index(i)), rv.Type().Elem()))
		}
		return out

	case reflect.Struct:
		out := reflect.New(rv.Type()).Elem()
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			field := t.Field(i)
			fv := rv.Field(i)
			dst := out.Field(i)
			if !dst.CanSet() {
				continue // unexported field: leave zero value, never echo it
			}
			if sensitiveKey.MatchString(field.Name) && redactableKind(field.Type.Kind()) {
				dst.Set(coerce(reflect.ValueOf(redacted), field.Type))
				continue
			}
			dst.Set(coerce(redactReflect(fv), field.Type))
		}
		return out

	default:
		return rv
	}
}

// coerce assigns v into a value of type t, falling back to the original
// (unconverted) value when a direct assignment isn't possible — this only
// matters at the boundary where redactReflect flattened an interface{} whose
// concrete type still satisfies an interface{}-typed destination.
func coerce(v reflect.Value, t reflect.Type) reflect.Value {
	if !v.IsValid() {
		return reflect.Zero(t)
	}
	if v.Type().AssignableTo(t) {
		return v
	}
	if t.Kind() == reflect.Interface && v.Type().Implements(t) {
		return v
	}
	if t.Kind() == reflect.Interface {
		out := reflect.New(t).Elem()
		out.Set(v)
		return out
	}
	if v.Type().ConvertibleTo(t) {
		return v.Convert(t)
	}
	return reflect.Zero(t)
}
