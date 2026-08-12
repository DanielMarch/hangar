package v2shim

import (
	"net/http"
)

// The two controllers with NO shim.
//
// SRS Appendix C: `RoleController` and `RoleLookupController` are
// "Reshaped — grant model differs; **breaking, no shim**", and the roadmap
// adds that "pretending otherwise is worse than a clean break".
//
// ── WHY THERE IS NO HONEST TRANSLATION ───────────────────────────────────
// Legacy's model is a role that carries permission TITLES plus per-grant
// JSON `filters`, attached directly to users and squads, and evaluated at
// request time. HANGAR's is a closed permission vocabulary
// (`app.permission`) resolved through role_grant into a MATERIALISED
// `app.effective_permission` row per (user, permission), with an explicit
// allow/deny effect that can subtract.
//
// The pieces do not line up in either direction:
//
//   - Legacy's `filters` have no representation at all. A shim would have
//     to either drop them — turning a narrowly-filtered grant into an
//     unfiltered one, which SILENTLY WIDENS ACCESS — or invent a field.
//   - HANGAR's `deny` effect has no legacy counterpart. A role that
//     subtracts a permission would serialise as a role that grants
//     nothing, so a caller reading the shimmed response would conclude
//     access was absent when it is actively revoked.
//   - `GET /roles/query/permission-check/{character_id}/{permission_name}`
//     asks a yes/no question about a *character*; HANGAR materialises
//     permissions per *user*, and a user has many characters. Any answer
//     the shim gave would be a guess about which one was meant.
//
// The first of those is the decisive one. A partial shim here does not
// return incomplete data, it returns a WRONG answer about who can do what,
// to a caller whose whole reason for asking is to make an access decision.
// 410 Gone with an explicit pointer is the only safe response.
const (
	// StatusGone is what a reshaped route answers. 410, not 404: the
	// resource existed and is deliberately gone, which is a different
	// message to a client than "never heard of it" — and 410 is the status
	// a caching layer treats as permanent.
	StatusGone = http.StatusGone
)

const roleBreak = `The /api/v2 roles surface is a BREAKING CHANGE with no shim.

Legacy roles carried permission titles plus per-grant JSON "filters", attached directly to
users and squads. HANGAR resolves a closed permission vocabulary through roles into a
materialised effective-permission set per user, with an explicit allow/deny effect.

There is no faithful translation, and a partial one would be unsafe rather than merely
incomplete:
  * legacy "filters" have no representation, so a shim would have to drop them — turning a
    narrowly-scoped grant into an unrestricted one;
  * HANGAR's "deny" effect has no legacy counterpart, so a role that REVOKES a permission
    would serialise as one that grants nothing.

Both failures are silent, and both produce a wrong answer to a caller who is asking in order
to make an access decision.

Migrate to:
  GET/POST/PUT /api/v1/admin/roles          role definitions
  PUT          /api/v1/admin/scopes         replace a role's whole grant set atomically
  GET          /api/v1/admin/users/{id}     a user's roles and effective permissions

See ` + DeprecationDocsURL + `.`

const roleLookupBreak = `The /api/v2 roles/query surface is a BREAKING CHANGE with no shim.

Legacy answered permission and role checks for a CHARACTER id. HANGAR materialises
permissions per USER, and a user holds many characters, so there is no character-scoped
answer to give — any value the shim returned would be a guess about which character was
meant, handed to a caller making an access decision.

Migrate to:
  GET /api/v1/admin/users/{id}    the user's roles and materialised effective permissions
  GET /api/v1/me                  the calling user's own permissions

To resolve a character to its user first:
  GET /api/v1/characters/{character_id}

See ` + DeprecationDocsURL + `.`

// breakingChangeHandler answers a reshaped route.
//
// It is deliberately NOT behind RequirePermission. The answer is the same
// for every caller and contains no data — it is documentation — and
// demanding a credential first would mean an integrator debugging a 410
// has to get authentication right before they can read why the route is
// gone. It still carries the Deprecation and Sunset headers, because
// TestShimEmitsDeprecationAndSunset is about EVERY shim response and a
// client whose only /api/v2 traffic is these routes is the one that most
// needs the signal.
func breakingChangeHandler(message string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, err := Encode(message)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, StatusGone, body)
	})
}
