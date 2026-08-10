package discord

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrAPIVersionNotAllowed is returned by Config.Validate when APIVersion
// is not present in Allowlist — a boot-time config error, never a
// first-request surprise (01_ARCHITECTURE.md §9.3 / roadmap: "API version
// from configuration against an allowlist. A version outside the
// allowlist fails at config validation, not at first request.").
var ErrAPIVersionNotAllowed = errors.New("discord: API version not in allowlist")

// Config is the Discord driver's configuration — internal/config wires
// this from HANGAR_DISCORD_* (see .env.example's "ACCESS PROVISIONING —
// DISCORD" block, Phase 0-scaffolded, parsed into internal/config.Config
// starting this phase).
type Config struct {
	Enabled             bool
	BotToken            string
	GuildID             string
	APIVersion          int
	Allowlist           []int
	GlobalRate          int // requests/second, hard ceiling
	InvalidBudgetMax    int
	InvalidWarnPercent  int
	InvalidPausePercent int

	// BaseURL overrides DefaultBaseURL — tests point this at an
	// httptest.Server; production leaves it empty.
	BaseURL string
	// HTTPClient overrides http.DefaultClient — tests inject one with a
	// short timeout; production leaves it nil.
	HTTPClient *http.Client
}

// Validate checks cfg against every hard requirement the driver cannot
// safely start without. Called by NewClient, and by internal/config.Validate
// at boot when HANGAR_DISCORD_ENABLED=true, so both a direct
// discord.NewClient caller (this package's own tests) and the real
// bootstrap path fail at the same point for the same reason.
func (c Config) Validate() error {
	var errs []error
	if c.BotToken == "" {
		errs = append(errs, errors.New("discord: bot token is required"))
	}
	if c.GuildID == "" {
		errs = append(errs, errors.New("discord: guild id is required"))
	}
	if c.GlobalRate <= 0 {
		errs = append(errs, errors.New("discord: global rate must be positive"))
	}
	if allowed := containsInt(c.Allowlist, c.APIVersion); !allowed {
		errs = append(errs, fmt.Errorf("%w: version %d not in %v", ErrAPIVersionNotAllowed, c.APIVersion, c.Allowlist))
	}
	if len(errs) > 0 {
		return fmt.Errorf("discord: invalid configuration: %w", errors.Join(errs...))
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

// InvalidBudgetWindow is Phase 12's fixed window for the invalid-request
// budget — 10 minutes, per 01_ARCHITECTURE.md §9.3.
const InvalidBudgetWindow = 10 * time.Minute

// HierarchyCacheTTL is how long the role-hierarchy guard's bot-member and
// role-position snapshot is trusted before a fresh read — §9.3: "Cache the
// bot member and guild role positions for 60 s, invalidating on 403."
const HierarchyCacheTTL = 60 * time.Second
