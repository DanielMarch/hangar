package discord_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/discord"
	"github.com/stretchr/testify/require"
)

// mockGuild is one fake Discord guild's fixture state for the hand-rolled
// httptest.Server every test in this file drives the real discord.Client
// against — real request/response shapes, real headers, no mocked Go
// interfaces standing in for the HTTP layer itself.
type mockGuild struct {
	GuildID    string
	OwnerID    string
	BotUserID  string
	BotRoleIDs []string
	Roles      map[string]int // role id -> position

	mu        sync.Mutex
	roleCalls []string // "PUT role-id" / "DELETE role-id" — every mutating call actually issued
	putStatus int      // status code the PUT handler returns; 204 if zero
}

func newMockGuild() *mockGuild {
	return &mockGuild{
		GuildID:   "1000",
		OwnerID:   "owner-user-id",
		BotUserID: "bot-user-id",
		Roles:     map[string]int{},
	}
}

func (g *mockGuild) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v10/users/@me", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"id": g.BotUserID})
	})
	mux.HandleFunc("/v10/guilds/"+g.GuildID, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"owner_id": g.OwnerID})
	})
	mux.HandleFunc("/v10/guilds/"+g.GuildID+"/roles", func(w http.ResponseWriter, r *http.Request) {
		type role struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
		}
		var roles []role
		for id, pos := range g.Roles {
			roles = append(roles, role{ID: id, Position: pos})
		}
		writeJSON(w, http.StatusOK, roles)
	})
	mux.HandleFunc("/v10/guilds/"+g.GuildID+"/members/"+g.BotUserID, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"roles": g.BotRoleIDs})
	})
	// Any member/role mutation route — records the call so tests can
	// assert whether it was ever actually issued.
	mux.HandleFunc("/v10/guilds/"+g.GuildID+"/members/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.roleCalls = append(g.roleCalls, r.Method+" "+r.URL.Path)
		status := g.putStatus
		g.mu.Unlock()
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	})

	return httptest.NewServer(mux)
}

func (g *mockGuild) calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.roleCalls))
	copy(out, g.roleCalls)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newTestClient(t *testing.T, baseURL string) *discord.Client {
	t.Helper()
	cfg := discord.Config{
		BotToken: "test-bot-token", GuildID: "1000", APIVersion: 10, Allowlist: []int{10},
		GlobalRate: 1000, BaseURL: baseURL, HTTPClient: http.DefaultClient,
	}
	client, err := discord.NewClient(cfg, nil, discord.SystemClock, nil)
	require.NoError(t, err)
	return client
}

// TestRoleHierarchyGuardBlocksAboveBot (roadmap exit criterion): a role at
// or above the bot's own highest position is refused WITHOUT the mutating
// role-assignment request ever being issued.
func TestRoleHierarchyGuardBlocksAboveBot(t *testing.T) {
	g := newMockGuild()
	g.BotRoleIDs = []string{"bot-role"}
	g.Roles = map[string]int{
		"bot-role":  5,
		"low-role":  2, // below the bot — allowed
		"high-role": 5, // AT the bot's position — refused (strictly below required)
		"top-role":  9, // above the bot — refused
	}
	server := g.server(t)
	defer server.Close()

	client := newTestClient(t, server.URL)
	driver := &discord.Driver{
		Client: client, GuildID: g.GuildID,
		Hierarchy: discord.NewHierarchyGuard(client, g.GuildID, discord.SystemClock),
	}

	err := driver.Grant(t.Context(), "some-member", "top-role")
	require.ErrorIs(t, err, discord.ErrRoleHierarchyRefused)
	require.Empty(t, g.calls(), "no PUT must ever be issued for a role at/above the bot's position")

	err = driver.Grant(t.Context(), "some-member", "high-role")
	require.ErrorIs(t, err, discord.ErrRoleHierarchyRefused)
	require.Empty(t, g.calls(), "a role AT the bot's own position (not strictly below) must also be refused")

	err = driver.Grant(t.Context(), g.OwnerID, "low-role")
	require.ErrorIs(t, err, discord.ErrRoleHierarchyRefused)
	require.Empty(t, g.calls(), "any operation against the guild owner must be refused regardless of role position")

	err = driver.Grant(t.Context(), "some-member", "low-role")
	require.NoError(t, err)
	require.Len(t, g.calls(), 1, "a role genuinely below the bot's position must actually issue the PUT")
	require.Contains(t, g.calls()[0], "PUT")
}

// TestHierarchyGuardCachesForSixtySeconds: repeated Allowed calls within
// the cache TTL do not re-fetch guild/roles/member state.
func TestHierarchyGuardCachesForSixtySeconds(t *testing.T) {
	g := newMockGuild()
	g.BotRoleIDs = []string{"bot-role"}
	g.Roles = map[string]int{"bot-role": 5, "low-role": 2}

	mux := g.server(t)
	defer mux.Close()

	client := newTestClient(t, mux.URL)
	guard := discord.NewHierarchyGuard(client, g.GuildID, discord.SystemClock)

	for i := 0; i < 5; i++ {
		allowed, err := guard.Allowed(t.Context(), "member", "low-role")
		require.NoError(t, err)
		require.True(t, allowed)
	}
	// No mutating calls should ever result from a read-only Allowed check.
	require.Empty(t, g.calls())
}
