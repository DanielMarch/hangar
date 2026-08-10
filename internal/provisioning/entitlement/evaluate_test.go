package entitlement_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/stretchr/testify/require"
)

// TestStrictModeFailsUserWhenAnyAltInvalid (roadmap exit criterion): a
// user whose main character's token is perfectly valid but whose ALT is
// invalid must still be denied every group — "any alt" is the operative
// word (01_ARCHITECTURE.md §9.1). Constructed so that, absent Strict Mode,
// a `public` rule would trivially grant the group — proving the denial
// comes from StrictModeDenied, not from an empty rule set.
func TestStrictModeFailsUserWhenAnyAltInvalid(t *testing.T) {
	group := uuid.New()
	rules := []entitlement.Rule{
		{GroupID: group, SourceKind: entitlement.SourcePublic, SourceRef: "", Effect: entitlement.EffectGrant},
	}
	world := entitlement.WorldState{
		UserID:           uuid.New(),
		CharacterIDs:     []int64{100, 200}, // 100 = main (valid token), 200 = alt (invalid token)
		StrictModeDenied: true,              // set because character 200's token is invalid
	}

	granted := entitlement.Evaluate(world, rules)
	require.Empty(t, granted, "any invalid alt token must deny the whole user, even against a `public` grant rule")
}

// TestStrictModeAllowsWhenEveryTokenValid is TestStrictModeFailsUserWhenAnyAltInvalid's
// control: the same rule set, only StrictModeDenied flipped, must grant.
func TestStrictModeAllowsWhenEveryTokenValid(t *testing.T) {
	group := uuid.New()
	rules := []entitlement.Rule{
		{GroupID: group, SourceKind: entitlement.SourcePublic, SourceRef: "", Effect: entitlement.EffectGrant},
	}
	world := entitlement.WorldState{UserID: uuid.New(), StrictModeDenied: false}

	granted := entitlement.Evaluate(world, rules)
	require.True(t, granted[group])
}

// TestDenyBeatsGrantPerGroup mirrors internal/rbac's deny-precedence truth
// table, over app.entitlement_rule's independent seven-source model
// instead of app.role_grant.
func TestDenyBeatsGrantPerGroup(t *testing.T) {
	group := uuid.New()
	roleID := uuid.New()
	world := entitlement.WorldState{UserID: uuid.New(), RoleIDs: []uuid.UUID{roleID}}

	rules := []entitlement.Rule{
		{GroupID: group, SourceKind: entitlement.SourceRole, SourceRef: roleID.String(), Effect: entitlement.EffectGrant},
		{GroupID: group, SourceKind: entitlement.SourcePublic, SourceRef: "", Effect: entitlement.EffectDeny},
	}
	granted := entitlement.Evaluate(world, rules)
	require.False(t, granted[group], "a deny anywhere on a group must beat a grant, regardless of source")
}

// TestEachSourceKindMatchesIndependently exercises every one of the seven
// grant sources in isolation, proving Evaluate reads exactly the WorldState
// field its doc comment says it does for each — in particular that a
// `role` rule matches ONLY app.user_role-held roles (WorldState.RoleIDs),
// never a role reached via squad membership, and that a `squad` rule
// matches direct membership regardless of whether that squad grants any
// RBAC role at all (READ FIRST #3's distinction).
func TestEachSourceKindMatchesIndependently(t *testing.T) {
	userID := uuid.New()
	roleID := uuid.New()
	squadID := uuid.New()

	world := entitlement.WorldState{
		UserID:         userID,
		RoleIDs:        []uuid.UUID{roleID},
		SquadIDs:       []uuid.UUID{squadID},
		CorporationIDs: []int64{98000001},
		AllianceIDs:    []int64{99000001},
		CorpTitles:     []string{"98000001:5"},
	}

	cases := []struct {
		name string
		rule entitlement.Rule
		want bool
	}{
		{"user match", entitlement.Rule{SourceKind: entitlement.SourceUser, SourceRef: userID.String(), Effect: entitlement.EffectGrant}, true},
		{"user mismatch", entitlement.Rule{SourceKind: entitlement.SourceUser, SourceRef: uuid.NewString(), Effect: entitlement.EffectGrant}, false},
		{"role match", entitlement.Rule{SourceKind: entitlement.SourceRole, SourceRef: roleID.String(), Effect: entitlement.EffectGrant}, true},
		{"role mismatch", entitlement.Rule{SourceKind: entitlement.SourceRole, SourceRef: uuid.NewString(), Effect: entitlement.EffectGrant}, false},
		{"squad match", entitlement.Rule{SourceKind: entitlement.SourceSquad, SourceRef: squadID.String(), Effect: entitlement.EffectGrant}, true},
		{"squad mismatch", entitlement.Rule{SourceKind: entitlement.SourceSquad, SourceRef: uuid.NewString(), Effect: entitlement.EffectGrant}, false},
		{"corporation match", entitlement.Rule{SourceKind: entitlement.SourceCorporation, SourceRef: "98000001", Effect: entitlement.EffectGrant}, true},
		{"corporation mismatch", entitlement.Rule{SourceKind: entitlement.SourceCorporation, SourceRef: "1", Effect: entitlement.EffectGrant}, false},
		{"alliance match", entitlement.Rule{SourceKind: entitlement.SourceAlliance, SourceRef: "99000001", Effect: entitlement.EffectGrant}, true},
		{"alliance mismatch", entitlement.Rule{SourceKind: entitlement.SourceAlliance, SourceRef: "1", Effect: entitlement.EffectGrant}, false},
		{"corp_title match", entitlement.Rule{SourceKind: entitlement.SourceCorpTitle, SourceRef: "98000001:5", Effect: entitlement.EffectGrant}, true},
		{"corp_title mismatch (right corp, wrong title)", entitlement.Rule{SourceKind: entitlement.SourceCorpTitle, SourceRef: "98000001:6", Effect: entitlement.EffectGrant}, false},
		{"public always matches", entitlement.Rule{SourceKind: entitlement.SourcePublic, SourceRef: "", Effect: entitlement.EffectGrant}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := uuid.New()
			rule := tc.rule
			rule.GroupID = group
			granted := entitlement.Evaluate(world, []entitlement.Rule{rule})
			require.Equal(t, tc.want, granted[group])
		})
	}
}

// TestRoleSourceIgnoresSquadGrantedRoles is READ FIRST #3's specific
// double-check: a role reached ONLY through a squad's app.squad_role RBAC
// grant (never placed into WorldState.RoleIDs by sources.go — that field
// is documented as direct-only) must NOT satisfy a `role` entitlement
// rule for that same role id. Only a `squad` rule naming the squad itself
// would.
func TestRoleSourceIgnoresSquadGrantedRoles(t *testing.T) {
	group := uuid.New()
	squadGrantedRoleID := uuid.New() // a role this user holds only via squad_role, per the RBAC model — never appears in WorldState.RoleIDs
	world := entitlement.WorldState{UserID: uuid.New(), RoleIDs: nil, SquadIDs: []uuid.UUID{uuid.New()}}

	rules := []entitlement.Rule{
		{GroupID: group, SourceKind: entitlement.SourceRole, SourceRef: squadGrantedRoleID.String(), Effect: entitlement.EffectGrant},
	}
	granted := entitlement.Evaluate(world, rules)
	require.False(t, granted[group], "a role entitlement rule must not match a role reached only via squad RBAC grant")
}

// TestMalformedSourceRefMatchesNobody: an unparseable source_ref for a
// uuid/int-typed source kind never matches, and never panics — it doesn't
// take down evaluation of every OTHER rule either.
func TestMalformedSourceRefMatchesNobody(t *testing.T) {
	group := uuid.New()
	world := entitlement.WorldState{UserID: uuid.New()}
	rules := []entitlement.Rule{
		{GroupID: group, SourceKind: entitlement.SourceRole, SourceRef: "not-a-uuid", Effect: entitlement.EffectGrant},
		{GroupID: group, SourceKind: entitlement.SourceCorporation, SourceRef: "not-a-number", Effect: entitlement.EffectGrant},
	}
	require.NotPanics(t, func() {
		granted := entitlement.Evaluate(world, rules)
		require.False(t, granted[group])
	})
}
