package sso

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// MinRefreshInterval bounds how recently a token must have been refreshed,
// as observed from *inside* the advisory lock, before a caller's own
// refresh attempt is treated as redundant and skipped. This is the
// mechanism that turns N concurrent RefreshCharacterToken calls for the
// same character into exactly one real EVE SSO token exchange (§7.3): the
// first caller to acquire the lock rotates and stamps last_refreshed_at;
// every other caller, once it acquires the lock in turn, re-reads and
// finds a token already fresh enough to skip.
const MinRefreshInterval = 2 * time.Minute

// RefreshPool is the pgx handle Refresher needs: Begin to open the
// transaction the advisory lock lives in, plus the plain gen.DBTX surface
// so gen.New(tx) can build a Store bound to that transaction.
type RefreshPool interface {
	gen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Refresher performs §7.3's advisory-lock-serialised refresh rotation.
type Refresher struct {
	Pool     RefreshPool
	OAuth    OAuthConfig
	Verifier *jwks.Verifier // set to nil to skip re-validating the refreshed access token
	Keyring  *crypto.Keyring

	// OnInvalidGrant fires when EVE SSO rejects the refresh with
	// invalid_grant — the revocation-path trigger (§7.3: "mark the token
	// invalid and fire the revocation path. Do not retry.").
	OnInvalidGrant func(ctx context.Context, characterID int64)
	// OnOwnerHashChanged mirrors callback.go's hook, for the case where a
	// refreshed access token's owner claim differs from what's stored.
	OnOwnerHashChanged func(ctx context.Context, characterID int64)
}

// ErrTokenNotFound is returned when characterID has no stored token to
// refresh.
var ErrTokenNotFound = errors.New("sso: refresh: no stored token for character")

// RefreshCharacterToken refreshes characterID's stored refresh token. It
// is safe to call concurrently for the same character from any number of
// goroutines or replicas — pg_advisory_xact_lock serialises them, and the
// freshness check inside the lock coalesces a concurrent burst into one
// real SSO round trip.
func (r *Refresher) RefreshCharacterToken(ctx context.Context, characterID int64) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sso: refresh: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// pg_advisory_xact_lock auto-releases at transaction end — no
	// separate unlock call, and it can never leak past a crash the way a
	// session-level advisory lock could. The lock key is built in Go, not
	// via `$1::text` concatenation in SQL: pgx infers each parameter's
	// wire encoding from its Go type (int64 -> the int8 OID), and a
	// `::text` cast on the Postgres side doesn't change what wire format
	// pgx sends — asking it to send an int64 where a text-encoded
	// parameter is expected fails before the cast ever runs.
	lockKey := fmt.Sprintf("esi_refresh:%d", characterID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return fmt.Errorf("sso: refresh: acquiring advisory lock: %w", err)
	}

	q := gen.New(tx)
	token, err := q.GetCharacterToken(ctx, characterID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrTokenNotFound
		}
		return fmt.Errorf("sso: refresh: reading token: %w", err)
	}

	if token.LastRefreshedAt != nil && time.Since(*token.LastRefreshedAt) < MinRefreshInterval {
		// Another concurrent caller already rotated this token while we
		// were waiting on the lock — nothing to do.
		return tx.Commit(ctx)
	}
	if !token.Valid {
		return fmt.Errorf("sso: refresh: token for character %d is marked invalid (%v)", characterID, token.InvalidReason)
	}

	plaintext, err := crypto.Open(r.Keyring, characterID, crypto.Sealed{
		KeyVersion: int(token.KeyVersion), WrappedDEK: token.WrappedDek, Nonce: token.Nonce, Ciphertext: token.Ciphertext,
	})
	if err != nil {
		return fmt.Errorf("sso: refresh: opening stored token: %w", err)
	}

	tokenResp, err := r.OAuth.RefreshToken(ctx, string(plaintext))
	if err != nil {
		if IsInvalidGrant(err) {
			reason := "invalid_grant"
			if invalidateErr := q.InvalidateCharacterToken(ctx, characterID, &reason); invalidateErr != nil {
				return fmt.Errorf("sso: refresh: invalidating after invalid_grant: %w", invalidateErr)
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return fmt.Errorf("sso: refresh: committing invalidation: %w", commitErr)
			}
			if r.OnInvalidGrant != nil {
				r.OnInvalidGrant(ctx, characterID)
			}
			return err // the caller must not retry (§7.3)
		}
		return fmt.Errorf("sso: refresh: exchanging refresh_token: %w", err)
	}

	ownerHash := token.OwnerHash
	if r.Verifier != nil {
		if claims, verr := r.Verifier.Verify(ctx, tokenResp.AccessToken); verr == nil {
			if claims.Owner != ownerHash && r.OnOwnerHashChanged != nil {
				r.OnOwnerHashChanged(ctx, characterID)
			}
			ownerHash = claims.Owner
		}
		// A verification failure here is not fatal to the refresh itself
		// — the new refresh token must still be persisted or it is lost
		// forever (EVE SSO already killed the old one) — but it also
		// means we cannot update owner_hash from this response.
	}

	sealed, err := crypto.Seal(r.Keyring, characterID, []byte(tokenResp.RefreshToken))
	if err != nil {
		return fmt.Errorf("sso: refresh: sealing new token: %w", err)
	}
	var accessExpiresAt *time.Time
	if tokenResp.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		accessExpiresAt = &t
	}
	if err := q.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID: characterID, KeyVersion: int32(sealed.KeyVersion),
		WrappedDek: sealed.WrappedDEK, Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext,
		AccessExpiresAt: accessExpiresAt, OwnerHash: ownerHash,
	}); err != nil {
		return fmt.Errorf("sso: refresh: persisting rotated token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sso: refresh: commit: %w", err)
	}
	return nil
}
