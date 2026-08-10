package rbac_test

import (
	"fmt"
	"testing"

	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/stretchr/testify/require"
)

// grantState is one grant path's contribution to a single permission:
// absent (the user holds no role granting/denying it via this path),
// allow, or deny. 02_DATABASE_SCHEMA.md §4.2 gives a user exactly two
// paths to a role — direct (app.user_role) and squad-derived
// (app.squad_member -> app.character.user_id -> app.squad_role) — NOT
// the seven-source split described in 01_ARCHITECTURE.md §9.1, which
// belongs to Phase 11's entitlement engine over a different table
// (app.entitlement_rule). This test is exhaustive over the two paths
// this schema actually has, crossed for both the checked permission and
// the superuser fallback permission (rbac.SuperuserPermission) — 3^4 = 81
// combinations, covering every way deny-precedence and the superuser
// fallback can interact.
type grantState int

const (
	stateAbsent grantState = iota
	stateAllow
	stateDeny
)

func (s grantState) grants(permission string) []rbac.Grant {
	switch s {
	case stateAllow:
		return []rbac.Grant{{Permission: permission, Effect: rbac.EffectAllow}}
	case stateDeny:
		return []rbac.Grant{{Permission: permission, Effect: rbac.EffectDeny}}
	default:
		return nil
	}
}

func (s grantState) String() string {
	return [...]string{"absent", "allow", "deny"}[s]
}

// TestDenyPrecedesAllowTruthTable (roadmap exit criterion): exhaustive
// over every grant-path combination x {allow, deny, absent}. The expected
// value is computed independently of resolve.go's implementation
// (deliberately verbose boolean logic, not a rewrite of Resolve's
// allow/deny-map algorithm) so this test can actually catch a defect in
// Resolve rather than restating it.
func TestDenyPrecedesAllowTruthTable(t *testing.T) {
	const permission = "characters.view"
	states := []grantState{stateAbsent, stateAllow, stateDeny}

	cases := 0
	for _, direct := range states {
		for _, squad := range states {
			for _, superDirect := range states {
				for _, superSquad := range states {
					cases++
					name := fmt.Sprintf("perm(direct=%s,squad=%s)/super(direct=%s,squad=%s)", direct, squad, superDirect, superSquad)
					t.Run(name, func(t *testing.T) {
						grants := append(direct.grants(permission), squad.grants(permission)...)
						grants = append(grants, superDirect.grants(rbac.SuperuserPermission)...)
						grants = append(grants, superSquad.grants(rbac.SuperuserPermission)...)

						// Independent expected-value derivation.
						permissionDenied := direct == stateDeny || squad == stateDeny
						permissionAllowed := direct == stateAllow || squad == stateAllow
						superuserDenied := superDirect == stateDeny || superSquad == stateDeny
						superuserAllowed := superDirect == stateAllow || superSquad == stateAllow

						var want bool
						if permissionDenied {
							want = false // deny on the checked permission ALWAYS wins, even over an allowed superuser
						} else if permissionAllowed {
							want = true
						} else if superuserAllowed && !superuserDenied {
							want = true // superuser fallback, itself deniable
						} else {
							want = false
						}

						got := rbac.Resolve(grants, permission)
						require.Equal(t, want, got, "grants=%+v", grants)
					})
				}
			}
		}
	}
	require.Equal(t, 81, cases, "must be exhaustive over 3^4 combinations")
}

// TestSuperuserNeverBypassesAnExplicitDeny is the specific case the
// roadmap calls out by name: "Superuser is a permission, not a bypass
// branch in code. A bypass branch cannot be denied." — a user holding an
// allowed, non-denied superuser grant is still denied a permission that
// is itself explicitly denied by some other role they hold.
func TestSuperuserNeverBypassesAnExplicitDeny(t *testing.T) {
	grants := []rbac.Grant{
		{Permission: rbac.SuperuserPermission, Effect: rbac.EffectAllow},
		{Permission: "admin.users.manage", Effect: rbac.EffectDeny},
	}
	require.False(t, rbac.Resolve(grants, "admin.users.manage"))
	// Every OTHER permission still benefits from the superuser fallback.
	require.True(t, rbac.Resolve(grants, "characters.view"))
}

// TestNoRolesMeansNoPermissions (roadmap exit criterion): a grant set
// with nothing in it resolves every permission to false — never "default
// allow".
func TestNoRolesMeansNoPermissions(t *testing.T) {
	var grants []rbac.Grant
	for _, p := range rbac.AllPermissions() {
		require.False(t, rbac.Resolve(grants, p), "permission %q must be denied with zero grants", p)
	}

	all := rbac.ResolveAll(grants, rbac.AllPermissions())
	for _, p := range rbac.AllPermissions() {
		require.False(t, all[p], "ResolveAll must also deny %q with zero grants", p)
	}
}
