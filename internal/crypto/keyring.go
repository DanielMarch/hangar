// Package crypto implements Phase 5's envelope encryption for stored
// refresh tokens (01_ARCHITECTURE.md §7.4). Per-token DEK, AES-256-GCM,
// wrapped with a versioned master key so key rotation is a fast,
// metadata-only operation — payloads are never re-encrypted.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/hangar-project/hangar/internal/config"
)

// Keyring holds the current master key (and, during a rotation window, the
// previous one) used to wrap/unwrap per-token DEKs. It never holds the
// refresh token plaintext itself — that's envelope.go's job, one layer up.
type Keyring struct {
	current  keyMaterial
	previous *keyMaterial // nil outside a rotation window
}

type keyMaterial struct {
	version int
	key     []byte // exactly 32 bytes (AES-256)
}

// NewKeyring builds a Keyring from config.CryptoConfig. MasterKey and (if
// present) MasterKeyPrevious must each decode as base64 to exactly 32
// bytes — the same contract internal/config/validate.go's require32ByteKey
// already enforces at boot, so a Config that passed Validate always builds
// a valid Keyring here.
func NewKeyring(cfg config.CryptoConfig) (*Keyring, error) {
	current, err := decodeKey(cfg.MasterKey.Reveal())
	if err != nil {
		return nil, fmt.Errorf("crypto: keyring: HANGAR_MASTER_KEY: %w", err)
	}
	kr := &Keyring{current: keyMaterial{version: cfg.MasterKeyVersion, key: current}}

	if !cfg.MasterKeyPrevious.Empty() {
		prev, err := decodeKey(cfg.MasterKeyPrevious.Reveal())
		if err != nil {
			return nil, fmt.Errorf("crypto: keyring: HANGAR_MASTER_KEY_PREVIOUS: %w", err)
		}
		kr.previous = &keyMaterial{version: cfg.MasterKeyVersion - 1, key: prev}
	}
	return kr, nil
}

func decodeKey(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decoding base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("want 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

// CurrentVersion is the key_version every new wrap uses.
func (k *Keyring) CurrentVersion() int { return k.current.version }

func (k *Keyring) keyForVersion(version int) ([]byte, bool) {
	if version == k.current.version {
		return k.current.key, true
	}
	if k.previous != nil && version == k.previous.version {
		return k.previous.key, true
	}
	return nil, false
}

// WrapDEK encrypts dek (a per-token 32-byte data-encryption key) under the
// current master key with AES-256-GCM. The returned wrapped blob is
// nonce‖ciphertext — self-contained, since the master key's own nonce
// doesn't need the AAD binding the token payload's does (there is nothing
// to bind a DEK-wrap to; the binding to character/version lives one layer
// up, on the payload itself).
func (k *Keyring) WrapDEK(dek []byte) (version int, wrapped []byte, err error) {
	gcm, err := newGCM(k.current.key)
	if err != nil {
		return 0, nil, fmt.Errorf("crypto: wrap dek: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return 0, nil, fmt.Errorf("crypto: wrap dek: generating nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, dek, nil)
	return k.current.version, sealed, nil
}

// UnwrapDEK reverses WrapDEK using whichever key (current or previous)
// matches version.
func (k *Keyring) UnwrapDEK(version int, wrapped []byte) ([]byte, error) {
	key, ok := k.keyForVersion(version)
	if !ok {
		return nil, fmt.Errorf("crypto: unwrap dek: no key material for version %d", version)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: unwrap dek: %w", err)
	}
	if len(wrapped) < gcm.NonceSize() {
		return nil, fmt.Errorf("crypto: unwrap dek: wrapped blob shorter than a nonce")
	}
	nonce, ciphertext := wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():]
	dek, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: unwrap dek: %w", err)
	}
	return dek, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
