package teamspeak

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ErrChallengeAlreadyConsumed covers all three ways a redemption can fail
// to find a matching row: already consumed, expired, or never issued —
// deliberately not distinguished from the SQL result alone (RedeemChallenge's
// UPDATE either affects the one still-valid row or none at all), so a
// caller (and any error message reaching an end user) never leaks which
// of the three applies.
var ErrChallengeAlreadyConsumed = errors.New("teamspeak: challenge token already consumed, expired, or unknown")

// tokenBytes is the random payload length for a challenge token before
// base32 encoding — 20 bytes (160 bits) is comfortably beyond brute-force
// range for a token a user types into a TS3 nickname by hand.
const tokenBytes = 20

// NewChallengeToken generates a random single-use token, base32-encoded
// (RFC 4648, no padding) so it's short enough to type into a TS3 nickname
// and free of characters TS3's own escaping would otherwise have to
// handle (base32's alphabet is a strict subset of ASCII letters/digits).
func NewChallengeToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("teamspeak: generating challenge token: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// IssueChallenge creates a new single-use token for userID, valid for ttl.
func IssueChallenge(ctx context.Context, s *store.Store, userID uuid.UUID, ttl time.Duration) (gen.AppTeamspeakChallenge, error) {
	token, err := NewChallengeToken()
	if err != nil {
		return gen.AppTeamspeakChallenge{}, err
	}
	row, err := s.IssueTeamspeakChallenge(ctx, token, userID, time.Now().Add(ttl))
	if err != nil {
		return gen.AppTeamspeakChallenge{}, fmt.Errorf("teamspeak: issuing challenge for user %s: %w", userID, err)
	}
	return row, nil
}

// RedeemChallenge consumes token, binding it to clientUniqueIdentifier —
// the base64 TS3 client UID observed in-client. Single-use is enforced by
// the underlying UPDATE's WHERE clause (challenge.sql's own doc comment);
// this wrapper's only job is translating "affected zero rows" into
// ErrChallengeAlreadyConsumed rather than a raw pgx.ErrNoRows leaking out
// of this package.
func RedeemChallenge(ctx context.Context, s *store.Store, token, clientUniqueIdentifier string) (gen.AppTeamspeakChallenge, error) {
	row, err := s.RedeemTeamspeakChallenge(ctx, token, &clientUniqueIdentifier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.AppTeamspeakChallenge{}, ErrChallengeAlreadyConsumed
		}
		return gen.AppTeamspeakChallenge{}, fmt.Errorf("teamspeak: redeeming challenge: %w", err)
	}
	return row, nil
}
