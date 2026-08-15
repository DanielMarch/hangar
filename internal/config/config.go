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
	Env       string
	LogLevel  string
	LogFormat string
	HTTPAddr  string
	// MetricsAddr is the Prometheus scrape listener, served by every
	// process role (serve, work, schedule) on its OWN port rather than on
	// HTTPAddr.
	//
	// Separate on purpose. HTTPAddr is the published port in
	// docker-compose.yml, so mounting an unauthenticated /metrics on it
	// would expose the installation's operational counters to whatever the
	// operator has pointed at 8080 — and `work` has no HTTP listener at
	// all, so it could not export the Gate 1 ledger-mode metric any other
	// way. Empty disables the listener entirely.
	MetricsAddr    string
	PublicURL      string
	TrustedProxies []string

	// Locale is the installation's UI locale (HANGAR_LOCALE), one of
	// internal/i18n's nine. It resolves — through internal/i18n's table,
	// never a second copy of it — to the Accept-Language HANGAR's ESI
	// gateway sends and keys its cache on (§5.3, §13).
	//
	// ── PHASE 20.2, DEFECT B23: WHY INSTALLATION-WIDE AND NOT PER USER ───
	// internal/i18n was absent from the binary entirely, so nothing ever
	// resolved a language and every ESI request went out with none. The
	// obvious repair — take it from the acting user — is wrong twice:
	// background sync is the overwhelming majority of HANGAR's ESI traffic
	// and has no acting user at all, and the resolved language is part of
	// the ESI cache key, so a per-user value fragments one shared cache
	// into up to nine, for payloads that are byte-identical apart from a
	// few localised name fields.
	//
	// It is deliberately NOT derived from the route catalogue the way
	// internal/scopes/login.go derives its scope set. A scope set is a
	// property of the spec and must track it; a locale is an operator's
	// choice about their own installation and no amount of reading the
	// spec reveals it.
	Locale string

	DB        DatabaseConfig
	Redis     RedisConfig
	Crypto    CryptoConfig
	SSO       SSOConfig
	ESI       ESIConfig
	Sync      SyncConfig
	Discord   DiscordConfig
	TeamSpeak TeamSpeakConfig
	Mumble    MumbleConfig
	Alerting  AlertingConfig
}

// AlertingConfig governs Phase 14's delivery pipeline (SRS §4.4). Most of
// its keys already existed in .env.example's "ALERT DELIVERY CHANNELS"
// block since Phase 0 and had no consumer until now; this struct is where
// they finally land.
//
// ── CHANNELS LIVE IN THE DATABASE, NOT HERE ─────────────────────────────
// app.alert_channel is the real channel registry — an installation can
// have any number of Slack and Discord webhooks and SMTP destinations,
// each targeted by its own routing rules. The three env-configured
// endpoints below are a convenience: when set, cmd/hangar provisions ONE
// corresponding "default-*" channel row at worker boot so a fresh
// installation can deliver without an operator writing SQL first. Leaving
// them empty is the default and is not an error — it means "no channels
// configured yet", exactly like RedisConfig's zero value (Principle 7).
type AlertingConfig struct {
	// CoalesceWindow is §4.4's coalescing window
	// (HANGAR_ALERT_COALESCE_WINDOW, 300s).
	CoalesceWindow time.Duration
	// MaxAttempts is how many send attempts a delivery gets before it
	// dead-letters (HANGAR_ALERT_MAX_ATTEMPTS, 8).
	MaxAttempts int
	// DeadLetterAfter is the age bound that dead-letters a delivery
	// regardless of how few attempts it has made
	// (HANGAR_ALERT_DEAD_LETTER_AFTER, 24h). Orthogonal to MaxAttempts:
	// with a long backoff a delivery could otherwise sit pending for days,
	// and an alert nobody has seen in a day is not going to become useful.
	DeadLetterAfter time.Duration
	// DispatchInterval is how often the outbox pump runs, and ClaimSize
	// bounds one pass. PHASE 14 ADDITIONS to .env.example: ClaimSize in
	// particular must exceed the largest expected coalescing group, since
	// a group is rolled up from one pass's claim.
	DispatchInterval time.Duration
	ClaimSize        int32
	// RetryBase is the first retry delay; each subsequent retry doubles it
	// up to RetryCap. PHASE 14 ADDITIONS.
	RetryBase time.Duration
	RetryCap  time.Duration

	// SMTP: HANGAR_SMTP_*.
	SMTPEnabled  bool
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword Secret
	SMTPFrom     string
	// SMTPTo is the default channel's recipient list. PHASE 14 ADDITION,
	// and a gap worth naming: .env.example declared a FROM address and no
	// recipients, because §4.4's per-user email routing has nothing to
	// resolve a user to an address with — app.user has no email column,
	// and EVE SSO never provides one. An installation-wide recipient list
	// is what can honestly be delivered; per-user email routing needs a
	// schema change and an address-verification flow that no phase owns.
	SMTPTo []string
	// SMTPTLS is "starttls" (default), "tls" (implicit/SMTPS), or "none".
	SMTPTLS string

	// SlackWebhook/DiscordWebhook are the default webhook URLs. Both are
	// credentials — anyone holding one can post to the channel.
	SlackWebhook   Secret
	DiscordWebhook Secret

	// ── PHASE 20.4 (B25): §4.4's THRESHOLD CATEGORY ─────────────────────
	// ThresholdInterval is how often the threshold evaluator re-asks each
	// of §4.4's four threshold questions of the synced data
	// (HANGAR_ALERT_THRESHOLD_INTERVAL, 10m). It is a FLOOR ON LATENESS,
	// not a firing rate: a threshold still crossed on the next pass
	// deduplicates against the event the last pass wrote, so shortening
	// this makes alerts arrive sooner and does not make them arrive more
	// often. Zero disables the evaluator entirely, which is a valid
	// configuration for an installation that wants only CCP's own
	// notifications.
	ThresholdInterval time.Duration
	// The four margins, one per catalogue threshold. See
	// internal/alerting.ThresholdPolicy for what each means and why its
	// default is what it is; every zero here selects that default rather
	// than disabling the threshold, because "0 hours of fuel remaining" is
	// not a sensible reading of an unset variable.
	StructureFuelWithin    time.Duration
	StarbaseFuelBelow      int64
	StarbaseFuelBand       int64
	MemberInactiveFor      time.Duration
	ContractExpiringWithin time.Duration
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

	// StartupCatalogueIngest controls whether `serve` runs the route
	// catalogue ingest in the background at startup. Default true, which is
	// §2's "single-process default": a one-box installation must not need a
	// second command before anything works (defect B20).
	//
	// ── WHY IT MUST BE POSSIBLE TO TURN OFF (defect B41) ─────────────────
	// catalogue.Boot is the ONLY writer of app.setting's `esi.d_max`, and
	// it rewrites it on every startup from whatever the live spec — or the
	// embedded snapshot on fallback — reports. That is right for a real
	// installation and wrong for any environment that has deliberately
	// seeded a catalogue and a D_max ceiling, because the ingest silently
	// overwrites the seed moments after the process starts.
	//
	// The Playwright suite is exactly that environment: web/e2e/global-setup
	// seeds `esi.d_max` and a five-route catalogue so the pin-advance
	// preview has a deterministic diff, and the startup ingest then replaces
	// the ceiling. Worse, it is a RACE — whether the seed survives depends on
	// how quickly ESI answers, so the suite passes on a machine that cannot
	// reach ESI and fails on one that can.
	//
	// This is also a real operator setting, not only a test hook: an
	// installation that manages its catalogue on a schedule through
	// `hangar admin ingest-catalogue`, or one running air-gapped, should not
	// have every replica restart reach for ESI.
	StartupCatalogueIngest bool
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

	// Scopes OVERRIDES the scope set the login flow requests. Empty (the
	// default) means "derive from the route catalogue", which is the
	// correct behaviour and the one that keeps tracking the spec.
	//
	// ── WHY AN ESCAPE HATCH EXISTS AT ALL ────────────────────────────────
	// The derived set is what HANGAR *needs*. What EVE SSO will *grant* is
	// whatever the operator enabled on the application in the developer
	// portal, and HANGAR has no way to read that list. When the two
	// disagree, SSO rejects the whole authorization with `invalid_scope`
	// naming ONE offending scope — after the user has already typed their
	// password and their 2FA code. Login is then impossible, and the
	// operator's only recourse without this setting is to edit Go source.
	//
	// Worse, the error names one scope at a time, so reconciling a
	// registration by trial and error costs a full login round trip per
	// missing scope.
	//
	// So: an operator running a deliberately narrower registration sets
	// this to exactly what they enabled, and HANGAR asks for that. The
	// routes whose scopes are absent will fail individually at sync time —
	// which is a visible, per-route failure on the sync board, and vastly
	// better than a login surface that cannot be used at all.
	Scopes []string
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
	// 9090 is Prometheus's own conventional port and is NOT published by
	// docker-compose.yml — a scraper on the compose network reaches it,
	// the internet does not. Set HANGAR_METRICS_ADDR="" to disable.
	v.SetDefault("metrics_addr", "0.0.0.0:9090")
	v.SetDefault("public_url", "http://localhost:8080")
	v.SetDefault("trusted_proxies", "")
	v.SetDefault("locale", "en")

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
	v.SetDefault("esi_startup_catalogue_ingest", true)
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

	// Phase 14 — alerting. The first three mirror .env.example verbatim;
	// the rest are this phase's additions (see AlertingConfig).
	v.SetDefault("alert_coalesce_window", "300s")
	v.SetDefault("alert_max_attempts", 8)
	v.SetDefault("alert_dead_letter_after", "24h")
	v.SetDefault("alert_dispatch_interval", "15s")
	v.SetDefault("alert_claim_size", 500)
	v.SetDefault("alert_retry_base", "60s")
	v.SetDefault("alert_retry_cap", "1h")
	// Phase 20.4 — the threshold evaluator. The four margins default to 0
	// here on purpose: internal/alerting.ThresholdPolicy owns the real
	// defaults (and the reasoning for each), and duplicating the numbers
	// in a second place is how the two drift apart.
	v.SetDefault("alert_threshold_interval", "10m")
	v.SetDefault("smtp_enabled", false)
	v.SetDefault("smtp_port", 587)
	v.SetDefault("smtp_from", "hangar@example.com")
	v.SetDefault("smtp_tls", "starttls")
}

// splitScopeList parses HANGAR_SSO_SCOPES, accepting commas, spaces or
// both. The OAuth request carries scopes space-separated, so an operator
// copying the set out of a browser URL has spaces; every other list-valued
// HANGAR setting is comma-separated, so an operator following house style
// has commas. Both work.
func splitScopeList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		MetricsAddr:    v.GetString("metrics_addr"),
		PublicURL:      v.GetString("public_url"),
		TrustedProxies: splitCSV(v.GetString("trusted_proxies")),
		Locale:         v.GetString("locale"),

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
			// Accepts either separator: the scope set appears
			// space-separated in the OAuth request itself, so an operator
			// copying it out of a browser URL naturally has spaces, while
			// every other list-valued HANGAR setting is comma-separated.
			// Rejecting one of those would be a papercut with no upside.
			Scopes: splitScopeList(v.GetString("sso_scopes")),
		},
		ESI: ESIConfig{
			RequestTimeout:         v.GetDuration("esi_request_timeout"),
			TTLFloor:               v.GetDuration("esi_ttl_floor"),
			ErrorLimitWindow:       v.GetDuration("esi_error_limit_window"),
			ErrorLimitMax:          v.GetInt("esi_error_limit_max"),
			ErrorLimitPauseAt:      v.GetInt("esi_error_limit_pause_at"),
			ErrorLimitResumeAt:     v.GetInt("esi_error_limit_resume_at"),
			StartupCatalogueIngest: v.GetBool("esi_startup_catalogue_ingest"),
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
		Alerting: AlertingConfig{
			CoalesceWindow:   v.GetDuration("alert_coalesce_window"),
			MaxAttempts:      v.GetInt("alert_max_attempts"),
			DeadLetterAfter:  v.GetDuration("alert_dead_letter_after"),
			DispatchInterval: v.GetDuration("alert_dispatch_interval"),
			ClaimSize:        int32(v.GetInt("alert_claim_size")),
			RetryBase:        v.GetDuration("alert_retry_base"),
			RetryCap:         v.GetDuration("alert_retry_cap"),
			SMTPEnabled:      v.GetBool("smtp_enabled"),
			SMTPHost:         v.GetString("smtp_host"),
			SMTPPort:         v.GetInt("smtp_port"),
			SMTPUsername:     v.GetString("smtp_username"),
			SMTPPassword:     NewSecret(v.GetString("smtp_password")),
			SMTPFrom:         v.GetString("smtp_from"),
			SMTPTo:           splitCSV(v.GetString("smtp_to")),
			SMTPTLS:          v.GetString("smtp_tls"),
			SlackWebhook:     NewSecret(v.GetString("slack_default_webhook")),
			DiscordWebhook:   NewSecret(v.GetString("discord_default_webhook")),

			ThresholdInterval:      v.GetDuration("alert_threshold_interval"),
			StructureFuelWithin:    v.GetDuration("alert_structure_fuel_within"),
			StarbaseFuelBelow:      v.GetInt64("alert_starbase_fuel_below"),
			StarbaseFuelBand:       v.GetInt64("alert_starbase_fuel_band"),
			MemberInactiveFor:      v.GetDuration("alert_member_inactive_for"),
			ContractExpiringWithin: v.GetDuration("alert_contract_expiring_within"),
		},
	}

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
