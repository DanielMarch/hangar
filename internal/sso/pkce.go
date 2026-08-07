// Package sso implements 01_ARCHITECTURE.md §7's EVE SSO Authorization
// Code + PKCE S256 flow, refresh rotation, and token lifecycle.
package sso

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// verifierBytes is chosen so the base64url-no-padding encoding lands
// comfortably inside PKCE's 43-128 character range (§7.1): 64 raw bytes
// encode to 86 characters.
const verifierBytes = 64

// GenerateVerifier returns a CSPRNG code_verifier per RFC 7636 — 43-128
// characters from the unreserved URL-safe alphabet, which base64url with
// no padding already is.
func GenerateVerifier() (string, error) {
	raw := make([]byte, verifierBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("sso: generating pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Challenge computes code_challenge = BASE64URL(SHA256(verifier)) — the S256
// PKCE method (§7.1). HANGAR never uses the "plain" method.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GenerateState returns a CSPRNG state value, single-use with a 10-minute
// TTL enforced by the caller (session.expires_at).
func GenerateState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("sso: generating state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
