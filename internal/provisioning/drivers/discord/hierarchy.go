package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// HierarchyGuard proactively refuses any role assignment at or above the
// bot's own highest role position, and any operation targeting the guild
// owner (01_ARCHITECTURE.md §9.3) — checked BEFORE driver.go issues the
// actual role-assignment request, since attempting and failing burns the
// invalid-request budget that guard exists to protect.
type HierarchyGuard struct {
	client  *Client
	guildID string
	clock   Clock

	mu            sync.Mutex
	cachedAt      time.Time
	botUserID     string // fetched once via /users/@me — never expires, the bot's own id doesn't change
	ownerID       string
	rolePositions map[string]int // role id -> position
	botPosition   int            // the bot's own highest role position
}

// NewHierarchyGuard constructs a guard for one guild.
func NewHierarchyGuard(client *Client, guildID string, clock Clock) *HierarchyGuard {
	if clock == nil {
		clock = SystemClock
	}
	return &HierarchyGuard{client: client, guildID: guildID, clock: clock}
}

type discordUser struct {
	ID string `json:"id"`
}

type discordGuild struct {
	OwnerID string `json:"owner_id"`
}

type discordRole struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
}

type discordMember struct {
	Roles []string `json:"roles"`
}

// Allowed reports whether the bot may assign roleID to targetUserID:
// false whenever targetUserID is the guild owner, or roleID's position is
// at or above the bot's own highest role position. An unrecognised roleID
// (not present in the guild's role list at all) is refused conservatively
// — an operation HANGAR cannot verify as safe is not one it performs.
func (h *HierarchyGuard) Allowed(ctx context.Context, targetUserID, roleID string) (bool, error) {
	if err := h.ensureFresh(ctx); err != nil {
		return false, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if targetUserID == h.ownerID {
		return false, nil
	}
	position, known := h.rolePositions[roleID]
	if !known {
		return false, nil
	}
	return position < h.botPosition, nil
}

// InvalidateCache forces the next Allowed call to re-fetch — §9.3: "Cache
// the bot member and guild role positions for 60 s, invalidating on 403."
// driver.go calls this when a role operation itself comes back 403, since
// that's the live signal the cached hierarchy snapshot might be stale
// (e.g. the bot's own roles changed since the last Refresh).
func (h *HierarchyGuard) InvalidateCache() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cachedAt = time.Time{}
}

func (h *HierarchyGuard) ensureFresh(ctx context.Context) error {
	h.mu.Lock()
	stale := h.cachedAt.IsZero() || h.clock.Now().Sub(h.cachedAt) >= HierarchyCacheTTL
	h.mu.Unlock()
	if !stale {
		return nil
	}
	return h.refresh(ctx)
}

// refresh re-reads the bot's own id (once, cached forever — it cannot
// change), the guild's owner, every role's position, and the bot's own
// role set, then computes the bot's highest position.
func (h *HierarchyGuard) refresh(ctx context.Context) error {
	h.mu.Lock()
	botUserID := h.botUserID
	h.mu.Unlock()

	if botUserID == "" {
		result, err := h.client.Do(ctx, http.MethodGet, "GET /users/@me", "/users/@me", nil)
		if err != nil {
			return fmt.Errorf("discord: hierarchy: fetching bot user: %w", err)
		}
		if result.StatusCode != http.StatusOK {
			return fmt.Errorf("discord: hierarchy: fetching bot user: unexpected status %d", result.StatusCode)
		}
		var user discordUser
		if err := json.Unmarshal(result.Body, &user); err != nil {
			return fmt.Errorf("discord: hierarchy: decoding bot user: %w", err)
		}
		botUserID = user.ID
	}

	guildResult, err := h.client.Do(ctx, http.MethodGet, "GET /guilds/{guild}", "/guilds/"+h.guildID, nil)
	if err != nil {
		return fmt.Errorf("discord: hierarchy: fetching guild: %w", err)
	}
	if guildResult.StatusCode == http.StatusForbidden {
		h.InvalidateCache()
	}
	if guildResult.StatusCode != http.StatusOK {
		return fmt.Errorf("discord: hierarchy: fetching guild: unexpected status %d", guildResult.StatusCode)
	}
	var guild discordGuild
	if err := json.Unmarshal(guildResult.Body, &guild); err != nil {
		return fmt.Errorf("discord: hierarchy: decoding guild: %w", err)
	}

	rolesResult, err := h.client.Do(ctx, http.MethodGet, "GET /guilds/{guild}/roles", "/guilds/"+h.guildID+"/roles", nil)
	if err != nil {
		return fmt.Errorf("discord: hierarchy: fetching roles: %w", err)
	}
	if rolesResult.StatusCode == http.StatusForbidden {
		h.InvalidateCache()
	}
	if rolesResult.StatusCode != http.StatusOK {
		return fmt.Errorf("discord: hierarchy: fetching roles: unexpected status %d", rolesResult.StatusCode)
	}
	var roles []discordRole
	if err := json.Unmarshal(rolesResult.Body, &roles); err != nil {
		return fmt.Errorf("discord: hierarchy: decoding roles: %w", err)
	}
	positions := make(map[string]int, len(roles))
	for _, r := range roles {
		positions[r.ID] = r.Position
	}

	memberResult, err := h.client.Do(ctx, http.MethodGet, "GET /guilds/{guild}/members/{user}", "/guilds/"+h.guildID+"/members/"+botUserID, nil)
	if err != nil {
		return fmt.Errorf("discord: hierarchy: fetching bot member: %w", err)
	}
	if memberResult.StatusCode == http.StatusForbidden {
		h.InvalidateCache()
	}
	if memberResult.StatusCode != http.StatusOK {
		return fmt.Errorf("discord: hierarchy: fetching bot member: unexpected status %d", memberResult.StatusCode)
	}
	var member discordMember
	if err := json.Unmarshal(memberResult.Body, &member); err != nil {
		return fmt.Errorf("discord: hierarchy: decoding bot member: %w", err)
	}
	botPosition := 0
	for _, roleID := range member.Roles {
		if p, ok := positions[roleID]; ok && p > botPosition {
			botPosition = p
		}
	}

	h.mu.Lock()
	h.botUserID = botUserID
	h.ownerID = guild.OwnerID
	h.rolePositions = positions
	h.botPosition = botPosition
	h.cachedAt = h.clock.Now()
	h.mu.Unlock()
	return nil
}
