package main

// sso.go assembles internal/sso.Flow — the EVE SSO login round trip that
// serve.go mounts at /auth/login and /auth/callback.
//
// PHASE 15.1. Phase 15 registered those two routes but passed a nil
// *sso.Flow into v1.RegisterAuthRedirects, so both answered 501: the
// pieces existed (internal/sso's Flow/OAuthConfig since Phase 5,
// internal/sso/jwks's Cache/Verifier since Phase 5, internal/crypto's
// Keyring since Phase 5) but nothing had ever assembled them for the
// serving path — only cmd/hangar/gateway.go's buildRefresher, which needs
// the Keyring but not the Verifier. This file is that missing assembly,
// following buildGateway/buildRefresher's established shape.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/hangar-project/hangar/internal/store"
)

// jwksSettingStore adapts *store.Store to internal/sso/jwks.SettingStore.
//
// The two interfaces differ on exactly one method's return type:
// gen.Querier.GetSetting yields a full gen.AppSetting (key, value,
// updated_at, updated_by) where the JWKS cache only ever reads Value, and
// jwks declares its own SettingRow specifically so that package doesn't
// have to import internal/store/gen (see its doc comment). So this is a
// field projection, not a reimplementation — UpsertSetting's signature
// already matches exactly and forwards unchanged.
type jwksSettingStore struct{ store *store.Store }

func (a jwksSettingStore) GetSetting(ctx context.Context, key string) (jwks.SettingRow, error) {
	row, err := a.store.GetSetting(ctx, key)
	if err != nil {
		return jwks.SettingRow{}, err
	}
	return jwks.SettingRow{Value: row.Value}, nil
}

func (a jwksSettingStore) UpsertSetting(ctx context.Context, key string, value json.RawMessage, updatedBy uuid.NullUUID) error {
	return a.store.UpsertSetting(ctx, key, value, updatedBy)
}

// buildSSOFlow assembles the login Flow: a JWKS cache backed by
// app.setting for cold-boot persistence, an offline token verifier over
// it, and the OAuth client configuration.
//
// The cache is seeded with Load (app.setting, no network) and then
// Refresh (network). A Refresh failure is deliberately NOT fatal when
// Load already produced keys: §7.1's offline-boot contract is that a
// previously-persisted key set keeps token validation working through an
// SSO outage, and failing serve's startup because login.eveonline.com is
// briefly unreachable would take the whole installation down with it.
// Only a cold cache (no persisted keys AND no reachable JWKS endpoint) is
// fatal, because nothing could validate a token in that state anyway.
//
// No jwks.Clock implementation is passed: jwks.NewCache already defaults
// a nil clock to its own unexported systemClock (cache.go), so supplying
// one here would add a type for no behavioural difference.
func buildSSOFlow(ctx context.Context, cfg *config.Config, s *store.Store, keyring *crypto.Keyring) (*sso.Flow, error) {
	cache := jwks.NewCache(cfg.SSO.JWKSURL, jwksSettingStore{store: s}, nil, nil)

	loadErr := cache.Load(ctx)
	if refreshErr := cache.Refresh(ctx); refreshErr != nil {
		if loadErr != nil {
			return nil, fmt.Errorf("sso: cold JWKS cache: persisted load failed (%v) and refresh failed: %w", loadErr, refreshErr)
		}
	}

	verifier := jwks.NewVerifier(cache, jwks.VerifierConfig{
		Issuers:  cfg.SSO.Issuers,
		ClientID: cfg.SSO.ClientID,
		Audience: cfg.SSO.Audience,
	})

	return &sso.Flow{
		Store:    s,
		OAuth:    ssoOAuthConfig(cfg),
		Verifier: verifier,
		Keyring:  keyring,
		// Phase 15.1: config.CryptoConfig.SessionTTL finally has a
		// consumer — see db/queries/user.sql's CompleteSessionLogin note.
		SessionTTL: cfg.Crypto.SessionTTL,
	}, nil
}

// ssoOAuthConfig is the one place the config -> sso.OAuthConfig mapping
// lives. gateway.go's buildRefresher built the same literal inline; both
// now call this, so the two can never drift into disagreeing about which
// credentials or endpoints the installation uses.
func ssoOAuthConfig(cfg *config.Config) sso.OAuthConfig {
	return sso.OAuthConfig{
		ClientID:     cfg.SSO.ClientID,
		ClientSecret: cfg.SSO.ClientSecret.Reveal(),
		CallbackURL:  cfg.SSO.CallbackURL,
		AuthorizeURL: cfg.SSO.AuthorizeURL,
		TokenURL:     cfg.SSO.TokenURL,
	}
}
