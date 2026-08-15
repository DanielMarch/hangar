package main

// world.go builds the installation Gate 1 runs against: N characters, each
// with a valid token and the full granted scope set, and the sync
// subscriptions those imply.
//
// ── WHY THE CHARACTERS ARE SEEDED RATHER THAN AUTHORISED ─────────────────
// 04_RELEASE_GATES.md §1.1 specifies 5000 characters. Authorising 5000
// characters through EVE SSO is not possible and would not be a better
// test: what the gate measures is the rate-limit ledger's behaviour under a
// realistic route mix at scale, and a token's provenance is not part of
// that. What IS part of it is that the route mix comes from
// app.sync_subscription at steady state (§1.1) — so the subscriptions are
// created by the REAL reconciler (internal/sync/subscribe.All) against the
// REAL catalogue, not by a hand-written list of paths that would drift from
// what an installation actually polls.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/subscribe"
)

// anchorCharacterID is the first character seeded, and anchorRefreshToken
// is the fixed refresh token it is given.
//
// Everything else gets a random one. This character is the exception
// because §1.3's entity-breaker condition — "5 consecutive 403s on ONE
// entity" — has to name the entity it targets, and the proxy identifies a
// caller by the bearer token it sees. A fixed refresh token here makes that
// caller's access token computable before the run starts (main.go's
// gate1InjectedToken), which is what lets the injection be aimed at one
// character while its 4999 siblings carry on — the sibling traffic being
// exactly what condition 1.5 ("failure stayed scoped") measures against.
const (
	anchorCharacterID  = int64(2_100_000_000)
	anchorRefreshToken = "gate1-refresh-anchor"
)

// seedResult records what the world ended up containing, for
// environment.json — a Gate 1 result is not interpretable without knowing
// how many characters and subscriptions were behind it.
type seedResult struct {
	Characters    int   `json:"characters"`
	Scopes        int   `json:"scopes_granted_per_character"`
	Subscriptions int64 `json:"sync_subscriptions"`
}

// seedWorld creates `characters` characters with valid tokens and every
// scope the catalogue's GET routes require, then runs the real subscription
// reconciler.
func seedWorld(ctx context.Context, pool *pgxpool.Pool, kr *crypto.Keyring, characters int) (seedResult, error) {
	var out seedResult
	s := store.New(pool)

	// Every scope any catalogue route requires. Granting the full set is
	// deliberate: the reconciler's scope gate (see
	// ReconcileCharacterSubscriptions) would otherwise silently drop routes
	// from the subscription set, and the gate would then run a narrower
	// route mix than §1.1 asks for without saying so.
	scopes, err := catalogueScopes(ctx, pool)
	if err != nil {
		return out, err
	}
	if len(scopes) == 0 {
		return out, fmt.Errorf("gate1: the route catalogue declares no scopes — was it ingested?")
	}
	out.Scopes = len(scopes)

	for i := 0; i < characters; i++ {
		characterID := anchorCharacterID + int64(i)
		if err := seedCharacter(ctx, pool, s, kr, characterID, scopes); err != nil {
			return out, fmt.Errorf("gate1: seeding character %d: %w", characterID, err)
		}
		if (i+1)%500 == 0 {
			fmt.Printf("gate1: seeded %d/%d characters\n", i+1, characters)
		}
	}
	out.Characters = characters

	result, err := subscribe.All(ctx, s)
	if err != nil {
		return out, fmt.Errorf("gate1: reconciling subscriptions: %w", err)
	}
	fmt.Printf("gate1: subscriptions created: %d character, %d corporation, %d global\n",
		result.CharacterCreated, result.CorporationCreated, result.GlobalCreated)

	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM app.sync_subscription WHERE enabled`).Scan(&out.Subscriptions); err != nil {
		return out, fmt.Errorf("gate1: counting subscriptions: %w", err)
	}
	return out, nil
}

func seedCharacter(ctx context.Context, pool *pgxpool.Pool, s *store.Store, kr *crypto.Keyring, characterID int64, scopes []string) error {
	ownerHash := fmt.Sprintf("gate1-owner-%d", characterID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO app.character (character_id, name, owner_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (character_id) DO NOTHING`,
		characterID, fmt.Sprintf("Gate1 Pilot %d", characterID), ownerHash); err != nil {
		return fmt.Errorf("inserting character: %w", err)
	}

	// A real sealed refresh token. The value is never presented to anything
	// that validates it — the runner's stub SSO endpoint exchanges whatever
	// it is handed — but it is sealed with the installation's own keyring so
	// the refresher's decrypt path runs for real on every request rather
	// than being bypassed.
	//
	// It must be UNIQUE PER CHARACTER, because the stub derives each
	// character's access token from it and the proxy partitions its
	// rate-limit buckets by the bearer token it sees. Two characters sharing
	// a refresh token would share a bucket on the server's side while HANGAR
	// counted two, which the proxy would correctly report as an overdraw.
	refreshToken := anchorRefreshToken
	if characterID != anchorCharacterID {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return fmt.Errorf("generating refresh token: %w", err)
		}
		refreshToken = "gate1-refresh-" + hex.EncodeToString(secret)
	}
	sealed, err := crypto.Seal(kr, characterID, []byte(refreshToken))
	if err != nil {
		return fmt.Errorf("sealing refresh token: %w", err)
	}
	expires := time.Now().Add(20 * time.Minute)
	if err := s.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID:     characterID,
		KeyVersion:      int32(sealed.KeyVersion),
		WrappedDek:      sealed.WrappedDEK,
		Nonce:           sealed.Nonce,
		Ciphertext:      sealed.Ciphertext,
		AccessExpiresAt: &expires,
		OwnerHash:       ownerHash,
	}); err != nil {
		return fmt.Errorf("upserting token: %w", err)
	}

	for _, scope := range scopes {
		if _, err := pool.Exec(ctx, `
			INSERT INTO app.character_token_scope (character_id, scope) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, characterID, scope); err != nil {
			return fmt.Errorf("granting scope %s: %w", scope, err)
		}
	}
	return nil
}

// catalogueScopes reads every scope the ingested catalogue references. It
// comes from app.esi_scope rather than from a list in this file for the
// same reason the proxy's rate limits come from the spec: a second
// hand-maintained copy would diverge, and the gate would then be measuring
// a route mix that no installation has.
func catalogueScopes(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT scope FROM app.esi_route_scope ORDER BY scope`)
	if err != nil {
		return nil, fmt.Errorf("gate1: listing catalogue scopes: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("gate1: scanning scope: %w", err)
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}
