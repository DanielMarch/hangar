package telemetry_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/stretchr/testify/require"
)

const secretValue = "sk-super-secret-value-do-not-leak"

// TestRedactHandlerRecursive is a named Phase 0 exit criterion: secrets at
// nesting depth >= 3, inside maps and slices, do not appear in output.
func TestRedactHandlerRecursive(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.NewJSONLogger(&buf, slog.LevelDebug)

	type innermost struct {
		Key config.Secret // depth 3, inside a struct
	}
	type middle struct {
		Nested innermost
		Extras []any // depth 3+, inside a slice
	}
	type outer struct {
		Deep map[string]any // depth 2 map holding depth-3 struct/slice
	}

	payload := outer{
		Deep: map[string]any{
			"level2": middle{
				Nested: innermost{Key: config.NewSecret(secretValue)},
				Extras: []any{
					map[string]any{"password": secretValue},
					[]config.Secret{config.NewSecret(secretValue)},
				},
			},
		},
	}

	logger.Info("boot",
		slog.Any("payload", payload),
		slog.Group("db",
			slog.String("token", secretValue),
			slog.Group("nested",
				slog.Any("creds", map[string]any{"api_key": secretValue}),
			),
		),
		slog.Any("direct_secret", config.NewSecret(secretValue)),
	)

	out := buf.String()
	require.NotContains(t, out, secretValue, "raw secret leaked into log output: %s", out)
	require.Contains(t, out, "[REDACTED]")
}

func TestRedactHandlerPassesNonSensitiveDataThrough(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.NewJSONLogger(&buf, slog.LevelDebug)

	logger.Info("boot", slog.String("character_id", "12345"), slog.Int("count", 7))

	out := buf.String()
	require.Contains(t, out, "12345")
	require.Contains(t, out, "\"count\":7")
}

func TestRedactHandlerWithAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	base := telemetry.NewJSONLogger(&buf, slog.LevelDebug)
	logger := base.With("session_token", secretValue).WithGroup("request")

	logger.Info("handled", slog.String("password", secretValue), slog.String("route", "/ok"))

	out := buf.String()
	require.NotContains(t, out, secretValue)
	require.Contains(t, out, "/ok")
}

// TestRedactHandlerPreservesErrorMessages is Phase 14.1's regression guard
// for a product-wide observability defect: every `logger.Error(..., "error",
// err)` call in HANGAR rendered as `error=""`.
//
// The cause was the reflection walk rebuilding an error struct field by
// field. Go's errors are structs with ONLY unexported fields
// (*fmt.wrapError, *errors.errorString), which reflect cannot set, so the
// rebuilt value carried an empty message while looking like a valid error.
// It was found in Phase 14 when a deliberately unreachable webhook logged a
// WARN line with an empty reason while writing the same text correctly to
// app.alert_delivery.error.
//
// Both handlers are asserted, because the failure mode differed between
// them: the text handler printed "" and a marshalling handler can render a
// value with no exported fields as {}.
func TestRedactHandlerPreservesErrorMessages(t *testing.T) {
	wrapped := fmt.Errorf("posting to webhook: %w", errors.New("dial tcp 127.0.0.1:9: connect: connection refused"))

	for name, newLogger := range map[string]func(*bytes.Buffer) *slog.Logger{
		"json": func(b *bytes.Buffer) *slog.Logger { return telemetry.NewJSONLogger(b, slog.LevelDebug) },
		"text": func(b *bytes.Buffer) *slog.Logger { return telemetry.NewTextLogger(b, slog.LevelDebug) },
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			newLogger(&buf).Error("delivery failed", "error", wrapped)

			out := buf.String()
			require.Contains(t, out, "posting to webhook", "the error's own message must survive redaction")
			require.Contains(t, out, "connection refused", "and so must the wrapped cause")
			require.NotContains(t, out, `error=""`, "the empty-error regression must not return")
			require.NotContains(t, out, `"error":{}`, "nor its marshalled equivalent")
		})
	}

	// A plain errors.New value, and a custom error type whose fields are
	// exported, must both come through too.
	var buf bytes.Buffer
	telemetry.NewJSONLogger(&buf, slog.LevelDebug).Error("boom", "error", errors.New("simple failure"))
	require.Contains(t, buf.String(), "simple failure")

	// An error carrying a secret in a FIELD (not in its message) must still
	// be redacted — the message is all that survives, so the field cannot
	// leak by construction.
	buf.Reset()
	telemetry.NewJSONLogger(&buf, slog.LevelDebug).Error("boom", "error", &secretBearingError{Token: config.NewSecret(secretValue)})
	require.NotContains(t, buf.String(), secretValue, "a secret held in an error's field must never be rendered")
	require.Contains(t, buf.String(), "authentication failed")
}

type secretBearingError struct {
	Token config.Secret
}

func (e *secretBearingError) Error() string { return "authentication failed" }
