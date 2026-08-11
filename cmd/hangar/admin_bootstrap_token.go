package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// newAdminBootstrapTokenCmd issues a working admin API token before any
// user has ever completed SSO — the only way into a fresh installation.
// It prints the raw secret exactly once; HANGAR never stores it, only its
// SHA-256 hash (the same shape app.api_token.hashed_secret already
// expects for every third-party token, §12).
func newAdminBootstrapTokenCmd() *cobra.Command {
	var displayName, tokenName string
	cmd := &cobra.Command{
		Use:   "bootstrap-token",
		Short: "Issue a working admin API token for a fresh installation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			pool, err := pgxpool.New(cmd.Context(), cfg.DB.URL.Reveal())
			if err != nil {
				return fmt.Errorf("admin bootstrap-token: connecting to database: %w", err)
			}
			defer pool.Close()

			secret, err := bootstrapToken(cmd.Context(), store.New(pool), displayName, tokenName)
			if err != nil {
				return err
			}
			fmt.Println("admin bootstrap-token: a new admin user and API token have been created.")
			fmt.Println("This secret is shown exactly once — HANGAR stores only its hash and cannot recover it:")
			fmt.Println()
			fmt.Println(secret)
			fmt.Println()
			// PHASE 18: this line used to promise Bearer-token access
			// "once Phase 15 lands". Phase 15 landed and wired the token
			// MANAGEMENT endpoints, but nothing authenticated a request by
			// token, so the secret this command prints was unusable for
			// anything. internal/api/middleware/apitoken.go closes that;
			// the instruction below is now true, and exercised by
			// TestBootstrapTokenAuthenticatesAgainstTheAPI.
			fmt.Println("Use it as a Bearer token against the HTTP API:")
			fmt.Println()
			fmt.Println("    curl -H \"Authorization: Bearer " + secret + "\" <public-url>/api/v1/me")
			return nil
		},
	}
	cmd.Flags().StringVar(&displayName, "user-name", "Bootstrap Admin", "display name for the created admin user")
	cmd.Flags().StringVar(&tokenName, "token-name", "bootstrap", "name recorded on the issued api_token row")
	return cmd
}

// bootstrapToken creates a new admin user and issues an API token carrying
// every known permission (internal/domain.Permissions — a bootstrap token
// needs unrestricted access from the moment it's minted, since it exists
// to configure everything else). Returns the raw, never-stored secret in
// "token_id.secret" form.
//
// PHASE 18 CLOSE-OUT, second half of the B21 defect. Minting the token was
// never enough on its own. `app.api_token.permissions` is a CAP applied on
// top of the owner's RBAC (internal/api/middleware/apitoken.go), and this
// command created a user with `is_admin = true` but NO roles — while
// RequirePermission reads only app.effective_permission, which
// `is_admin` does not populate and nothing else was going to populate
// either. So even once bearer authentication existed, the bootstrap token
// authenticated successfully and then 403'd on every guarded route: it
// could read /api/v1/me and nothing else, which is the exact opposite of
// "unrestricted access from the moment it's minted".
//
// The user is therefore also given the seeded `admin` role, that role is
// granted `superuser` if it has no grants yet, and the user's effective
// permissions are materialised. db/seed/roles.sql ships `admin`
// deliberately EMPTY of grants — "safer than shipping a silently
// over-privileged default" — and names an operator action as what fills
// it. This is that action: explicit, audited by its own output, and taken
// only when someone runs this command.
func bootstrapToken(ctx context.Context, s *store.Store, displayName, tokenName string) (string, error) {
	user, err := s.CreateUser(ctx, displayName)
	if err != nil {
		return "", fmt.Errorf("admin bootstrap-token: creating user: %w", err)
	}
	if err := s.SetUserAdmin(ctx, user.UserID, true); err != nil {
		return "", fmt.Errorf("admin bootstrap-token: granting admin: %w", err)
	}
	if err := grantBootstrapRBAC(ctx, s, user.UserID); err != nil {
		return "", err
	}

	rawSecret := make([]byte, 32)
	if _, err := rand.Read(rawSecret); err != nil {
		return "", fmt.Errorf("admin bootstrap-token: generating secret: %w", err)
	}
	secretEncoded := base64.RawURLEncoding.EncodeToString(rawSecret)
	hash := sha256.Sum256(rawSecret)

	permissions := make([]string, len(domain.Permissions))
	for i, p := range domain.Permissions {
		permissions[i] = p.Name
	}

	token, err := s.CreateApiToken(ctx, gen.CreateApiTokenParams{
		UserID: user.UserID, Name: tokenName, HashedSecret: hash[:], Permissions: permissions,
	})
	if err != nil {
		return "", fmt.Errorf("admin bootstrap-token: issuing token: %w", err)
	}

	return token.TokenID.String() + "." + secretEncoded, nil
}

// grantBootstrapRBAC gives userID the seeded `admin` role, ensures that
// role actually grants something, and materialises the result. See
// bootstrapToken's doc comment for why this is not optional.
func grantBootstrapRBAC(ctx context.Context, s *store.Store, userID uuid.UUID) error {
	// Located by name off ListRoles rather than through a new
	// GetRoleByName query: the role set is a handful of seeded rows, and
	// adding a generated query for one lookup in one command is not worth
	// a schema-generation round trip.
	roles, err := s.ListRoles(ctx)
	if err != nil {
		return fmt.Errorf("admin bootstrap-token: listing roles: %w", err)
	}
	var role gen.AppRole
	for _, r := range roles {
		if r.Name == bootstrapRoleName {
			role = r
			break
		}
	}
	if role.RoleID == uuid.Nil {
		return fmt.Errorf("admin bootstrap-token: the seeded %q role is missing — run 'hangar migrate up' first", bootstrapRoleName)
	}

	// Only fill the role if it is still empty. An operator who has already
	// curated `admin`'s grants must not have them silently replaced by
	// superuser just because they minted another token.
	grants, err := s.ListRoleGrants(ctx, role.RoleID)
	if err != nil {
		return fmt.Errorf("admin bootstrap-token: reading %q role grants: %w", bootstrapRoleName, err)
	}
	if len(grants) == 0 {
		if _, err := s.AddRoleGrant(ctx, role.RoleID, domain.SuperuserPermission, "allow"); err != nil {
			return fmt.Errorf("admin bootstrap-token: granting %s to %q: %w", domain.SuperuserPermission, bootstrapRoleName, err)
		}
	}

	if err := s.AssignUserRole(ctx, userID, role.RoleID, uuid.NullUUID{}); err != nil {
		return fmt.Errorf("admin bootstrap-token: assigning the %q role: %w", bootstrapRoleName, err)
	}
	// Materialise: RequirePermission reads app.effective_permission and
	// never recomputes live, so a grant that has not been refreshed is a
	// grant that does not exist as far as every guarded route is concerned.
	if err := rbac.RefreshUser(ctx, s, userID); err != nil {
		return fmt.Errorf("admin bootstrap-token: materialising effective permissions: %w", err)
	}
	return nil
}

// bootstrapRoleName is the seeded role (db/seed/roles.sql) the bootstrap
// user is placed in.
const bootstrapRoleName = "admin"
