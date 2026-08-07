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
