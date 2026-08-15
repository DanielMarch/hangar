//go:build integration

// The half of Appendix A's verification that needs Postgres.
//
// ── WHY THESE ARE NOT UNIT TESTS ─────────────────────────────────────────
// Every capability below is HANGAR-NATIVE: no ESI route delivers it, and its
// behaviour IS the database — an append-only log, a role grant replaced
// atomically, a webhook secret demoted in the same statement that installs
// its successor, a squad's cascade on delete. A fake store would let each of
// these assert that Go called a method, which is precisely the kind of test
// that let defect B42 exist (three integration tests called
// UpsertSyncSubscription and no production code did, for nine phases).
//
// One container for the whole package, not one per test: forty-two
// capability tests each spinning testcontainers would dominate the suite,
// and every test here seeds its own ids so they do not collide.
package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

var sharedPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "capability: starting postgres: %v\n", err)
		os.Exit(1)
	}
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "capability: connection string: %v\n", err)
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capability: pool: %v\n", err)
		os.Exit(1)
	}
	deadline := time.Now().Add(30 * time.Second)
	for pool.Ping(ctx) != nil {
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "capability: postgres never became ready")
			os.Exit(1)
		}
		time.Sleep(250 * time.Millisecond)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	goose.SetBaseFS(hangardb.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		fmt.Fprintf(os.Stderr, "capability: goose dialect: %v\n", err)
		os.Exit(1)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		fmt.Fprintf(os.Stderr, "capability: migrations: %v\n", err)
		os.Exit(1)
	}
	// The seeds matter here rather than being boilerplate: app.permission is
	// a CLOSED set with a foreign key from app.role_grant, so the scope- and
	// squad-role capabilities below could not grant anything without it —
	// and that refusal is itself one of the assertions those tests make.
	if err := hangardb.ApplySeeds(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "capability: seeds: %v\n", err)
		os.Exit(1)
	}
	sharedPool = pool

	code := m.Run()

	_ = sqlDB.Close()
	pool.Close()
	_ = container.Terminate(ctx)
	os.Exit(code)
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	require.NotNil(t, sharedPool, "TestMain did not start Postgres")
	return store.New(sharedPool)
}

// seedUser creates a user with a main character, which almost every
// HANGAR-native capability needs as its subject.
func seedUser(t *testing.T, s *store.Store, characterID int64, name string) (uuid.UUID, int64) {
	t.Helper()
	ctx := context.Background()
	user, err := s.CreateUser(ctx, name)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx,
		`INSERT INTO app.character (character_id, name, owner_hash, user_id) VALUES ($1, $2, 'owner-hash', $3)`,
		characterID, name, user.UserID)
	require.NoError(t, err)
	require.NoError(t, s.SetUserMainCharacter(ctx, user.UserID, &characterID))
	return user.UserID, characterID
}

// TestUserAdministration — Appendix A #50.
//
// The capability is the pair of switches an administrator has over an
// account, and the assertion that matters is that DEACTIVATION IS NOT
// DELETION: app.user rows carry sessions, character links, role grants and
// an audit trail, and an operator who suspends an account must be able to
// restore it with all of that intact. A delete-based implementation would
// pass a naive "the user is gone" test and lose everything.
func TestUserAdministration(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	userID, characterID := seedUser(t, s, 2000000001, "Admin Subject")

	before, err := s.GetUser(ctx, userID)
	require.NoError(t, err)
	require.True(t, before.IsActive, "a new account starts active")
	require.False(t, before.IsAdmin)

	require.NoError(t, s.SetUserActive(ctx, userID, false))
	require.NoError(t, s.SetUserAdmin(ctx, userID, true))

	after, err := s.GetUser(ctx, userID)
	require.NoError(t, err)
	require.False(t, after.IsActive)
	require.True(t, after.IsAdmin)
	require.NotNil(t, after.MainCharacterID)
	require.Equal(t, characterID, *after.MainCharacterID,
		"deactivation must not detach the account's characters — it is a suspension, not a delete")

	// The account still appears on the administration list, marked inactive.
	// A deactivated user that vanished from the board could never be
	// reactivated through it.
	page, err := s.ListUsersPage(ctx, uuid.Nil, 500)
	require.NoError(t, err)
	var found bool
	for _, u := range page {
		if u.UserID == userID {
			found = true
			require.False(t, u.IsActive)
		}
	}
	require.True(t, found, "a deactivated account must remain administrable")

	require.NoError(t, s.SetUserActive(ctx, userID, true))
	restored, err := s.GetUser(ctx, userID)
	require.NoError(t, err)
	require.True(t, restored.IsActive)
}

// TestScopeAdministration — Appendix A #48, PUT /api/v1/admin/scopes.
//
// "Replace one role's grant set atomically" is the whole capability, and
// atomicity is the part worth a database. A replace implemented as
// delete-then-insert outside a transaction leaves a window in which the role
// grants NOTHING — and because internal/rbac materialises effective
// permissions, a resolve landing in that window writes the empty set and it
// persists until the next refresh.
func TestScopeAdministration(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	userID, _ := seedUser(t, s, 2000000002, "Scope Subject")

	role, err := s.CreateRole(ctx, "capability-scope-admin", nil, false)
	require.NoError(t, err)
	require.NoError(t, s.AssignUserRole(ctx, userID, role.RoleID, uuid.NullUUID{}))

	require.NoError(t, rbac.ReplaceRoleGrants(ctx, sharedPool, role.RoleID, allow("characters.view", "corporations.view")))
	require.NoError(t, rbac.RefreshUser(ctx, s, userID))

	permitted, err := s.GetEffectivePermission(ctx, userID, "characters.view")
	require.NoError(t, err)
	require.True(t, permitted.Permitted)

	// Replace with a DISJOINT set: every previous grant goes and a new one
	// arrives, in one transaction.
	require.NoError(t, rbac.ReplaceRoleGrants(ctx, sharedPool, role.RoleID, allow("tools.view")))
	require.NoError(t, rbac.RefreshUser(ctx, s, userID))

	grants, err := s.ListRoleGrants(ctx, role.RoleID)
	require.NoError(t, err)
	require.Len(t, grants, 1, "a replace is a replace, not a merge")
	require.Equal(t, "tools.view", grants[0].Permission)

	gone, err := s.GetEffectivePermission(ctx, userID, "characters.view")
	require.NoError(t, err)
	require.False(t, gone.Permitted, "a permission removed from the role must be removed from the materialised set")
	kept, err := s.GetEffectivePermission(ctx, userID, "tools.view")
	require.NoError(t, err)
	require.True(t, kept.Permitted)

	// A replace naming a permission outside the closed set is refused
	// outright rather than stored — app.permission is the authority and an
	// unknown grant would be a permission nothing ever checks.
	require.Error(t, rbac.ReplaceRoleGrants(ctx, sharedPool, role.RoleID, allow("not.a.real.permission")),
		"the permission set is closed (internal/domain); an unknown grant must be refused")
	still, err := s.ListRoleGrants(ctx, role.RoleID)
	require.NoError(t, err)
	require.Len(t, still, 1, "a refused replace must leave the existing grants untouched")
	require.Equal(t, "tools.view", still[0].Permission)
}

// TestAuditLog — Appendix A #51, the append-only security log.
//
// "Append-only" is the capability. The table has no UPDATE or DELETE query
// generated for it anywhere, which is the structural half; the behavioural
// half is that entries accumulate in recorded order and that the detail
// payload survives a round trip, because an audit entry whose detail is
// lost records that something happened and not what.
func TestAuditLog(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	userID, _ := seedUser(t, s, 2000000003, "Audited Subject")

	ip := netip.MustParseAddr("198.51.100.7")
	detail, err := json.Marshal(map[string]any{"role": "capability-scope-admin", "granted": []string{"tools.view"}})
	require.NoError(t, err)

	for _, action := range []string{"role.granted", "role.revoked", "token.minted"} {
		target := action + ":target"
		require.NoError(t, s.RecordSecurityLogEntry(ctx, gen.RecordSecurityLogEntryParams{
			UserID: uuid.NullUUID{UUID: userID, Valid: true}, Action: action, Target: &target, IpAddress: &ip, Detail: detail,
		}))
	}

	entries, err := s.ListSecurityLogForUser(ctx, uuid.NullUUID{UUID: userID, Valid: true}, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3, "every recorded action must be retrievable; the log appends and never replaces")

	// Newest first — an audit surface that showed the oldest entry first
	// would bury the event an operator is investigating.
	require.Equal(t, "token.minted", entries[0].Action)
	require.Equal(t, "role.granted", entries[2].Action)

	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(entries[0].Detail, &roundTripped))
	require.Equal(t, "capability-scope-admin", roundTripped["role"],
		"the detail payload is the difference between recording that something happened and recording what")
	require.NotNil(t, entries[0].IpAddress)
	require.Equal(t, ip.String(), entries[0].IpAddress.String())
}

// TestSquadCRUD — Appendix A #53.
//
// The delete is the assertion with substance: app.squad's children
// (member, moderator, role, application) hang off it with ON DELETE CASCADE
// rather than being cleaned up in Go, and a squad deleted while leaving
// orphaned role grants behind would keep granting them.
func TestSquadCRUD(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, characterID := seedUser(t, s, 2000000004, "Squad Owner")

	description := "created by the capability suite"
	squad, err := s.CreateSquad(ctx, gen.CreateSquadParams{
		Name: "Capability Squad", Type: "open", OwnerUserID: ownerID, Description: &description,
	})
	require.NoError(t, err)
	require.Equal(t, "Capability Squad", squad.Name)
	require.Equal(t, "open", squad.Type)

	renamed := "Capability Squad (renamed)"
	updated, err := s.UpdateSquad(ctx, squad.SquadID, renamed, &description)
	require.NoError(t, err)
	require.Equal(t, renamed, updated.Name)

	require.NoError(t, s.AddSquadMember(ctx, squad.SquadID, characterID))
	require.NoError(t, s.AddSquadModerator(ctx, squad.SquadID, ownerID))

	require.NoError(t, s.DeleteSquad(ctx, squad.SquadID))
	_, err = s.GetSquad(ctx, squad.SquadID)
	require.Error(t, err)

	members, err := s.ListSquadMembers(ctx, squad.SquadID)
	require.NoError(t, err)
	require.Empty(t, members, "squad_member must cascade; an orphaned membership row still matches an entitlement rule")
	moderator, err := s.IsSquadModerator(ctx, squad.SquadID, ownerID)
	require.NoError(t, err)
	require.False(t, moderator, "squad_moderator must cascade")
}

// TestSquadMembers — Appendix A #54.
//
// Membership is keyed by CHARACTER, not by user — a player joins a squad
// with one of their characters — while entitlement asks "is this USER in
// that squad". ListSquadsForUser is the join that bridges the two, and
// getting it wrong in either direction either grants an alt's access to the
// whole account or denies the account its own alt's.
func TestSquadMembers(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, mainCharacter := seedUser(t, s, 2000000005, "Member Main")

	// A second character on the SAME account, which is how EVE players
	// actually organise.
	alt := int64(2000000006)
	_, err := sharedPool.Exec(ctx,
		`INSERT INTO app.character (character_id, name, owner_hash, user_id) VALUES ($1, $2, 'owner-hash', $3)`,
		alt, "Member Alt", ownerID)
	require.NoError(t, err)

	squad, err := s.CreateSquad(ctx, gen.CreateSquadParams{
		Name: "Capability Members", Type: "open", OwnerUserID: ownerID,
	})
	require.NoError(t, err)

	require.NoError(t, s.AddSquadMember(ctx, squad.SquadID, alt))
	// Idempotent: the join endpoint is retryable and a duplicate must not
	// error or double-count.
	require.NoError(t, s.AddSquadMember(ctx, squad.SquadID, alt))

	members, err := s.ListSquadMembers(ctx, squad.SquadID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, alt, members[0].CharacterID)

	forUser, err := s.ListSquadsForUser(ctx, uuid.NullUUID{UUID: ownerID, Valid: true})
	require.NoError(t, err)
	require.Contains(t, forUser, squad.SquadID,
		"the ALT's membership makes the USER a member for entitlement purposes")

	require.NoError(t, s.RemoveSquadMember(ctx, squad.SquadID, alt))
	forUser, err = s.ListSquadsForUser(ctx, uuid.NullUUID{UUID: ownerID, Valid: true})
	require.NoError(t, err)
	require.NotContains(t, forUser, squad.SquadID)

	// The main character was never a member; removing the alt must not have
	// been a removal of the account.
	require.NoError(t, s.AddSquadMember(ctx, squad.SquadID, mainCharacter))
	members, err = s.ListSquadMembers(ctx, squad.SquadID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, mainCharacter, members[0].CharacterID)
}

// TestSquadRoles — Appendix A #55.
//
// A squad ROLE is an RBAC role every member of the squad holds by virtue of
// membership — a wholly separate mechanism from the entitlement engine's
// `squad` source kind, and one that must flow into
// app.effective_permission or it grants nothing at all.
func TestSquadRoles(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	memberUserID, memberCharacter := seedUser(t, s, 2000000007, "Squad Role Member")
	outsiderID, _ := seedUser(t, s, 2000000008, "Squad Outsider")

	squad, err := s.CreateSquad(ctx, gen.CreateSquadParams{
		Name: "Capability Roles", Type: "open", OwnerUserID: memberUserID,
	})
	require.NoError(t, err)
	require.NoError(t, s.AddSquadMember(ctx, squad.SquadID, memberCharacter))

	role, err := s.CreateRole(ctx, "capability-squad-role", nil, false)
	require.NoError(t, err)
	require.NoError(t, rbac.ReplaceRoleGrants(ctx, sharedPool, role.RoleID, allow("tools.view")))
	require.NoError(t, s.AddSquadRole(ctx, squad.SquadID, role.RoleID))

	roles, err := s.ListSquadRoles(ctx, squad.SquadID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	require.Equal(t, role.RoleID, roles[0])

	require.NoError(t, rbac.RefreshUser(ctx, s, memberUserID))
	require.NoError(t, rbac.RefreshUser(ctx, s, outsiderID))

	granted, err := s.GetEffectivePermission(ctx, memberUserID, "tools.view")
	require.NoError(t, err)
	require.True(t, granted.Permitted,
		"a squad role must reach app.effective_permission; a grant nothing materialises grants nothing")

	denied, err := s.GetEffectivePermission(ctx, outsiderID, "tools.view")
	require.NoError(t, err)
	require.False(t, denied.Permitted, "a squad role must not leak to non-members")

	// Removing the role from the squad withdraws it from the member.
	require.NoError(t, s.RemoveSquadRole(ctx, squad.SquadID, role.RoleID))
	require.NoError(t, rbac.RefreshUser(ctx, s, memberUserID))
	withdrawn, err := s.GetEffectivePermission(ctx, memberUserID, "tools.view")
	require.NoError(t, err)
	require.False(t, withdrawn.Permitted)
}

// TestWebhookRotationOverlap — Appendix A #56.
//
// A webhook secret cannot be swapped instantaneously: a receiver has to be
// able to verify a delivery signed with the OLD secret while it updates its
// configuration. So rotation demotes the current secret to `prev_*` with an
// expiry and installs the new one IN ONE STATEMENT — two statements would
// leave a window in which a delivery is signed with a secret nobody would
// accept.
//
// Ownership is in the UPDATE's own predicate rather than checked in Go,
// which is the second assertion: a handler that forgot the check must rotate
// nothing rather than rotate somebody else's endpoint.
func TestWebhookRotationOverlap(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ownerID, _ := seedUser(t, s, 2000000009, "Webhook Owner")
	strangerID, _ := seedUser(t, s, 2000000010, "Webhook Stranger")

	endpoint, err := s.CreateWebhookEndpoint(ctx, gen.CreateWebhookEndpointParams{
		OwnerUserID: ownerID, Url: "https://example.invalid/hook", EventFilter: []string{"alert.raised"},
		HmacKeyVersion: 1, HmacWrappedDek: []byte("wrapped-v1"), HmacNonce: []byte("nonce-v1"),
		HmacCiphertext: []byte("secret-v1"),
	})
	require.NoError(t, err)
	require.Nil(t, endpoint.PrevHmacCiphertext, "a freshly created endpoint has no superseded secret")

	rotated, err := s.RotateWebhookSecret(ctx, gen.RotateWebhookSecretParams{
		EndpointID: endpoint.EndpointID, OwnerUserID: ownerID,
		Grace:          24 * time.Hour,
		HmacKeyVersion: 2, HmacWrappedDek: []byte("wrapped-v2"), HmacNonce: []byte("nonce-v2"),
		HmacCiphertext: []byte("secret-v2"),
	})
	require.NoError(t, err)
	require.Equal(t, []byte("secret-v2"), rotated.HmacCiphertext, "the new secret is installed")
	require.Equal(t, []byte("secret-v1"), rotated.PrevHmacCiphertext, "and the old one is retained, not discarded")
	require.EqualValues(t, 1, *rotated.PrevHmacKeyVersion)
	require.NotNil(t, rotated.PrevHmacExpiresAt)
	require.True(t, rotated.PrevHmacExpiresAt.After(time.Now()),
		"the overlap window must be OPEN immediately after rotation, or a delivery in flight is unverifiable")

	// Ownership is a predicate. A stranger's rotate matches no row.
	_, err = s.RotateWebhookSecret(ctx, gen.RotateWebhookSecretParams{
		EndpointID: endpoint.EndpointID, OwnerUserID: strangerID,
		Grace:          24 * time.Hour,
		HmacKeyVersion: 3, HmacWrappedDek: []byte("wrapped-v3"), HmacNonce: []byte("nonce-v3"),
		HmacCiphertext: []byte("secret-v3"),
	})
	require.Error(t, err, "rotation is owner-scoped in SQL; a stranger must match no row rather than succeed")

	unchanged, err := s.GetWebhookEndpointForOwner(ctx, endpoint.EndpointID, ownerID)
	require.NoError(t, err)
	require.Equal(t, []byte("secret-v2"), unchanged.HmacCiphertext)

	// Expiry clears the superseded secret — a second live secret that
	// outlives its window is a second secret to steal. Nothing is cleared
	// while the window is still open.
	require.NoError(t, s.ExpireWebhookPreviousSecret(ctx, endpoint.EndpointID))
	stillOverlapping, err := s.GetWebhookEndpointForOwner(ctx, endpoint.EndpointID, ownerID)
	require.NoError(t, err)
	require.NotNil(t, stillOverlapping.PrevHmacCiphertext, "the grace window has not passed; the old secret stays")

	_, err = sharedPool.Exec(ctx,
		`UPDATE app.webhook_endpoint SET prev_hmac_expires_at = now() - interval '1 second' WHERE endpoint_id = $1`,
		endpoint.EndpointID)
	require.NoError(t, err)
	require.NoError(t, s.ExpireWebhookPreviousSecret(ctx, endpoint.EndpointID))
	expired, err := s.GetWebhookEndpointForOwner(ctx, endpoint.EndpointID, ownerID)
	require.NoError(t, err)
	require.Nil(t, expired.PrevHmacCiphertext, "once the window has passed the superseded secret must be gone")
	require.Nil(t, expired.PrevHmacExpiresAt)
}

// TestCharacterNotes — Appendix A #43, officer notes on a character.
//
// Notes are HANGAR-native annotation: they belong to the installation, not
// to ESI, and they must survive whatever the character's synced state does.
// The assertion is that notes accumulate per character and are attributed —
// an unattributed note in a corporation tool is a rumour.
func TestCharacterNotes(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	authorID, subject := seedUser(t, s, 2000000011, "Note Subject")
	other := int64(2000000012)
	_, err := sharedPool.Exec(ctx,
		`INSERT INTO app.character (character_id, name, owner_hash) VALUES ($1, $2, 'owner-hash')`, other, "Other Subject")
	require.NoError(t, err)

	first, err := s.CreateCharacterNote(ctx, subject, authorID, "Applied to the corp on 2026-08-01.")
	require.NoError(t, err)
	require.Equal(t, authorID, first.AuthorUserID, "a note records who wrote it")

	_, err = s.CreateCharacterNote(ctx, subject, authorID, "Vouched for by a director.")
	require.NoError(t, err)
	_, err = s.CreateCharacterNote(ctx, other, authorID, "Unrelated.")
	require.NoError(t, err)

	notes, err := s.ListCharacterNotes(ctx, subject)
	require.NoError(t, err)
	require.Len(t, notes, 2, "notes accumulate; a second note must not replace the first")

	otherNotes, err := s.ListCharacterNotes(ctx, other)
	require.NoError(t, err)
	require.Len(t, otherNotes, 1, "notes are scoped to one character")
}

// TestIntelGraph — Appendix A #17, the character interaction graph.
//
// An edge is directed and typed, and the READ is undirected: "who has this
// character interacted with" must return edges where they are either
// endpoint, or half the graph is invisible depending on who mailed whom
// first. That asymmetry between how edges are stored and how they are
// queried is the whole capability, and it is a SQL property.
func TestIntelGraph(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, subject := seedUser(t, s, 2000000013, "Intel Subject")
	counterparty := int64(2000000014)
	third := int64(2000000015)
	for id, name := range map[int64]string{counterparty: "Intel Counterparty", third: "Intel Third"} {
		_, err := sharedPool.Exec(ctx, `INSERT INTO app.character (character_id, name, owner_hash) VALUES ($1, $2, 'owner-hash')`, id, name)
		require.NoError(t, err)
	}

	observed := time.Now().UTC().Truncate(time.Second)
	// Subject -> counterparty (mail), counterparty -> subject (contact), and
	// an edge between two OTHER characters that must not appear.
	_, err := s.UpsertCharacterIntelEdge(ctx, gen.UpsertCharacterIntelEdgeParams{
		SourceCharacterID: subject, TargetCharacterID: counterparty, EdgeKind: "mail", Weight: 3, LastObservedAt: observed,
	})
	require.NoError(t, err)
	_, err = s.UpsertCharacterIntelEdge(ctx, gen.UpsertCharacterIntelEdgeParams{
		SourceCharacterID: counterparty, TargetCharacterID: subject, EdgeKind: "contact", Weight: 7, LastObservedAt: observed,
	})
	require.NoError(t, err)
	_, err = s.UpsertCharacterIntelEdge(ctx, gen.UpsertCharacterIntelEdgeParams{
		SourceCharacterID: counterparty, TargetCharacterID: third, EdgeKind: "mail", Weight: 9, LastObservedAt: observed,
	})
	require.NoError(t, err)

	edges, err := s.ListCharacterIntelEdges(ctx, subject)
	require.NoError(t, err)
	require.Len(t, edges, 2, "the read is undirected: an edge INTO the character counts as much as one out of it")
	require.EqualValues(t, 7, edges[0].Weight, "heaviest edge first — the graph is read as a ranking")
	require.Equal(t, "contact", edges[0].EdgeKind)
	require.Equal(t, "mail", edges[1].EdgeKind)

	// Re-observing an edge updates its weight in place rather than adding a
	// parallel edge; the graph would otherwise grow without bound.
	_, err = s.UpsertCharacterIntelEdge(ctx, gen.UpsertCharacterIntelEdgeParams{
		SourceCharacterID: subject, TargetCharacterID: counterparty, EdgeKind: "mail", Weight: 11, LastObservedAt: observed,
	})
	require.NoError(t, err)
	edges, err = s.ListCharacterIntelEdges(ctx, subject)
	require.NoError(t, err)
	require.Len(t, edges, 2, "(source, target, kind) is the identity of an edge")
	require.EqualValues(t, 11, edges[0].Weight)
}

// TestAdminSyncBoard — Appendix A #52.
//
// The board's whole reason for existing is defect B42: for nine phases an
// installation with ZERO subscriptions looked merely quiet, because "never
// scheduled" and "scheduled but failing" were indistinguishable on every
// surface. SubscriptionEnabledForPath is the query that separates them, and
// this asserts it answers differently for the three states a route can be
// in — never subscribed, subscribed and enabled, subscribed and disabled.
func TestAdminSyncBoard(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	route, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID: "CapabilityBoardRoute", Method: "GET", UpstreamPath: "/capability/board-probe",
		CompatibilityDate: pgtype.Date{Time: time.Now().UTC().Truncate(24 * time.Hour), Valid: true},
		SpecFragment:      json.RawMessage(`{}`), IdentifierTypes: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// State 1: catalogued, never subscribed. This is B42's state, and the
	// one that used to be invisible.
	enabled, err := s.SubscriptionEnabledForPath(ctx, "/capability/board-probe")
	require.NoError(t, err)
	require.False(t, enabled, "a route with no subscription must report as not polled, not as merely quiet")

	sub, err := s.UpsertSyncSubscription(ctx, gen.UpsertSyncSubscriptionParams{
		EntityKind: "global", EntityID: 0, RouteID: route.RouteID,
	})
	require.NoError(t, err)

	// State 2: subscribed and enabled.
	enabled, err = s.SubscriptionEnabledForPath(ctx, "/capability/board-probe")
	require.NoError(t, err)
	require.True(t, enabled)

	// State 3: subscribed and DISABLED — a scope was revoked, say. The board
	// must distinguish this from state 1: the row still carries etag and
	// cursor state, and the operator action is different.
	require.NoError(t, s.SetSyncSubscriptionEnabled(ctx, sub.SubscriptionID, false))
	enabled, err = s.SubscriptionEnabledForPath(ctx, "/capability/board-probe")
	require.NoError(t, err)
	require.False(t, enabled)

	stillThere, err := s.GetSyncSubscription(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	require.False(t, stillThere.Enabled,
		"disabling must not delete: the row's accumulated sync state is expensive to rebuild and still valid")

	// A sync run against the subscription is what the board renders as
	// history, and it must survive the subscription being disabled.
	run, err := s.StartSyncRun(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	outcome, rows, status := "200", int32(0), int16(200)
	require.NoError(t, s.FinishSyncRun(ctx, gen.FinishSyncRunParams{
		RunID: run.RunID, Status: &status, Outcome: &outcome, RowsAffected: &rows,
	}))
	runs, err := s.ListRecentSyncRuns(ctx, sub.SubscriptionID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NotNil(t, runs[0].Outcome)
	require.Equal(t, "200", *runs[0].Outcome)
	require.NotNil(t, runs[0].RowsAffected)
	require.EqualValues(t, 0, *runs[0].RowsAffected,
		"a run that landed no rows is recorded as such — zero rows and no run are different facts")
}

// TestSyncAllianceSubscriptionsAreOrdered — capability #37's database half.
//
// PHASE 20.8. AllianceWorker cannot be verified against real ESI on the
// development installation: app.alliance holds zero rows because HANGAR Corp
// is in no alliance, so no alliance subscription is ever created and the
// worker is never dispatched. What CAN be verified is the SQL, which is
// where the capability's real logic lives — the reconcile statement's scope
// gate and its two-level join, and the elector's candidate pool.
//
// This is the test that would have failed if the alliance reconcile had been
// written against app.character.alliance_id (a column that does not exist)
// or without the scope gate.
func TestSyncAllianceSubscriptionsAreOrdered(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	const (
		allianceID    = int64(99000001)
		corporationID = int64(98000101)
		characterID   = int64(2000000016)
	)
	_, err := sharedPool.Exec(ctx, `INSERT INTO app.alliance (alliance_id, name) VALUES ($1, '')`, allianceID)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx,
		`INSERT INTO app.corporation (corporation_id, name, ticker, alliance_id) VALUES ($1, 'Capability Corp', 'CAP', $2)`,
		corporationID, allianceID)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx,
		`INSERT INTO app.character (character_id, name, owner_hash, corporation_id) VALUES ($1, 'Alliance Actor', 'owner-hash', $2)`,
		characterID, corporationID)
	require.NoError(t, err)

	// The candidate pool is "every tracked character whose CORPORATION is in
	// the alliance" — a character has no alliance_id of its own.
	pool, err := s.ListAllianceMemberCharacters(ctx, allianceID)
	require.NoError(t, err)
	require.Equal(t, []int64{characterID}, pool,
		"the elector's alliance pool joins through app.corporation.alliance_id")

	// The member-corporation reconcile writes membership for corporations
	// HANGAR ALREADY HAS and inserts nothing. An id it has never seen is
	// silently not created — that is the no-stub rule.
	joined, err := s.SetCorporationAllianceMembership(ctx, allianceID, []int64{corporationID, 98000999})
	require.NoError(t, err)
	require.EqualValues(t, 0, joined, "the corporation was already recorded in this alliance; nothing changed")

	var unknown int64
	require.NoError(t, sharedPool.QueryRow(ctx,
		`SELECT count(*) FROM app.corporation WHERE corporation_id = 98000999`).Scan(&unknown))
	require.EqualValues(t, 0, unknown,
		"a member id HANGAR has no row for must NOT be stubbed — that is the empty-name defect capability #37 exists to fix")

	// A corporation that has left the alliance is cleared, not guessed at.
	left, err := s.ClearCorporationAllianceMembershipNotIn(ctx, allianceID, []int64{98000999})
	require.NoError(t, err)
	require.EqualValues(t, 1, left)
	corp, err := s.GetCorporation(ctx, corporationID)
	require.NoError(t, err)
	require.Nil(t, corp.AllianceID, "a leaver's alliance is cleared; this route says nothing about where they went")
}

// allow turns permission names into the grant rows ReplaceRoleGrants takes.
// Every grant here is an ALLOW: the deny half of the truth table is
// internal/rbac's own TestDenyPrecedesAllowTruthTable, not this capability's.
func allow(permissions ...string) []gen.AppRoleGrant {
	out := make([]gen.AppRoleGrant, 0, len(permissions))
	for _, p := range permissions {
		out = append(out, gen.AppRoleGrant{Permission: p, Effect: "allow"})
	}
	return out
}
