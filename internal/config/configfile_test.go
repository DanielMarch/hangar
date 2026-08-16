package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// ── PHASE 22, DEFECT B-7 ─────────────────────────────────────────────────
//
// internal/config.New used to declare a config TYPE as well as a config
// NAME, which turns on viper's "a file called exactly `hangar`, with no
// extension, counts as a config file" fallback. In 01_ARCHITECTURE.md
// §9.2's manual layout — /opt/hangar/hangar with
// WorkingDirectory=/opt/hangar, which is what a systemd unit produces —
// the file with that name in that directory is the binary itself, so every
// invocation tried to parse the executable as YAML:
//
//	Error: config: reading config file: While parsing config:
//	       yaml: control characters are not allowed
//
// naming neither the file nor the reason. Measured both directions in Gate
// 5: the same bytes mounted at /opt/hangar-bin migrated an external
// PostgreSQL 18 cleanly.
//
// Every test here uses t.Chdir, because the defect is specifically about
// the working-directory search path and nothing else reproduces it.

// elfPrefix is a plausible prefix of the real binary. The control
// characters are the point: they are what the YAML parser objected to.
func elfPrefix() []byte {
	return append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 64)...)
}

// TestConfigIgnoresAnExtensionlessFileNamedHangar is the regression test at
// the level the defect lives: the search itself must not see the binary.
func TestConfigIgnoresAnExtensionlessFileNamedHangar(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hangar"), elfPrefix(), 0o600))
	t.Chdir(dir)

	err := config.New().ReadInConfig()

	// "No config file found" is the normal condition for every installation
	// that configures by environment, and config.Load swallows exactly this
	// error type. A PARSE failure is fatal. Before the fix, this directory
	// produced the second.
	var notFound viper.ConfigFileNotFoundError
	require.ErrorAs(t, err, &notFound,
		"an extensionless file named `hangar` must be invisible to the config search, not parsed as YAML")
}

// TestConfigLoadsBesideTheBinary is the same thing at the level Gate 5
// measured: `hangar migrate up` in its own installation directory.
func TestConfigLoadsBesideTheBinary(t *testing.T) {
	setEnv(t, fullValidEnv())
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hangar"), elfPrefix(), 0o600))
	t.Chdir(dir)

	cfg, err := config.Load(config.New())
	require.NoError(t, err, "the documented §9.2 layout must boot")
	require.Equal(t, "en", cfg.Locale, "and with defaults intact")
}

// TestConfigStillReadsHangarYaml pins the other half: dropping the config
// type must not cost an operator the config file the documentation tells
// them they can write. The binary is present too, exactly as it would be
// in /opt/hangar.
func TestConfigStillReadsHangarYaml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hangar"), elfPrefix(), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hangar.yaml"),
		[]byte("locale: de\nlog_level: debug\n"), 0o600))
	t.Chdir(dir)

	v := config.New()
	require.NoError(t, v.ReadInConfig())
	require.Equal(t, filepath.Join(dir, "hangar.yaml"), v.ConfigFileUsed())
	require.Equal(t, "de", v.GetString("locale"))
	require.Equal(t, "debug", v.GetString("log_level"))
}

// ── PHASE 22, DEFECT B-8 ─────────────────────────────────────────────────
//
// 04_RELEASE_GATES.md §5.3 requires a HANGAR_PUBLIC_URL / callback mismatch
// to be "reported as a configuration error with the expected value shown,
// not as an opaque OAuth failure". Gate 5 recorded the FAIL against
// v1.0.0-rc1 with a boot log: `serve` started normally with
// HANGAR_PUBLIC_URL=https://hangar.example.com and
// HANGAR_SSO_CALLBACK_URL=https://SOMETHING-ELSE.example.com/auth/callback.

func TestConfigRejectsCallbackPublicURLMismatch(t *testing.T) {
	env := fullValidEnv()
	env["HANGAR_PUBLIC_URL"] = "https://hangar.example.com"
	env["HANGAR_SSO_CALLBACK_URL"] = "https://SOMETHING-ELSE.example.com/auth/callback"
	setEnv(t, env)

	_, err := config.Load(config.New())
	require.Error(t, err)
	// The condition is not "they disagree" — an operator who set one of
	// them wrong needs the other. The expected value must be in the text.
	require.Contains(t, err.Error(), "https://hangar.example.com/auth/callback",
		"the error must name the EXPECTED callback, not merely say the two disagree")
	require.Contains(t, err.Error(), "HANGAR_SSO_CALLBACK_URL")
	require.Contains(t, err.Error(), "HANGAR_PUBLIC_URL")
}

func TestConfigAcceptsMatchingCallback(t *testing.T) {
	for _, tc := range []struct{ name, public, callback string }{
		{"exact", "https://hangar.example.com", "https://hangar.example.com/auth/callback"},
		{"trailing slash on the public url", "https://hangar.example.com/", "https://hangar.example.com/auth/callback"},
		{"host case", "https://Hangar.Example.com", "https://hangar.example.com/auth/callback"},
		{"non-default port", "http://localhost:8099", "http://localhost:8099/auth/callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fullValidEnv()
			env["HANGAR_PUBLIC_URL"] = tc.public
			env["HANGAR_SSO_CALLBACK_URL"] = tc.callback
			setEnv(t, env)

			_, err := config.Load(config.New())
			require.NoError(t, err)
		})
	}
}

// The shipped defaults must agree with each other, or every installation
// that configures nothing fails to boot.
func TestConfigDefaultsAgreeOnTheCallback(t *testing.T) {
	setEnv(t, fullValidEnv())
	cfg, err := config.Load(config.New())
	require.NoError(t, err)
	require.Equal(t, config.ExpectedCallbackURL(cfg.PublicURL), cfg.SSO.CallbackURL)
}

func TestConfigRejectsNonAbsolutePublicURL(t *testing.T) {
	env := fullValidEnv()
	env["HANGAR_PUBLIC_URL"] = "hangar.example.com"
	setEnv(t, env)

	_, err := config.Load(config.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "HANGAR_PUBLIC_URL")
	require.Contains(t, err.Error(), "absolute URL")
}
