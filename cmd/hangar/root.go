package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/telemetry"
	"github.com/spf13/cobra"
)

// globalViper is the single Viper instance for the process. Precedence:
// flag > env > config file > default (internal/config.New sets up the last
// three; persistentFlags below binds the first).
var globalViper = config.New()

var cfgFile string

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "hangar",
		Short:         "Project HANGAR — EVE Online corporation/alliance management",
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate),
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./hangar.yaml)")
	root.PersistentFlags().String("log-level", "", "override HANGAR_LOG_LEVEL (debug|info|warn|error)")
	root.PersistentFlags().String("log-format", "", "override HANGAR_LOG_FORMAT (json|text)")
	_ = globalViper.BindPFlag("log_level", root.PersistentFlags().Lookup("log-level"))
	_ = globalViper.BindPFlag("log_format", root.PersistentFlags().Lookup("log-format"))

	cobra.OnInitialize(func() {
		if cfgFile != "" {
			globalViper.SetConfigFile(cfgFile)
		}
	})

	root.AddCommand(
		newServeCmd(),
		newWorkCmd(),
		newScheduleCmd(),
		newMigrateCmd(),
		newAdminCmd(),
		newOpenAPICmd(),
		newHealthcheckCmd(),
	)
	return root
}

// loadConfig resolves and validates configuration from globalViper. Every
// subcommand that needs configuration calls this rather than reading env
// vars directly, so precedence and validation are applied uniformly.
func loadConfig() (*config.Config, error) {
	return config.Load(globalViper)
}

// newLogger builds the process logger per HANGAR_LOG_FORMAT/HANGAR_LOG_LEVEL.
// JSON in production (the default); text is accepted for local development
// only. Both paths go through the redacting handler.
func newLogger(cfg *config.Config) *slog.Logger {
	level := parseLevel(cfg.LogLevel)
	var logger *slog.Logger
	if cfg.LogFormat == "text" {
		logger = telemetry.NewTextLogger(os.Stdout, level)
	} else {
		logger = telemetry.NewJSONLogger(os.Stdout, level)
	}
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
