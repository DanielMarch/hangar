package config

import (
	"fmt"
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

	DB     DatabaseConfig
	Redis  RedisConfig
	Crypto CryptoConfig
	SSO    SSOConfig
	ESI    ESIConfig
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
	}

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
