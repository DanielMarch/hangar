package crypto

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
)

// webhookAADTag is the literal 02_DATABASE_SCHEMA.md §4.6 binds a webhook
// endpoint's HMAC secret to, alongside the endpoint id and key version:
// "AAD = endpoint_id ‖ key_version ‖ 'webhook_secret'".
const webhookAADTag = "webhook_secret"

// buildWebhookAAD encodes that binding. The endpoint id is a uuid, so its
// 16 raw bytes go in fixed-width — same reasoning as buildAAD's big-endian
// int64: HANGAR owns both ends of this format and a fixed-width encoding
// cannot suffer a delimiter collision between the id and the tag.
//
// A DIFFERENT tag from refresh_token's is the point, not decoration. It
// means a row lifted out of app.webhook_endpoint cannot be decrypted as if
// it were an app.character_token row (or vice versa) even under the same
// master key: GCM authenticates the AAD, so a mismatched tag fails to open
// rather than silently yielding the wrong secret to the wrong subsystem.
func buildWebhookAAD(endpointID uuid.UUID, keyVersion int) []byte {
	aad := make([]byte, 16+4+len(webhookAADTag))
	copy(aad[0:16], endpointID[:])
	binary.BigEndian.PutUint32(aad[16:20], uint32(keyVersion))
	copy(aad[20:], webhookAADTag)
	return aad
}

// SealWebhookSecret envelope-encrypts an endpoint's HMAC secret, using the
// same per-payload-DEK scheme Seal uses for refresh tokens
// (01_ARCHITECTURE.md §7.4) with the §4.6 AAD.
func SealWebhookSecret(kr *Keyring, endpointID uuid.UUID, secret []byte) (Sealed, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal webhook secret: generating dek: %w", err)
	}
	defer zero(dek)

	version, wrapped, err := kr.WrapDEK(dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal webhook secret: %w", err)
	}

	gcm, err := newGCM(dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal webhook secret: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, fmt.Errorf("crypto: seal webhook secret: generating nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, secret, buildWebhookAAD(endpointID, version))

	return Sealed{KeyVersion: version, WrappedDEK: wrapped, Nonce: nonce, Ciphertext: ciphertext}, nil
}

// OpenWebhookSecret reverses SealWebhookSecret. Because the AAD binds
// endpointID, a Sealed row moved to a different endpoint fails to decrypt.
func OpenWebhookSecret(kr *Keyring, endpointID uuid.UUID, s Sealed) ([]byte, error) {
	dek, err := kr.UnwrapDEK(s.KeyVersion, s.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("crypto: open webhook secret: %w", err)
	}
	defer zero(dek)

	gcm, err := newGCM(dek)
	if err != nil {
		return nil, fmt.Errorf("crypto: open webhook secret: %w", err)
	}
	secret, err := gcm.Open(nil, s.Nonce, s.Ciphertext, buildWebhookAAD(endpointID, s.KeyVersion))
	if err != nil {
		return nil, fmt.Errorf("crypto: open webhook secret: authentication failed: %w", err)
	}
	return secret, nil
}

// NewWebhookSecret generates a fresh 32-byte HMAC secret — the size of
// SHA-256's block output, so the HMAC key needs neither padding nor
// hashing down before use.
func NewWebhookSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("crypto: generating webhook secret: %w", err)
	}
	return secret, nil
}
