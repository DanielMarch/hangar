package crypto

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// Sealed is the four fields 02_DATABASE_SCHEMA.md's app.character_token
// stores per refresh token: key_version, wrapped_dek, nonce, ciphertext.
type Sealed struct {
	KeyVersion int
	WrappedDEK []byte
	Nonce      []byte
	Ciphertext []byte
}

// aadTag is the literal §7.4 AAD binds every payload to, alongside the
// character ID and key version.
const aadTag = "refresh_token"

// buildAAD deterministically encodes "character_id ‖ key_version ‖
// 'refresh_token'" (01_ARCHITECTURE.md §7.4). Fixed-width big-endian
// integers rather than a delimited string: HANGAR controls both ends of
// this format, and a fixed-width encoding can never have a delimiter
// collision between the character ID and the tag.
func buildAAD(characterID int64, keyVersion int) []byte {
	aad := make([]byte, 8+4+len(aadTag))
	binary.BigEndian.PutUint64(aad[0:8], uint64(characterID))
	binary.BigEndian.PutUint32(aad[8:12], uint32(keyVersion))
	copy(aad[12:], aadTag)
	return aad
}

// Seal envelope-encrypts plaintext (the refresh token) for characterID: a
// fresh 32-byte DEK encrypts the payload under AES-256-GCM with AAD bound
// to (characterID, the keyring's current version); the DEK itself is then
// wrapped under the master key (01_ARCHITECTURE.md §7.4).
func Seal(kr *Keyring, characterID int64, plaintext []byte) (Sealed, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal: generating dek: %w", err)
	}
	defer zero(dek)

	version, wrapped, err := kr.WrapDEK(dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal: %w", err)
	}

	gcm, err := newGCM(dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal: generating nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, buildAAD(characterID, version))

	return Sealed{KeyVersion: version, WrappedDEK: wrapped, Nonce: nonce, Ciphertext: ciphertext}, nil
}

// Open reverses Seal. Because the AAD binds characterID into the tag GCM
// verifies, a Sealed row moved to a different character_id fails to
// decrypt — the Phase 5 adversarial test (TestEnvelopeAADRejectsMismatchedCharacter).
func Open(kr *Keyring, characterID int64, s Sealed) ([]byte, error) {
	dek, err := kr.UnwrapDEK(s.KeyVersion, s.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	defer zero(dek)

	gcm, err := newGCM(dek)
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	plaintext, err := gcm.Open(nil, s.Nonce, s.Ciphertext, buildAAD(characterID, s.KeyVersion))
	if err != nil {
		return nil, fmt.Errorf("crypto: open: authentication failed: %w", err)
	}
	return plaintext, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
