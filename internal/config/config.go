package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the fully resolved HANGAR configuration. Precedence follows
// Viper's native order: flag > env > config file > default. Every
// credential-bearing field uses Secret (see secret.go) so it cannot leak
// through fmt, encoding/json, or log/slog by accident.
type Config struct {
	Env            string
	LogLevel       string
	LogFormat      string
	HTTPAddr       string
	PublicURL      string
	TrustedProxies []string

	DB        DatabaseConfig
	Redis     RedisConfig
	Crypto    CryptoConfig
	SSO       SSOConfig
	ESI       ESIConfig
	Sync      SyncConfig
	Discord   DiscordConfig
	TeamSpeak TeamSpeakConfig
	Mumble    MumbleConfig
}

// ESIConfig governs Phase 3/4's outbound gateway: the conditional-cache
// floor, the two rate-limit governors, and how long an in-flight request is
// allowed to hold a predictive reservation open (01_ARCHITECTURE.md §5.5,
// §5.7).
type ESIConfig struct {
	// RequestTimeout bounds a single outbound ESI call. It is also the
	// deadline stamped on a Governor 1 predictive reservation at issue
	// time (§5.5): a request that never returns has its reservation
	// expire — and be charged the worst case — at this deadline, never
	// held open indefinitely.
	RequestTimeout time.Duration
	// TTLFloor is the minimum poll interval enforced regardless of what
	// the spec declares (§6.2), and the snooze duration for a headerless
	// 429 that carries no Retry-After (§5.5's edge case).
	TTLFloor time.Duration
	// ErrorLimitWindow is Governor 2's fixed window (§5.7): 100
	// non-2XX/3XX responses per this duration, installation-wide.
	ErrorLimitWindow time.Duration
	// ErrorLimitMax is the window's error budget (100 per the spec).
	ErrorLimitMax int
	// ErrorLimitPauseAt / ErrorLimitResumeAt are the hysteresis pair:
	// pause proactively when remaining budget falls to this value,
	// resume only once it climbs back to the (higher) resume threshold —
	// otherwise the installation oscillates in and out of pause.
	ErrorLimitPauseAt  int
	ErrorLimitResumeAt int
}

// SyncConfig governs Phase 6's planner: leader-elected claim loop cadence,
// claim batch size, the lease that protects a just-claimed subscription from
// being reclaimed by the very next tick, and the adaptive-backoff cap
// (01_ARCHITECTURE.md §6, .env.example "SYNC ENGINE").
type SyncConfig struct {
	// PlannerInterval is how often the leader claims due work (§6.1: 5s).
	PlannerInterval time.Duration
	// ClaimBatchSize bounds a single claim transaction's row count.
	ClaimBatchSize int32
	// ClaimLease is how long a just-claimed subscription's next_due_at is
	// pushed out, so the claim transaction itself — not just River's
	// unique-job option — is the first line of defence against
	// re-claiming a row before its attempt finishes (§6.1).
	ClaimLease time.Duration
	// BackoffCap bounds the 1.5^n consecutive-304 backoff (§6.2) — maps
	// onto internal/sync.PolicyConfig.BackoffCap, which (like Jitter
	// above) a Phase 7+ worker consumes after an attempt completes; the
	// claim loop itself never applies backoff.
	BackoffCap time.Duration
	// Jitter selects "full" (production) or "none" (deterministic tests
	// only — HANGAR_SYNC_JITTER=none in .env.example). Phase 6 loads and
	// validates this, but nothing consumes it yet: the claim loop itself
	// never computes a next_due_at (internal/sync.PlanNextDueAt, which
	// this maps onto), only a Phase 7+ worker does, after an attempt
	// completes. Wiring "none" through to PlanNextDueAt's Rand parameter
	// (e.g. a generator that always returns 0) is that phase's job.
	Jitter string
}

// DiscordConfig governs Phase 12's provisioning driver
// (internal/provisioning/drivers/discord) — strictly optional, like
// RedisConfig: a zero value (Enabled=false) is valid and means "no
// Discord provisioning", never an error. Field names mirror
// discord.Config 1:1; cmd/hangar's wiring converts one to the other
// rather than internal/config importing internal/provisioning/drivers/
// discord directly (this package stays dependency-free of any specific
// driver, the same reason ESIConfig doesn't import internal/esi).
type DiscordConfig struct {
	Enabled      bool
	BotToken     Secret
	ClientID     string
	ClientSecret Secret
	GuildID      string
	// APIVersion must appear in Allowlist — enforced at Validate time
	// (§9.3: "a version outside the allowlist fails at config validation,
	// not at first request"), not deferred to the driver's own
	// construction.
	APIVersion int
	Allowlist  []int
	// GlobalRate is Discord's flat requests/second ceiling (50 per the
	// spec, configurable for testing against a mock).
	GlobalRate int
	// InvalidBudgetMax/WarnPercent/PausePercent are the Cloudflare
	// invalid-request budget's fixed-window size and hysteresis
	// thresholds (§9.3: 10,000 per 10 minutes, warn at 50%, pause at 80%).
	InvalidBudgetMax    int
	InvalidWarnPercent  int
	InvalidPausePercent int
}

// TeamSpeakConfig governs Phase 13's TS3 driver
// (internal/provisioning/drivers/teamspeak) — strictly optional
// (DiscordConfig's pattern).
type TeamSpeakConfig struct {
	Enabled           bool
	WebQueryURL       string
	APIKey            Secret
	VirtualServerID   int
	ChallengeTokenTTL time.Duration
}

// MumbleConfig governs Phase 13's Mumble driver
// (internal/provisioning/drivers/mumble) — strictly optional.
type MumbleConfig struct {
	Enabled  bool
	GRPCAddr string
	ServerID uint32
	// TLSCAPath empty means an insecure (plaintext) channel — .env.example:
	// "empty = insecure channel (LAN only)".
	TLSCAPath string

	ExternalAuthenticator bool
	// FailClosed's default (false) is deliberate — see
	// internal/provisioning/drivers/mumble.Config.FailClosed's doc
	// comment for why defaulting this to true would be actively harmful.
	FailClosed       bool
	AuthSharedSecret Secret
}

// DatabaseConfig is PostgreSQL 18 connection configuration (SRS §3.1).
type DatabaseConfig struct {
	URL              Secret
	MaxConns         int
	MinConns         int
	MaxConnLifetime  time.Duration
	StatementTimeout time.Duration
	AppSchema        string
	RiverSchema      string
	SDESchema        string
}

// RedisConfig is strictly optional (Principle 7). A zero value is valid and
// means "no cache" — never an error.
type RedisConfig struct {
	URL    Secret
	Prefix string
}

// CryptoConfig holds envelope-encryption and session-signing key material
// (Principle 8, SRS §4.5).
type CryptoConfig struct {
	MasterKey           Secret
	MasterKeyVersion    int
	MasterKeyPrevious   Secret
	SessionSecret       Secret
	SessionTTL          time.Duration
	SessionCookieSecure bool
}

// SSOConfig is EVE SSO OAuth2 Authorization Code + PKCE S256 configuration
// (SRS §3.1, §4.8).
type SSOConfig struct {
	ClientID     string
	ClientSecret Secret
	CallbackURL  string
	AuthorizeURL string
	TokenURL     string
	JWKSURL      string
	Issuers      []string
	Audience     string
}

// New returns a *viper.Viper wired for HANGAR's env var contract
// (HANGAR_-prefixed, underscore-separated) and default config file search
// path. cmd/hangar binds Cobra flags into it via BindPFlag so flags win.
func New() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("HANGAR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetConfigName("hangar")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/hangar")
	applyDefaults(v)
	return v
}

// applyDefaults mirrors .env.example's non-secret defaults exactly. Secrets
// (HANGAR_MASTER_KEY, HANGAR_SESSION_SECRET, HANGAR_SSO_CLIENT_SECRET, ...)
// deliberately have NO default — see validate.go.
func applyDefaults(v *viper.Viper) {
	v.SetDefault("env", "production")
	v.SetDefault("log_level", "info")
	v.SetDefault("log_format", "json")
	v.SetDefault("http_addr", "0.0.0.0:8080")
	v.SetDefault("public_url", "http://localhost:8080")
	v.SetDefault("trusted_proxies", "")

	v.SetDefault("db_max_conns", 25)
	v.SetDefault("db_min_conns", 5)
	v.SetDefault("db_max_conn_lifetime", "1h")
	v.SetDefault("db_statement_timeout", "30s")
	v.SetDefault("db_app_schema", "app")
	v.SetDefault("db_river_schema", "river")
	v.SetDefault("db_sde_schema", "sde")

	v.SetDefault("redis_prefix", "hangar")

	v.SetDefault("master_key_version", 1)
	v.SetDefault("session_ttl", "720h")
	v.SetDefault("session_cookie_secure", true)

	v.SetDefault("sso_callback_url", "http://localhost:8080/auth/callback")
	v.SetDefault("sso_authorize_url", "https://login.eveonline.com/v2/oauth/authorize")
	v.SetDefault("sso_token_url", "https://login.eveonline.com/v2/oauth/token")
	v.SetDefault("sso_jwks_url", "https://login.eveonline.com/oauth/jwks")
	v.SetDefault("sso_issuers", "login.eveonline.com,https://login.eveonline.com")
	v.SetDefault("sso_audience", "EVE Online")

	v.SetDefault("esi_request_timeout", "30s")
	v.SetDefault("esi_ttl_floor", "300s")
	v.SetDefault("esi_error_limit_window", "60s")
	v.SetDefault("esi_error_limit_max", 100)
	v.SetDefault("esi_error_limit_pause_at", 20)
	v.SetDefault("esi_error_limit_resume_at", 60)

	v.SetDefault("sync_planner_interval", "5s")
	v.SetDefault("sync_claim_batch_size", 500)
	v.SetDefault("sync_claim_lease", "2m")
	v.SetDefault("sync_backoff_cap", "24h")
	v.SetDefault("sync_jitter", "full")

	v.SetDefault("discord_enabled", false)
	v.SetDefault("discord_api_version", 10)
	v.SetDefault("discord_api_version_allowlist", "10")
	v.SetDefault("discord_global_rate", 50)
	v.SetDefault("discord_invalid_budget", 10000)
	v.SetDefault("discord_invalid_warn_pct", 50)
	v.SetDefault("discord_invalid_pause_pct", 80)

	v.SetDefault("teamspeak_enabled", false)
	v.SetDefault("teamspeak_virtual_server_id", 1)
	v.SetDefault("teamspeak_token_ttl", "24h")

	v.SetDefault("mumble_enabled", false)
	v.SetDefault("mumble_server_id", 1)
	v.SetDefault("mumble_external_authenticator", false)
	v.SetDefault("mumble_auth_fail_closed", false)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitCSVInts is splitCSV plus an int parse per element — HANGAR_DISCORD_
// API_VERSION_ALLOWLIST's shape ("10" today, "10,11" once Discord ships a
// new version HANGAR has verified against). A malformed entry is silently
// dropped rather than failing config load outright — Validate's allowlist
// membership check on the resolved APIVersion is what actually enforces
// correctness; a stray typo in the allowlist just shrinks it.
func splitCSVInts(s string) []int {
	parts := splitCSV(s)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// Load reads the merged configuration from v (env/file/flags already bound)
// into a Config and validates it. A missing required secret is a boot-time
// error — HANGAR never fabricates an ephemeral key as a fallback.
func Load(v *viper.Viper) (*Config, error) {
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("config: reading config file: %w", err)
		}
	}

	cfg := &Config{
		Env:            v.GetString("env"),
		LogLevel:       v.GetString("log_level"),
		LogFormat:      v.GetString("log_format"),
		HTTPAddr:       v.GetString("http_addr"),
		PublicURL:      v.GetString("public_url"),
		TrustedProxies: splitCSV(v.GetString("trusted_proxies")),

		DB: DatabaseConfig{
			URL:              NewSecret(v.GetString("db_url")),
			MaxConns:         v.GetInt("db_max_conns"),
			MinConns:         v.GetInt("db_min_conns"),
			MaxConnLifetime:  v.GetDuration("db_max_conn_lifetime"),
			StatementTimeout: v.GetDuration("db_statement_timeout"),
			AppSchema:        v.GetString("db_app_schema"),
			RiverSchema:      v.GetString("db_river_schema"),
			SDESchema:        v.GetString("db_sde_schema"),
		},
		Redis: RedisConfig{
			URL:    NewSecret(v.GetString("redis_url")),
			Prefix: v.GetString("redis_prefix"),
		},
		Crypto: CryptoConfig{
			MasterKey:           NewSecret(v.GetString("master_key")),
			MasterKeyVersion:    v.GetInt("master_key_version"),
			MasterKeyPrevious:   NewSecret(v.GetString("master_key_previous")),
			SessionSecret:       NewSecret(v.GetString("session_secret")),
			SessionTTL:          v.GetDuration("session_ttl"),
			SessionCookieSecure: v.GetBool("session_cookie_secure"),
		},
		SSO: SSOConfig{
			ClientID:     v.GetString("sso_client_id"),
			ClientSecret: NewSecret(v.GetString("sso_client_secret")),
			CallbackURL:  v.GetString("sso_callback_url"),
			AuthorizeURL: v.GetString("sso_authorize_url"),
			TokenURL:     v.GetString("sso_token_url"),
			JWKSURL:      v.GetString("sso_jwks_url"),
			Issuers:      splitCSV(v.GetString("sso_issuers")),
			Audience:     v.GetString("sso_audience"),
		},
		ESI: ESIConfig{
			RequestTimeout:     v.GetDuration("esi_request_timeout"),
			TTLFloor:           v.GetDuration("esi_ttl_floor"),
			ErrorLimitWindow:   v.GetDuration("esi_error_limit_window"),
			ErrorLimitMax:      v.GetInt("esi_error_limit_max"),
			ErrorLimitPauseAt:  v.GetInt("esi_error_limit_pause_at"),
			ErrorLimitResumeAt: v.GetInt("esi_error_limit_resume_at"),
		},
		Sync: SyncConfig{
			PlannerInterval: v.GetDuration("sync_planner_interval"),
			ClaimBatchSize:  int32(v.GetInt("sync_claim_batch_size")),
			ClaimLease:      v.GetDuration("sync_claim_lease"),
			BackoffCap:      v.GetDuration("sync_backoff_cap"),
			Jitter:          v.GetString("sync_jitter"),
		},
		Discord: DiscordConfig{
			Enabled:             v.GetBool("discord_enabled"),
			BotToken:            NewSecret(v.GetString("discord_bot_token")),
			ClientID:            v.GetString("discord_client_id"),
			ClientSecret:        NewSecret(v.GetString("discord_client_secret")),
			GuildID:             v.GetString("discord_guild_id"),
			APIVersion:          v.GetInt("discord_api_version"),
			Allowlist:           splitCSVInts(v.GetString("discord_api_version_allowlist")),
			GlobalRate:          v.GetInt("discord_global_rate"),
			InvalidBudgetMax:    v.GetInt("discord_invalid_budget"),
			InvalidWarnPercent:  v.GetInt("discord_invalid_warn_pct"),
			InvalidPausePercent: v.GetInt("discord_invalid_pause_pct"),
		},
		TeamSpeak: TeamSpeakConfig{
			Enabled:           v.GetBool("teamspeak_enabled"),
			WebQueryURL:       v.GetString("teamspeak_webquery_url"),
			APIKey:            NewSecret(v.GetString("teamspeak_api_key")),
			VirtualServerID:   v.GetInt("teamspeak_virtual_server_id"),
			ChallengeTokenTTL: v.GetDuration("teamspeak_token_ttl"),
		},
		Mumble: MumbleConfig{
			Enabled:               v.GetBool("mumble_enabled"),
			GRPCAddr:              v.GetString("mumble_grpc_addr"),
			ServerID:              uint32(v.GetUint("mumble_server_id")),
			TLSCAPath:             v.GetString("mumble_tls_ca"),
			ExternalAuthenticator: v.GetBool("mumble_external_authenticator"),
			FailClosed:            v.GetBool("mumble_auth_fail_closed"),
			AuthSharedSecret:      NewSecret(v.GetString("mumble_auth_shared_secret")),
		},
	}

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
