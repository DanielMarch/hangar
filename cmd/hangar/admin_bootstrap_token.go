package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/hangar-project/hangar/internal/domain"
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

			secret, err := bootstrapToken(cmd.Context(), gen.New(pool), displayName, tokenName)
			if err != nil {
				return err
			}
			fmt.Println("admin bootstrap-token: a new admin user and API token have been created.")
			fmt.Println("This secret is shown exactly once — HANGAR stores only its hash and cannot recover it:")
			fmt.Println()
			fmt.Println(secret)
			fmt.Println()
			fmt.Println("Use it as a Bearer token, or Basic-auth style, against the HTTP API once Phase 15 lands.")
			return nil
		},
	}
	cmd.Flags().StringVar(&displayName, "user-name", "Bootstrap Admin", "display name for the created admin user")
	cmd.Flags().StringVar(&tokenName, "token-name", "bootstrap", "name recorded on the issued api_token row")
	return cmd
}

// bootstrapToken creates a new admin user and issues an API token carrying
// every known permission (internal/domain.Permissions — RBAC enforcement
// itself lands in Phase 10; a bootstrap token needs unrestricted access
// from the moment it's minted, since it exists to configure everything
// else). Returns the raw, never-stored secret in "token_id.secret" form.
func bootstrapToken(ctx context.Context, q *gen.Queries, displayName, tokenName string) (string, error) {
	user, err := q.CreateUser(ctx, displayName)
	if err != nil {
		return "", fmt.Errorf("admin bootstrap-token: creating user: %w", err)
	}
	if err := q.SetUserAdmin(ctx, user.UserID, true); err != nil {
		return "", fmt.Errorf("admin bootstrap-token: granting admin: %w", err)
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

	token, err := q.CreateApiToken(ctx, gen.CreateApiTokenParams{
		UserID: user.UserID, Name: tokenName, HashedSecret: hash[:], Permissions: permissions,
	})
	if err != nil {
		return "", fmt.Errorf("admin bootstrap-token: issuing token: %w", err)
	}

	return token.TokenID.String() + "." + secretEncoded, nil
}
