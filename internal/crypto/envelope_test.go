package crypto_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/stretchr/testify/require"
)

func testKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	kr, err := crypto.NewKeyring(config.CryptoConfig{
		MasterKey:        config.NewSecret(base64.StdEncoding.EncodeToString(raw)),
		MasterKeyVersion: 1,
	})
	require.NoError(t, err)
	return kr
}

func TestEnvelopeSealOpenRoundTrip(t *testing.T) {
	kr := testKeyring(t)
	plaintext := []byte("a-refresh-token-value")

	sealed, err := crypto.Seal(kr, 12345, plaintext)
	require.NoError(t, err)
	require.Equal(t, 1, sealed.KeyVersion)

	got, err := crypto.Open(kr, 12345, sealed)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

// TestEnvelopeAADRejectsMismatchedCharacter (roadmap exit criterion): a
// ciphertext moved to another character's record must fail to decrypt.
func TestEnvelopeAADRejectsMismatchedCharacter(t *testing.T) {
	kr := testKeyring(t)
	sealed, err := crypto.Seal(kr, 111, []byte("secret-token"))
	require.NoError(t, err)

	_, err = crypto.Open(kr, 222, sealed)
	require.Error(t, err, "opening under a different character_id must fail authentication")
}

func TestEnvelopeRotationOldVersionStillDecrypts(t *testing.T) {
	raw1 := make([]byte, 32)
	_, err := rand.Read(raw1)
	require.NoError(t, err)
	kr1, err := crypto.NewKeyring(config.CryptoConfig{
		MasterKey: config.NewSecret(base64.StdEncoding.EncodeToString(raw1)), MasterKeyVersion: 1,
	})
	require.NoError(t, err)

	sealed, err := crypto.Seal(kr1, 42, []byte("token-v1"))
	require.NoError(t, err)

	raw2 := make([]byte, 32)
	_, err = rand.Read(raw2)
	require.NoError(t, err)
	kr2, err := crypto.NewKeyring(config.CryptoConfig{
		MasterKey:         config.NewSecret(base64.StdEncoding.EncodeToString(raw2)),
		MasterKeyVersion:  2,
		MasterKeyPrevious: config.NewSecret(base64.StdEncoding.EncodeToString(raw1)),
	})
	require.NoError(t, err)

	got, err := crypto.Open(kr2, 42, sealed)
	require.NoError(t, err, "a payload sealed under the previous key version must still open during a rotation window")
	require.Equal(t, []byte("token-v1"), got)
}

func TestEnvelopeUnknownVersionFails(t *testing.T) {
	kr := testKeyring(t)
	sealed, err := crypto.Seal(kr, 1, []byte("x"))
	require.NoError(t, err)
	sealed.KeyVersion = 99
	_, err = crypto.Open(kr, 1, sealed)
	require.Error(t, err)
}
