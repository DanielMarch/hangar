package telemetry_test

import (
	"bytes"
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
