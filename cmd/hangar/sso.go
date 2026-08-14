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
	"log/slog"
	"sort"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/scopes"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/sso/jwks"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/sync/subscribe"
	"github.com/hangar-project/hangar/internal/sync/worker"
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
func buildSSOFlow(
	ctx context.Context,
	cfg *config.Config,
	pool store.Pool,
	s *store.Store,
	keyring *crypto.Keyring,
	lifecycle *sso.Lifecycle,
	urgent *provisioning.Urgent,
	logger *slog.Logger,
) (*sso.Flow, error) {
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
		// Phase 20.2, defect B37.
		LoginScopes: loginScopeResolver(s, cfg.SSO.Scopes, logger),
		// Phase 20.3, defect B27. §7.2's transfer edge case, on the one
		// path a transferred character actually takes.
		//
		// This hook has existed since Phase 5 and has never been set by any
		// process. Its own doc comment claimed "lifecycle.go wires the real
		// invalidation regardless of whether this hook is set" — which was
		// true of nothing: internal/sso.Lifecycle had no production caller
		// either, so a character transferred to another EVE account kept
		// every HANGAR entitlement its previous owner had earned, forever,
		// and logging in as the new owner did not disturb that.
		//
		// It fires from resolveUser, BEFORE persistToken writes the new
		// owner's token — so the invalidation lands on the OLD token and
		// the fresh authorization that follows is stored valid, which is
		// exactly §7.2's intent. A failure is logged, never returned: the
		// login has succeeded by then and refusing it would leave the new
		// owner locked out of an account they legitimately hold.
		OnOwnerHashChanged: func(ctx context.Context, characterID int64) {
			logger.WarnContext(ctx,
				"sso: this character's owner hash changed — it has been transferred to a different EVE "+
					"account, so every stored token for it is being invalidated (§7.2)",
				"character_id", characterID)
			if err := lifecycle.InvalidateForOwnerHashChange(ctx, characterID); err != nil {
				logger.ErrorContext(ctx,
					"sso: invalidating tokens after an owner hash change FAILED — a transferred character "+
						"may still hold its previous owner's entitlements",
					"character_id", characterID, "error", err)
			}
		},
		// Phase 20.3, defect B27 — Gate 2 trigger row 3. See
		// internal/sso.Flow.OnScopesReduced for why this is its own event
		// and not a token invalidation.
		OnScopesReduced: notifyScopesReduced(urgent, pool, logger),
		// Phase 20.2, defect B40: a freshly authenticated SSO user held ZERO
		// permissions, and no route existed by which anyone could grant them
		// any. BootstrapFirstAdmin promotes this user to the seeded `admin`
		// role IF AND ONLY IF the installation currently has nobody holding
		// superuser — see internal/rbac/bootstrap.go for why that condition
		// and not "is this the first user row".
		//
		// Failure is logged, never returned, for the same reason as the
		// subscription hook below: the login has succeeded, and refusing it
		// over a role assignment leaves the operator with neither a session
		// nor a permission. The log line is loud because an installation
		// that reaches it is one nobody can administer.
		OnUserAuthenticated: func(ctx context.Context, userID uuid.UUID, isNewUser bool) {
			promoted, err := rbac.BootstrapFirstAdmin(ctx, pool, userID)
			if err != nil {
				logger.ErrorContext(ctx,
					"rbac: first-administrator bootstrap failed — if this installation has no "+
						"administrator, nobody can grant one through the API; "+
						"run 'hangar admin bootstrap-token' to recover",
					"user_id", userID, "new_user", isNewUser, "error", err)
				return
			}
			if promoted {
				logger.InfoContext(ctx,
					"rbac: this installation had no administrator, so the authenticating user has been "+
						"granted the seeded 'admin' role. Every subsequent login is unaffected.",
					"user_id", userID, "role", rbac.BootstrapRoleName)
			}
		},
		// Phase 20.1.1, defect B42: authorising a character schedules its
		// routes immediately rather than at the next reconcile tick.
		//
		// Failure is logged, never returned: the login has already
		// succeeded by this point and the periodic pass will pick the
		// subscriptions up regardless, so turning a delayed sync into a
		// failed login would be a strictly worse trade.
		OnTokenPersisted: func(ctx context.Context, characterID int64, scopes []string) {
			result, err := subscribe.ForCharacter(ctx, s, characterID)
			if err != nil {
				logger.ErrorContext(ctx,
					"sync: could not reconcile subscriptions for a newly authorised character — "+
						"it will be picked up by the periodic pass",
					"character_id", characterID, "scopes", len(scopes), "error", err)
				return
			}
			result.Log(ctx, logger, "login")
		},
	}, nil
}

// loginScopeResolver answers "which scopes should this login request?" from
// the route catalogue, falling back to the embedded spec snapshot.
//
// The fallback is not belt-and-braces. `serve` ingests the catalogue in the
// BACKGROUND at startup (see serve.go), so on a fresh installation there is
// a window — and on an installation that has never reached ESI, an
// indefinite one — in which app.esi_route is empty. Resolving to the empty
// set there would reproduce B37 precisely on the deployment least able to
// notice: first boot, first login, no refresh token, no error.
//
// Which source answered is logged at every login, because "why is HANGAR
// asking for a different scope set than yesterday" is a question an
// operator will eventually ask, and the answer is always one of these two.
func loginScopeResolver(s *store.Store, configured []string, logger *slog.Logger) func(context.Context) ([]string, error) {
	syncSet := worker.SyncSet()
	paths := make([]string, 0, len(syncSet))
	for path := range syncSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	return func(ctx context.Context) ([]string, error) {
		// HANGAR_SSO_SCOPES wins outright when set. The derived set is what
		// HANGAR NEEDS; the developer-portal registration is what EVE SSO
		// will GRANT, and HANGAR cannot read the latter. When they
		// disagree, SSO rejects the entire authorization with
		// `invalid_scope` — naming one scope, and only after the user has
		// entered their password and 2FA code. Without an override the
		// operator's only recourse is to edit Go source, so this exists.
		if len(configured) > 0 {
			logger.DebugContext(ctx, "sso: login scope set resolved",
				"source", "HANGAR_SSO_SCOPES", "scopes", len(configured))
			return configured, nil
		}
		fromCatalogue, err := s.ListScopesForRoutePaths(ctx, paths)
		if err != nil {
			return nil, fmt.Errorf("querying catalogue scopes: %w", err)
		}
		if len(fromCatalogue) > 0 {
			logger.DebugContext(ctx, "sso: login scope set resolved",
				"source", "catalogue", "scopes", len(fromCatalogue))
			return fromCatalogue, nil
		}

		specBytes, _, err := catalogue.LoadEmbeddedSnapshot()
		if err != nil {
			return nil, fmt.Errorf("catalogue is empty and the embedded snapshot is unreadable: %w", err)
		}
		fromSnapshot, missing, err := scopes.FromSpec(specBytes, paths)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			// Defect B38's detector. A sync-set path absent from the spec
			// contributes no scopes and would otherwise be invisible.
			logger.WarnContext(ctx,
				"sso: sync-set paths are absent from the spec — they can never return data "+
					"and contribute no scopes (Principle 5: upstream_path is verbatim, never derived)",
				"paths", missing)
		}
		logger.InfoContext(ctx, "sso: login scope set resolved from the EMBEDDED snapshot — "+
			"the route catalogue is empty; run 'hangar admin ingest-catalogue' once ESI is reachable",
			"source", "embedded-snapshot", "scopes", len(fromSnapshot))
		return fromSnapshot, nil
	}
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
