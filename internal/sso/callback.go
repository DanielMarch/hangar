package sso

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/scopes"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// StateTTL is §7.1's fixed single-use window for a pending login's state
// and PKCE verifier. It bounds only the PRE-auth row; the authenticated
// session that replaces it on callback gets Flow.SessionTTL instead (see
// DefaultSessionTTL and db/queries/user.sql's CompleteSessionLogin note).
const StateTTL = 10 * time.Minute

// DefaultSessionTTL is the authenticated session lifetime used when
// Flow.SessionTTL is left zero. It matches internal/config's
// `session_ttl` default (720h / 30 days) so a Flow built without explicit
// configuration behaves the same as one built from a default Config.
const DefaultSessionTTL = 720 * time.Hour

// Store is the subset of gen.Querier the SSO flow needs, declared
// narrowly against gen's own types (the same convention
// internal/esi/catalogue.Store uses) so *gen.Queries and *store.Store both
// satisfy it with no adapter.
type Store interface {
	CreateSession(ctx context.Context, arg gen.CreateSessionParams) (gen.AppSession, error)
	GetSession(ctx context.Context, sessionID uuid.UUID) (gen.AppSession, error)
	CompleteSessionLogin(ctx context.Context, sessionID uuid.UUID, userID uuid.NullUUID, expiresAt time.Time) error
	DeleteSession(ctx context.Context, sessionID uuid.UUID) error

	GetCharacter(ctx context.Context, characterID int64) (gen.AppCharacter, error)
	UpsertCharacter(ctx context.Context, arg gen.UpsertCharacterParams) (gen.AppCharacter, error)

	CreateUser(ctx context.Context, displayName string) (gen.AppUser, error)
	GetUser(ctx context.Context, userID uuid.UUID) (gen.AppUser, error)
	SetUserMainCharacter(ctx context.Context, userID uuid.UUID, mainCharacterID *int64) error
	TouchUserLastLogin(ctx context.Context, userID uuid.UUID) error

	GetCharacterToken(ctx context.Context, characterID int64) (gen.AppCharacterToken, error)
	UpsertCharacterToken(ctx context.Context, arg gen.UpsertCharacterTokenParams) error
	InvalidateCharacterToken(ctx context.Context, characterID int64, invalidReason *string) error

	ReplaceCharacterTokenScopes(ctx context.Context, characterID int64) error
	AddCharacterTokenScope(ctx context.Context, characterID int64, scope string) error
	UpsertEsiScope(ctx context.Context, scope string) error
}

// Flow orchestrates the login round trip: BeginLogin creates a pre-auth
// session and returns the redirect URL; HandleCallback exchanges the code,
// validates the returned access token offline, resolves/creates the
// character and user, and persists the envelope-sealed refresh token.
type Flow struct {
	Store    Store
	OAuth    OAuthConfig
	Verifier *jwks.Verifier
	Keyring  *crypto.Keyring

	// SessionTTL is how long an authenticated session lives once the
	// callback completes. Zero means DefaultSessionTTL. cmd/hangar wires
	// config.CryptoConfig.SessionTTL here.
	SessionTTL time.Duration

	// OnOwnerHashChanged is called (if set) whenever a character's
	// owner_hash changes — an entitlement-reducing transfer event that
	// feeds provision-urgent in Phase 11. Phase 5 has no provisioning
	// engine to call yet, so the default is a no-op; lifecycle.go wires
	// the real invalidation regardless of whether this hook is set.
	OnOwnerHashChanged func(ctx context.Context, characterID int64)
}

// PendingLogin is what BeginLogin hands back for the caller to persist as
// an HttpOnly, SameSite=Lax cookie (§7.1) — never localStorage, never a
// URL parameter.
type PendingLogin struct {
	SessionID   uuid.UUID
	RedirectURL string
	ExpiresAt   time.Time
}

// BeginLogin creates a pre-auth session holding the PKCE verifier and
// state, and returns the EVE SSO authorization URL to redirect the browser
// to. scopeList is the requested scope set — passed through verbatim,
// never validated (internal/scopes' opacity contract).
func (f *Flow) BeginLogin(ctx context.Context, scopeList []string, ipAddress *string, userAgent *string) (*PendingLogin, error) {
	verifier, err := GenerateVerifier()
	if err != nil {
		return nil, err
	}
	state, err := GenerateState()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(StateTTL)

	session, err := f.Store.CreateSession(ctx, gen.CreateSessionParams{
		UserID:       uuid.NullUUID{},
		PkceVerifier: &verifier,
		State:        &state,
		UserAgent:    userAgent,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("sso: begin login: creating session: %w", err)
	}

	return &PendingLogin{
		SessionID:   session.SessionID,
		RedirectURL: f.OAuth.AuthorizeURLFor(state, Challenge(verifier), scopeList),
		ExpiresAt:   expiresAt,
	}, nil
}

// LoginResult is HandleCallback's outcome.
type LoginResult struct {
	UserID        uuid.UUID
	CharacterID   int64
	CharacterName string
	Scopes        []string
	IsNewUser     bool
	// SessionExpiresAt is the authenticated session's new expiry, so the
	// HTTP layer can re-issue the session cookie with a matching Expires
	// instead of leaving the browser holding the 10-minute pre-auth one.
	SessionExpiresAt time.Time
}

// sessionTTL resolves the configured authenticated-session lifetime,
// falling back to DefaultSessionTTL when unset.
func (f *Flow) sessionTTL() time.Duration {
	if f.SessionTTL > 0 {
		return f.SessionTTL
	}
	return DefaultSessionTTL
}

// HandleCallback completes the login: validates state against the
// session, exchanges code for tokens, validates the access token offline,
// resolves or creates the character/user, and persists the envelope-sealed
// refresh token.
func (f *Flow) HandleCallback(ctx context.Context, sessionID uuid.UUID, code, state string) (*LoginResult, error) {
	session, err := f.Store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sso: callback: session not found or expired: %w", err)
	}
	if session.State == nil || session.PkceVerifier == nil {
		return nil, fmt.Errorf("sso: callback: session has no pending login (state already consumed or never issued)")
	}
	// state is single-use (§7.1): constant-time-irrelevant here since
	// state isn't a secret comparison target on its own (the verifier is
	// what's bound to possession of the original browser), but an exact
	// match is still required to guard against a forged callback.
	if state != *session.State {
		return nil, fmt.Errorf("sso: callback: state mismatch")
	}
	verifier := *session.PkceVerifier

	tokenResp, err := f.OAuth.ExchangeCode(ctx, code, verifier)
	if err != nil {
		return nil, fmt.Errorf("sso: callback: exchanging code: %w", err)
	}

	claims, err := f.Verifier.Verify(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("sso: callback: validating access token: %w", err)
	}
	characterID, err := claims.CharacterID()
	if err != nil {
		return nil, fmt.Errorf("sso: callback: %w", err)
	}

	userID, isNewUser, err := f.resolveUser(ctx, characterID, claims)
	if err != nil {
		return nil, err
	}

	if err := f.persistToken(ctx, characterID, tokenResp, claims); err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(f.sessionTTL())
	if err := f.Store.CompleteSessionLogin(ctx, sessionID, uuid.NullUUID{UUID: userID, Valid: true}, expiresAt); err != nil {
		return nil, fmt.Errorf("sso: callback: completing session: %w", err)
	}
	if err := f.Store.TouchUserLastLogin(ctx, userID); err != nil {
		return nil, fmt.Errorf("sso: callback: touching last login: %w", err)
	}

	return &LoginResult{
		UserID: userID, CharacterID: characterID, CharacterName: claims.Name,
		Scopes: []string(claims.Scopes), IsNewUser: isNewUser,
		SessionExpiresAt: expiresAt,
	}, nil
}

// resolveUser finds or creates the app.user for characterID, and fires the
// owner_hash-changed hook if the stored character's owner_hash no longer
// matches the token's (§7.2's transfer edge case).
func (f *Flow) resolveUser(ctx context.Context, characterID int64, claims *jwks.Claims) (userID uuid.UUID, isNew bool, err error) {
	existing, err := f.Store.GetCharacter(ctx, characterID)
	if err == nil {
		if existing.OwnerHash != claims.Owner && f.OnOwnerHashChanged != nil {
			f.OnOwnerHashChanged(ctx, characterID)
		}
		if existing.UserID.Valid {
			return existing.UserID.UUID, false, nil
		}
		// Character row exists (e.g. seeded by an earlier sync) but has
		// no owning user yet — fall through to create one below.
	}
	// Otherwise there is no existing row at all: a character brand new to
	// this installation, created below.

	user, err := f.Store.CreateUser(ctx, claims.Name)
	if err != nil {
		return uuid.UUID{}, false, fmt.Errorf("sso: resolving user: creating user: %w", err)
	}
	// PHASE 15.1 FIX — ORDERING. app.user and app.character reference each
	// other (app.user.main_character_id -> app.character via
	// fk_user_main_character, added in 00004_platform_identity.sql; and
	// app.character.user_id -> app.user). Neither constraint is DEFERRABLE,
	// so the write order is not a matter of taste: this used to call
	// SetUserMainCharacter BEFORE UpsertCharacter, pointing the fresh
	// user's main_character_id at a character row that did not exist yet,
	// and every first-time login died with
	//   insert or update on table "user" violates foreign key constraint
	//   "fk_user_main_character" (SQLSTATE 23503).
	//
	// It survived from Phase 5 to Phase 15.1 undetected because
	// internal/sso's unit tests drive an in-memory fakeStore (sso_test.go)
	// that enforces no foreign keys, and no test had ever run the login
	// flow against real Postgres — Phase 15 registered /auth/callback but
	// passed a nil *sso.Flow, so the route 501'd and the path stayed
	// unreachable. internal/api/v1/auth_integration_test.go is the
	// regression cover.
	//
	// Correct order: create the character (its user_id FK is already
	// satisfiable), THEN point the user at it.
	if _, err := f.Store.UpsertCharacter(ctx, gen.UpsertCharacterParams{
		CharacterID: characterID,
		UserID:      uuid.NullUUID{UUID: user.UserID, Valid: true},
		Name:        claims.Name,
		OwnerHash:   claims.Owner,
	}); err != nil {
		return uuid.UUID{}, false, fmt.Errorf("sso: resolving user: upserting character: %w", err)
	}
	if err := f.Store.SetUserMainCharacter(ctx, user.UserID, &characterID); err != nil {
		return uuid.UUID{}, false, fmt.Errorf("sso: resolving user: setting main character: %w", err)
	}
	return user.UserID, true, nil
}

// persistToken envelope-seals the refresh token and ingests the granted
// scope set.
func (f *Flow) persistToken(ctx context.Context, characterID int64, tokenResp *TokenResponse, claims *jwks.Claims) error {
	sealed, err := crypto.Seal(f.Keyring, characterID, []byte(tokenResp.RefreshToken))
	if err != nil {
		return fmt.Errorf("sso: persisting token: sealing: %w", err)
	}

	var accessExpiresAt *time.Time
	if tokenResp.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		accessExpiresAt = &t
	}

	if err := f.Store.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID:     characterID,
		KeyVersion:      int32(sealed.KeyVersion),
		WrappedDek:      sealed.WrappedDEK,
		Nonce:           sealed.Nonce,
		Ciphertext:      sealed.Ciphertext,
		AccessExpiresAt: accessExpiresAt,
		OwnerHash:       claims.Owner,
	}); err != nil {
		return fmt.Errorf("sso: persisting token: upserting: %w", err)
	}

	if err := scopes.Ingest(ctx, f.Store, claims.Scopes); err != nil {
		return fmt.Errorf("sso: persisting token: ingesting scopes: %w", err)
	}
	if err := f.Store.ReplaceCharacterTokenScopes(ctx, characterID); err != nil {
		return fmt.Errorf("sso: persisting token: clearing old scopes: %w", err)
	}
	for _, s := range claims.Scopes {
		if err := f.Store.AddCharacterTokenScope(ctx, characterID, s); err != nil {
			return fmt.Errorf("sso: persisting token: recording scope: %w", err)
		}
	}
	return nil
}
