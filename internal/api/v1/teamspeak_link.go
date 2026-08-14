// teamspeak_link.go mounts §9.4's single-use challenge flow — defect B35.
//
// ── WHAT WAS WRONG ───────────────────────────────────────────────────────
// internal/provisioning/drivers/teamspeak had IssueChallenge,
// NewChallengeToken and RedeemChallenge fully written and integration-
// tested against a real Postgres since Phase 13, and the generated
// GetTeamspeakChallenge alongside them, with no caller anywhere outside
// those tests. app.teamspeak_challenge could therefore only ever be
// written by hand, which means TeamSpeak identity linking could not be
// completed on a running installation at all: a TS3 platform could hold
// groups, rules and an entitlement set, and no user could ever be bound to
// a client UID for any of it to apply to.
//
// ── THE FLOW, AND ITS ONE DELIBERATE LIMITATION ──────────────────────────
// 01_ARCHITECTURE.md §9.4: "HANGAR issues a token, the user presents it
// in-client, HANGAR observes the redemption and binds the UID."
//
//  1. the authenticated user calls POST /api/v1/me/teamspeak/challenge and
//     gets a short base32 token with an expiry;
//  2. they present it in-client (typically as their nickname);
//  3. the TS3-side observer — a bot, a ServerQuery integration, or an
//     operator with provisioning.platforms.manage — calls
//     POST /api/v1/admin/platforms/{id}/teamspeak/redeem with the token and
//     the client_unique_identifier it observed, and HANGAR binds them.
//
// Step 3 is a PRIVILEGED endpoint rather than one the user calls with
// their own UID, and that is the security-relevant part of the design: a
// user asserting "my TS3 UID is X" proves nothing, and would let anyone
// bind any UID — including one already used by somebody else — to
// themselves. The observation has to come from the side that can actually
// see the client.
//
// NOT IMPLEMENTED, and recorded rather than hidden: HANGAR does not poll
// TS3's own `clientlist` to observe the redemption by itself. The WebQuery
// client (drivers/teamspeak/webquery.go) has the generic Do() a clientlist
// call would need, but nothing constructs a TS3 client in the API process,
// and adding a polling loop is a background subsystem rather than a route.
// The endpoint below is the seam that loop would call when it exists; a
// deployment today uses a bot or an operator, which is a supported §9.4
// topology and is what makes linking completable now.
package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/teamspeak"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ChallengeTTL is how long an issued TS3 challenge stays redeemable.
//
// Long enough to alt-tab into TeamSpeak, change a nickname and get noticed;
// short enough that a token shoulder-surfed off somebody's screen is not
// usable an hour later. It is not configurable because there is no
// deployment-shaped reason for it to differ — it is a human-interaction
// window, not a capacity or topology parameter.
const ChallengeTTL = 15 * time.Minute

func registerTeamspeakLink(hapi huma.API, deps api.Deps) {
	mutate[EmptyIn, TeamspeakChallengeOut](hapi, deps, http.MethodPost,
		"", "/api/v1/me/teamspeak/challenge", "issue-teamspeak-challenge",
		"Issue a single-use TeamSpeak identity-linking token for the caller", authTag,
		issueTeamspeakChallengeHandler(deps))

	// The caller's own view of a token they hold: has it been redeemed
	// yet? Without this the flow has no completion signal — the user
	// presents a token in TeamSpeak and the browser has no way to find out
	// whether anything happened, so the screen either lies ("linked!")
	// or says nothing at all.
	//
	// Scoped to the holder: the row is fetched by token and then checked
	// against the caller's user id, so possessing a token is not by itself
	// enough to read its state.
	get[TeamspeakChallengeIn, ItemOut](hapi, deps,
		"", "/api/v1/me/teamspeak/challenge/{token}", "get-teamspeak-challenge",
		"Status of one of the caller's TeamSpeak linking tokens", authTag,
		getTeamspeakChallengeHandler(deps))

	mutate[TeamspeakRedeemIn, ItemOut](hapi, deps, http.MethodPost,
		"provisioning.platforms.manage", "/api/v1/admin/platforms/{id}/teamspeak/redeem", "redeem-teamspeak-challenge",
		"Bind an observed TeamSpeak client UID to the user who holds this challenge token", adminTag,
		redeemTeamspeakChallengeHandler(deps))
}

// ---- shapes ----

type TeamspeakChallengeOut struct {
	Body struct {
		Token     string    `json:"token" doc:"Present this in TeamSpeak — typically as your nickname — then have it redeemed."`
		ExpiresAt time.Time `json:"expires_at"`
	}
}

type TeamspeakChallengeIn struct {
	Token string `path:"token"`
}

type TeamspeakRedeemIn struct {
	ID   string `path:"id" format:"uuid" doc:"TeamSpeak platform id."`
	Body struct {
		Token                  string `json:"token"`
		ClientUniqueIdentifier string `json:"client_unique_identifier" doc:"The base64 TS3 client UID observed in-client."`
	}
}

// ---- handlers ----

func issueTeamspeakChallengeHandler(deps api.Deps) func(context.Context, *EmptyIn) (*TeamspeakChallengeOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*TeamspeakChallengeOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		row, err := teamspeak.IssueChallenge(ctx, deps.Store, userID, ChallengeTTL)
		if err != nil {
			return nil, api.Internal("issuing teamspeak challenge", err)
		}
		out := &TeamspeakChallengeOut{}
		out.Body.Token = row.Token
		out.Body.ExpiresAt = row.ExpiresAt
		return out, nil
	}
}

// getTeamspeakChallengeHandler answers "has my token been redeemed yet?".
//
// A token the caller does not own answers 404, identically to a token that
// does not exist. Distinguishing them would turn this into an oracle for
// which tokens are outstanding, and the token IS the credential in this
// flow.
func getTeamspeakChallengeHandler(deps api.Deps) func(context.Context, *TeamspeakChallengeIn) (*ItemOut, error) {
	return func(ctx context.Context, in *TeamspeakChallengeIn) (*ItemOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		row, err := deps.Store.GetTeamspeakChallenge(ctx, in.Token)
		if err != nil || row.UserID != userID {
			return nil, api.NotFound("teamspeak challenge")
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// redeemTeamspeakChallengeHandler binds an observed client UID to the user
// who holds the token, and creates their provisioning_state row on the
// platform so the entitlement engine has something to act on.
//
// The link is written with EMPTY desired/actual group sets rather than a
// computed entitlement set. Linking is not the moment to decide what
// somebody should have — that is entitlement.Evaluate's job, and it runs
// on the reconcile that follows. Writing a guess here would mean two
// places computing entitlements, which is how they come to disagree.
func redeemTeamspeakChallengeHandler(deps api.Deps) func(context.Context, *TeamspeakRedeemIn) (*ItemOut, error) {
	return func(ctx context.Context, in *TeamspeakRedeemIn) (*ItemOut, error) {
		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		if in.Body.Token == "" || in.Body.ClientUniqueIdentifier == "" {
			return nil, huma.Error422UnprocessableEntity("token and client_unique_identifier are both required")
		}
		if _, err := deps.Store.GetPlatform(ctx, platformID); err != nil {
			return nil, api.NotFound("platform")
		}

		// One error for all three of "already consumed", "expired" and
		// "never issued" — challenge.go's own contract, so a caller
		// cannot probe which tokens exist. 409 rather than 404 because
		// the distinction between "no such token" and "that token is
		// spent" is exactly what must not be observable.
		challenge, err := teamspeak.RedeemChallenge(ctx, deps.Store, in.Body.Token, in.Body.ClientUniqueIdentifier)
		if err != nil {
			if errors.Is(err, teamspeak.ErrChallengeAlreadyConsumed) {
				return nil, huma.Error409Conflict("challenge token is already consumed, expired, or unknown")
			}
			return nil, api.Internal("redeeming teamspeak challenge", err)
		}

		if err := linkTeamspeakIdentity(ctx, deps, platformID, challenge); err != nil {
			return nil, err
		}
		auditAdminAction(ctx, deps, "admin.provisioning.teamspeak_linked", challenge.UserID.String())

		data := rowOf(challenge)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// linkTeamspeakIdentity writes the provisioning_state row, preserving the
// groups already recorded if the user was linked before (a re-link after
// changing TS3 identity must not silently look like a mass revocation to
// the next reconcile).
func linkTeamspeakIdentity(ctx context.Context, deps api.Deps, platformID uuid.UUID, challenge gen.AppTeamspeakChallenge) error {
	desired, actual := []string{}, []string{}
	if existing, err := deps.Store.GetProvisioningState(ctx, platformID, challenge.UserID); err == nil {
		desired, actual = existing.DesiredGroups, existing.ActualGroups
	}

	now := time.Now()
	if err := deps.Store.UpsertProvisioningState(ctx, gen.UpsertProvisioningStateParams{
		PlatformID:     platformID,
		UserID:         challenge.UserID,
		RemoteIdentity: challenge.ClientUniqueIdentifier,
		ChallengeToken: &challenge.Token,
		DesiredGroups:  desired,
		ActualGroups:   actual,
		LinkedAt:       &now,
	}); err != nil {
		return api.Internal("linking teamspeak identity", err)
	}
	return nil
}
