// Package config holds Viper-backed configuration, the Secret redaction
// wrapper, and boot-time validation (SRS v3.1 Principle 8, Phase 0).
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

const redacted = "[REDACTED]"

// Secret wraps a credential so it can never accidentally reach a log line, a
// span attribute, an error message, or an API response. Every serialisation
// path is overridden to emit "[REDACTED]" unconditionally:
//
//   - String()      — fmt.Stringer, e.g. implicit string conversion
//   - GoString()     — fmt "%#v"
//   - Format()       — fmt.Formatter, every verb including %s %v %q %+v
//   - MarshalJSON()  — encoding/json, including via a struct field
//   - LogValue()     — log/slog, so slog.Any(name, secret) redacts even
//     without the recursive handler in internal/telemetry
//
// Reveal() is the one deliberate escape hatch; every call site is a
// credential boundary (a database driver, an HTTP client, a KDF) and should
// be reviewable as such by grepping for it.
type Secret string

// NewSecret wraps a plain string value.
func NewSecret(v string) Secret { return Secret(v) }

// Reveal returns the underlying value.
func (s Secret) Reveal() string { return string(s) }

// Empty reports whether the wrapped value is the empty string.
func (s Secret) Empty() bool { return string(s) == "" }

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer.
func (s Secret) GoString() string { return redacted }

// Format implements fmt.Formatter so every verb redacts, not just %s/%v.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(redacted))
}

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(redacted)
}

// UnmarshalJSON implements json.Unmarshaler so a Secret round-trips through
// JSON (e.g. a config file) without ever accepting the literal "[REDACTED]"
// as a real value.
func (s *Secret) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v == redacted {
		return fmt.Errorf("config: refusing to unmarshal the redaction sentinel %q as a secret value", redacted)
	}
	*s = Secret(v)
	return nil
}

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(redacted)
}
