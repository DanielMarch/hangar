package v1_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/stretchr/testify/require"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyMumbleAuthSignature(t *testing.T) {
	body := []byte(`{"certificate_hash":"deadbeef"}`)
	valid := sign("shared-secret", body)

	require.True(t, v1.VerifyMumbleAuthSignature("shared-secret", body, valid))
	require.False(t, v1.VerifyMumbleAuthSignature("wrong-secret", body, valid))
	require.False(t, v1.VerifyMumbleAuthSignature("shared-secret", []byte(`{"certificate_hash":"tampered"}`), valid))
	require.False(t, v1.VerifyMumbleAuthSignature("shared-secret", body, "not-valid-hex"))
	require.False(t, v1.VerifyMumbleAuthSignature("shared-secret", body, ""))
}
