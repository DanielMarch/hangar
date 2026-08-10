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

	// Discord is strictly optional (RedisConfig's pattern) — these checks
	// only run when an administrator has actually turned it on
	// (HANGAR_DISCORD_ENABLED=true). Phase 12 / 01_ARCHITECTURE.md §9.3:
	// "a version outside the allowlist fails at config validation, not at
	// first request" — enforced HERE, at the same boot-time fail-fast
	// point as every other required-when-enabled setting, not deferred to
	// discord.NewClient (which re-checks the same thing defensively for
	// callers that construct a driver directly, e.g. this package's own
	// tests, without going through internal/config at all).
	if cfg.Discord.Enabled {
		requireSecret("HANGAR_DISCORD_BOT_TOKEN", cfg.Discord.BotToken)
		requireString("HANGAR_DISCORD_GUILD_ID", cfg.Discord.GuildID)
		if !containsInt(cfg.Discord.Allowlist, cfg.Discord.APIVersion) {
			errs = append(errs, fmt.Errorf("HANGAR_DISCORD_API_VERSION: version %d not in allowlist %v", cfg.Discord.APIVersion, cfg.Discord.Allowlist))
		}
	}

	// TeamSpeak and Mumble are strictly optional, same pattern.
	if cfg.TeamSpeak.Enabled {
		requireString("HANGAR_TEAMSPEAK_WEBQUERY_URL", cfg.TeamSpeak.WebQueryURL)
		requireSecret("HANGAR_TEAMSPEAK_API_KEY", cfg.TeamSpeak.APIKey)
	}
	if cfg.Mumble.Enabled {
		requireString("HANGAR_MUMBLE_GRPC_ADDR", cfg.Mumble.GRPCAddr)
		if cfg.Mumble.ExternalAuthenticator {
			requireSecret("HANGAR_MUMBLE_AUTH_SHARED_SECRET", cfg.Mumble.AuthSharedSecret)
		}
	}

	// Alerting (Phase 14). Strictly optional in the same sense: no
	// channels configured is a valid, working installation that simply
	// delivers nothing yet. Only SMTP has required companions, because a
	// half-configured mail channel fails at delivery time — on the queue,
	// hours later — rather than at boot, which is the failure mode
	// fail-fast validation exists to prevent.
	if cfg.Alerting.SMTPEnabled {
		requireString("HANGAR_SMTP_HOST", cfg.Alerting.SMTPHost)
		requireString("HANGAR_SMTP_FROM", cfg.Alerting.SMTPFrom)
		if len(cfg.Alerting.SMTPTo) == 0 {
			errs = append(errs, fmt.Errorf("HANGAR_SMTP_TO is required when HANGAR_SMTP_ENABLED=true (a mail channel with no recipients can never deliver)"))
		}
	}
	switch cfg.Alerting.SMTPTLS {
	case "starttls", "tls", "none":
	default:
		errs = append(errs, fmt.Errorf("HANGAR_SMTP_TLS must be one of starttls|tls|none, got %q", cfg.Alerting.SMTPTLS))
	}
	if cfg.Alerting.MaxAttempts < 1 {
		errs = append(errs, fmt.Errorf("HANGAR_ALERT_MAX_ATTEMPTS must be at least 1, got %d", cfg.Alerting.MaxAttempts))
	}
	if cfg.Alerting.ClaimSize < 1 {
		errs = append(errs, fmt.Errorf("HANGAR_ALERT_CLAIM_SIZE must be at least 1, got %d", cfg.Alerting.ClaimSize))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config: invalid configuration: %w", errors.Join(errs...))
	}
	return nil
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
