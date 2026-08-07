package config_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/stretchr/testify/require"
)

func validKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

// setEnv sets HANGAR_-prefixed env vars for the duration of the test.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
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
	required := []string{
		"HANGAR_DB_URL",
		"HANGAR_MASTER_KEY",
		"HANGAR_SESSION_SECRET",
		"HANGAR_SSO_CLIENT_ID",
		"HANGAR_SSO_CLIENT_SECRET",
	}
	for _, missing := range required {
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
