package discord_test

import (
	"testing"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/discord"
	"github.com/stretchr/testify/require"
)

func validConfig() discord.Config {
	return discord.Config{
		BotToken:            "bot-token",
		GuildID:             "123456789",
		APIVersion:          10,
		Allowlist:           []int{10},
		GlobalRate:          50,
		InvalidBudgetMax:    10000,
		InvalidWarnPercent:  50,
		InvalidPausePercent: 80,
	}
}

// TestAPIVersionAllowlistEnforced (roadmap exit criterion): a version
// outside the allowlist fails at config validation, not at first request
// — Validate is what NewClient calls before ever touching the network.
func TestAPIVersionAllowlistEnforced(t *testing.T) {
	cfg := validConfig()
	cfg.APIVersion = 9 // not in cfg.Allowlist ([10])

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, discord.ErrAPIVersionNotAllowed)

	_, clientErr := discord.NewClient(cfg, nil, nil, nil)
	require.Error(t, clientErr, "NewClient must refuse to construct a client for a disallowed version — no request can ever be attempted")
	require.ErrorIs(t, clientErr, discord.ErrAPIVersionNotAllowed)
}

// TestAPIVersionInAllowlistSucceeds is the control case.
func TestAPIVersionInAllowlistSucceeds(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.Validate())

	client, err := discord.NewClient(cfg, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// TestConfigValidateRequiresBotTokenAndGuild: the other two hard
// requirements Validate enforces, exercised independently of the
// allowlist so a failure message is never ambiguous about which
// requirement was missed.
func TestConfigValidateRequiresBotTokenAndGuild(t *testing.T) {
	cfg := validConfig()
	cfg.BotToken = ""
	require.Error(t, cfg.Validate())

	cfg = validConfig()
	cfg.GuildID = ""
	require.Error(t, cfg.Validate())
}
