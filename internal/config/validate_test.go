package config_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/stretchr/testify/require"
)

func validKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

// setEnv sets HANGAR_-prefixed env vars for the duration of the test.
// setEnv installs kv as THE environment for the required-secret variables
// — every name in requiredSecretEnv that kv does not mention is explicitly
// blanked, not merely left alone.
//
// PHASE 15.1 FIX. This used to only set the keys present in kv, which
// meant `delete(env, "HANGAR_DB_URL")` removed the key from the map but
// left any AMBIENT HANGAR_DB_URL untouched — so
// TestConfigFailsFastOnEachRequiredSecret asserted "loading fails when
// this secret is missing" while the secret was still perfectly present in
// the process environment, and the assertion failed.
//
// That made the test pass only in a shell with none of the five variables
// exported, and fail in the one environment that matters most: CI exports
// HANGAR_DB_URL for the `make ci` / `make ci-strict` step
// (.github/workflows/ci.yml), so `make test` inside it could never have
// gone green. It is a latent Phase 0 defect — reproduced on Phase 15's own
// commit (46b803b) — and corroborates Phase 14.1's finding that `make ci`
// had never actually run.
//
// Blanking rather than unsetting is deliberate: t.Setenv restores the
// previous value on cleanup, so the test stays hermetic, and
// internal/config treats "" as missing for all five (requireSecret checks
// Secret.Empty(), requireString checks v == "").
// setEnv gives the test a HERMETIC environment: every HANGAR_-prefixed
// variable present in the ambient environment is cleared first, then kv is
// applied.
//
// PHASE 18. This used to clear only requiredSecretEnv, so every OTHER
// HANGAR_ variable an operator happened to have exported leaked in through
// viper's AutomaticEnv — and TestConfigLoadsSuccessfullyWithAllRequiredValues
// asserts that DEFAULTS are applied, which is only true in a clean
// environment. It had never bitten because nothing else required those
// variables to be set while running the tests.
//
// Phase 18 changed that: `make ci` now runs `make e2e`, which is guarded on
// HANGAR_DB_URL, so the documented way to run the full gate is with
// HANGAR_* exported — and `HANGAR_ENV=development` in a developer's shell
// would fail a config test that has nothing to do with either. Clearing the
// whole prefix is the fix; enumerating the variables would just be a
// second copy of applyDefaults' key list, waiting to drift.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(key, "HANGAR_") {
			if _, override := kv[key]; !override {
				t.Setenv(key, "")
			}
		}
	}
	for _, k := range requiredSecretEnv {
		if _, ok := kv[k]; !ok {
			t.Setenv(k, "")
		}
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// requiredSecretEnv is the closed set of environment variables
// internal/config treats as required secrets — the same list
// TestConfigFailsFastOnEachRequiredSecret iterates.
var requiredSecretEnv = []string{
	"HANGAR_DB_URL",
	"HANGAR_MASTER_KEY",
	"HANGAR_SESSION_SECRET",
	"HANGAR_SSO_CLIENT_ID",
	"HANGAR_SSO_CLIENT_SECRET",
}

func fullValidEnv() map[string]string {
	return map[string]string{
		"HANGAR_DB_URL":            "postgres://hangar:pw@localhost:5432/hangar?sslmode=disable",
		"HANGAR_MASTER_KEY":        validKey(),
		"HANGAR_SESSION_SECRET":    validKey(),
		"HANGAR_SSO_CLIENT_ID":     "client-id",
		"HANGAR_SSO_CLIENT_SECRET": "client-secret",
	}
}

// TestConfigFailsFastOnMissingSecrets is a named Phase 0 exit criterion:
// absent HANGAR_MASTER_KEY aborts boot with a named error, never a generated
// key.
func TestConfigFailsFastOnMissingSecrets(t *testing.T) {
	env := fullValidEnv()
	delete(env, "HANGAR_MASTER_KEY")
	setEnv(t, env)

	v := config.New()
	cfg, err := config.Load(v)

	require.Error(t, err)
	require.Nil(t, cfg)
	require.ErrorIs(t, err, config.ErrMissingSecret)
	require.Contains(t, err.Error(), "HANGAR_MASTER_KEY")

	// Never a generated fallback: a second independent load with the secret
	// still absent must fail identically, not silently mint a key.
	v2 := config.New()
	_, err2 := config.Load(v2)
	require.Error(t, err2)
}

func TestConfigFailsFastOnEachRequiredSecret(t *testing.T) {
	for _, missing := range requiredSecretEnv {
		t.Run(missing, func(t *testing.T) {
			env := fullValidEnv()
			delete(env, missing)
			setEnv(t, env)

			v := config.New()
			_, err := config.Load(v)
			require.Error(t, err)
			require.Contains(t, err.Error(), missing)
		})
	}
}

func TestConfigRejectsMalformedMasterKey(t *testing.T) {
	env := fullValidEnv()
	env["HANGAR_MASTER_KEY"] = "not-base64-and-not-32-bytes"
	setEnv(t, env)

	v := config.New()
	_, err := config.Load(v)
	require.Error(t, err)
	require.ErrorIs(t, err, config.ErrInvalidSecret)
}

func TestConfigLoadsSuccessfullyWithAllRequiredValues(t *testing.T) {
	setEnv(t, fullValidEnv())

	v := config.New()
	cfg, err := config.Load(v)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "production", cfg.Env) // default applied
}

func TestSecretRedactsInEveryPath(t *testing.T) {
	s := config.NewSecret("super-secret-value")

	require.Equal(t, "[REDACTED]", s.String())

	for _, verb := range []string{"%s", "%v", "%q", "%+v", "%#v"} {
		out := fmt.Sprintf(verb, s)
		require.NotContainsf(t, out, "super-secret-value", "verb %s leaked the secret", verb)
		require.Contains(t, out, "[REDACTED]")
	}

	b, err := json.Marshal(s)
	require.NoError(t, err)
	require.NotContains(t, string(b), "super-secret-value")

	type wrapper struct {
		Token config.Secret `json:"token"`
	}
	wb, err := json.Marshal(wrapper{Token: s})
	require.NoError(t, err)
	require.NotContains(t, string(wb), "super-secret-value")

	require.Equal(t, "super-secret-value", s.Reveal())
}
