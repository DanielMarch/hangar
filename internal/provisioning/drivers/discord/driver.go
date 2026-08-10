package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ErrRoleHierarchyRefused is returned by Grant when the target role sits
// at or above the bot's own highest role position, or the target user is
// the guild owner — refused BEFORE any request reaches Discord (§9.3).
var ErrRoleHierarchyRefused = errors.New("discord: refused: role at or above bot's hierarchy position, or target is guild owner")

// Driver implements internal/provisioning.Driver against one Discord
// guild. remoteIdentity is the linked Discord user id
// (app.provisioning_state.remote_identity); groupRef is the Discord role
// id (app.platform_group.remote_ref).
type Driver struct {
	Client    *Client
	GuildID   string
	Hierarchy *HierarchyGuard
}

// NewDriver constructs a Driver, wiring a Client and HierarchyGuard from
// cfg. budget is shared with every other Discord driver instance in the
// process (there is exactly one guild per HANGAR installation currently —
// HANGAR_DISCORD_GUILD_ID is singular — but the budget is intentionally a
// separate object so a future multi-guild driver doesn't have to
// re-architect this).
func NewDriver(cfg Config, budget *InvalidBudget, clock Clock) (*Driver, error) {
	client, err := NewClient(cfg, budget, clock, nil)
	if err != nil {
		return nil, err
	}
	return &Driver{
		Client:    client,
		GuildID:   cfg.GuildID,
		Hierarchy: NewHierarchyGuard(client, cfg.GuildID, clock),
	}, nil
}

// Grant adds remoteIdentity to the Discord role groupRef — implements
// provisioning.Driver. Idempotent: PUT .../roles/{role} succeeds whether
// or not the member already holds the role. Refuses (ErrRoleHierarchyRefused)
// without issuing the request if the hierarchy guard disallows it, and
// treats a 404 (member left the guild) as a successful no-op — reconciled
// state, not an error (§9.3 edge case).
func (d *Driver) Grant(ctx context.Context, remoteIdentity, groupRef string) error {
	allowed, err := d.Hierarchy.Allowed(ctx, remoteIdentity, groupRef)
	if err != nil {
		return fmt.Errorf("discord: grant: checking role hierarchy: %w", err)
	}
	if !allowed {
		return ErrRoleHierarchyRefused
	}

	route := "PUT /guilds/{guild}/members/{user}/roles/{role}"
	path := "/guilds/" + d.GuildID + "/members/" + remoteIdentity + "/roles/" + groupRef
	result, err := d.Client.Do(ctx, http.MethodPut, route, path, nil)
	if err != nil {
		return fmt.Errorf("discord: grant: %w", err)
	}
	return d.interpretRoleOperation(result)
}

// Revoke removes remoteIdentity from the Discord role groupRef —
// implements provisioning.Driver. No hierarchy pre-check: §9.3 only calls
// out proactively refusing an ASSIGNMENT; a revoke of a role the bot could
// never have assigned in the first place is not a HANGAR-initiated risk in
// the same way, and Discord itself will 403 a revoke the bot genuinely
// cannot perform.
func (d *Driver) Revoke(ctx context.Context, remoteIdentity, groupRef string) error {
	route := "DELETE /guilds/{guild}/members/{user}/roles/{role}"
	path := "/guilds/" + d.GuildID + "/members/" + remoteIdentity + "/roles/" + groupRef
	result, err := d.Client.Do(ctx, http.MethodDelete, route, path, nil)
	if err != nil {
		return fmt.Errorf("discord: revoke: %w", err)
	}
	return d.interpretRoleOperation(result)
}

// interpretRoleOperation turns one Grant/Revoke HTTP outcome into
// driver.Driver's error contract: 2XX and 404 (member left the guild —
// reconciled state, not an error) are both success; anything else is a
// reported failure, and a 403 additionally invalidates the hierarchy
// cache (§9.3: "invalidating on 403").
func (d *Driver) interpretRoleOperation(result Result) error {
	switch {
	case result.StatusCode >= 200 && result.StatusCode < 300:
		return nil
	case result.StatusCode == http.StatusNotFound:
		return nil
	case result.StatusCode == http.StatusForbidden:
		d.Hierarchy.InvalidateCache()
		return fmt.Errorf("discord: role operation forbidden (status 403): %s", string(result.Body))
	default:
		return fmt.Errorf("discord: role operation failed (status %d): %s", result.StatusCode, string(result.Body))
	}
}
