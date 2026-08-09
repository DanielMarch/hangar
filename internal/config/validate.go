package config

import (
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrMissingSecret is wrapped with the offending field's env var name so
// operators get an actionable boot-time error.
var ErrMissingSecret = errors.New("required value is not set")

// ErrInvalidSecret indicates a secret was set but fails a hard format
// requirement (e.g. HANGAR_MASTER_KEY must decode as exactly 32 raw bytes).
var ErrInvalidSecret = errors.New("value is set but invalid")

// Validate enforces Phase 0's fail-fast contract (Roadmap Phase 0 exit
// criterion: TestConfigFailsFastOnMissingSecrets). A missing required secret
// aborts boot with a named, wrapped error — HANGAR must NEVER generate an
// ephemeral key as a fallback. install.sh/install.bat generate real,
// persisted key material into .env before first boot; that is the only
// place key generation happens.
func Validate(cfg *Config) error {
	var errs []error

	requireSecret := func(name string, s Secret) {
		if s.Empty() {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrMissingSecret))
		}
	}
	requireString := func(name, v string) {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrMissingSecret))
		}
	}
	require32ByteKey := func(name string, s Secret) {
		if s.Empty() {
			return // already reported by requireSecret
		}
		raw, err := base64.StdEncoding.DecodeString(s.Reveal())
		if err != nil || len(raw) != 32 {
			errs = append(errs, fmt.Errorf("%s: %w: must be base64-encoded 32 bytes", name, ErrInvalidSecret))
		}
	}

	// Required with no default (SRS Principle 8, .env.example).
	requireSecret("HANGAR_DB_URL", cfg.DB.URL)
	requireSecret("HANGAR_MASTER_KEY", cfg.Crypto.MasterKey)
	requireSecret("HANGAR_SESSION_SECRET", cfg.Crypto.SessionSecret)
	requireString("HANGAR_SSO_CLIENT_ID", cfg.SSO.ClientID)
	requireSecret("HANGAR_SSO_CLIENT_SECRET", cfg.SSO.ClientSecret)

	require32ByteKey("HANGAR_MASTER_KEY", cfg.Crypto.MasterKey)
	require32ByteKey("HANGAR_SESSION_SECRET", cfg.Crypto.SessionSecret)
	if !cfg.Crypto.MasterKeyPrevious.Empty() {
		require32ByteKey("HANGAR_MASTER_KEY_PREVIOUS", cfg.Crypto.MasterKeyPrevious)
	}

	switch cfg.LogFormat {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("HANGAR_LOG_FORMAT: invalid value %q (want json|text)", cfg.LogFormat))
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("HANGAR_LOG_LEVEL: invalid value %q (want debug|info|warn|error)", cfg.LogLevel))
	}
	switch cfg.Sync.Jitter {
	case "full", "none":
	default:
		errs = append(errs, fmt.Errorf("HANGAR_SYNC_JITTER: invalid value %q (want full|none)", cfg.Sync.Jitter))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config: invalid configuration: %w", errors.Join(errs...))
	}
	return nil
}
