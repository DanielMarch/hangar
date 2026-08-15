package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hangar-project/hangar/internal/sync"
)

// routeSecurityScopes reads each catalogued GET operation's required scopes
// out of the embedded spec snapshot — the same source tools/scopedump uses,
// and for the same reason unmapped_test.go gives: "can HANGAR ever poll this
// route" must be answerable at BUILD time, with no database.
func routeSecurityScopes(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "esi", "catalogue", "embedded", "openapi.snapshot.json"))
	if err != nil {
		t.Fatalf("reading the embedded spec snapshot: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the embedded spec snapshot: %v", err)
	}

	out := map[string][]string{}
	seenGet := false
	for path, ops := range spec.Paths {
		op, ok := ops["get"]
		if !ok {
			continue
		}
		seenGet = true
		for _, req := range op.Security {
			for _, list := range req {
				out[path] = append(out[path], list...)
			}
		}
	}
	if !seenGet {
		t.Fatal("the embedded snapshot yielded no GET operations — every check below would pass vacuously")
	}
	return out
}

// TestGlobalRoutesRequireNoScope holds the invariant that makes an
// entity_kind = 'global' subscription safe.
//
// ── WHY THIS IS A REAL HAZARD AND NOT A TIDINESS RULE (PHASE 20.8) ───────
// The three reconcile statements in db/queries/sync_subscription.sql are not
// symmetric. The character and corporation ones carry a NOT EXISTS scope
// gate, so a route whose scopes the acting token lacks produces no
// subscription at all. ReconcileGlobalSubscriptions carries NO such gate,
// and it cannot: a global row has acting_character_id = NULL, so there is no
// token to compare against. DisableUnscopedSubscriptions then reads a NULL
// acting character as "fully covered" and leaves the row enabled.
//
// The consequence of putting a SCOPED route in the global set is therefore
// not a missing sync — it is a subscription created enabled, polling on
// every cadence, 403ing every time, forever. Governor 2 counts every one of
// those against an installation-wide budget of 100 errors per minute (§5.7),
// so a single misplaced route can pause the whole installation's ESI access.
//
// The invariant held by accident until this phase, because every global
// route happened to be unauthenticated and the comment on
// ReconcileGlobalSubscriptions merely asserted it ("these routes are
// unauthenticated"). Capability #41's station route is the first global
// route added since, and capability #37's contacts routes were the first
// serious temptation to break it — an alliance fan-out driven from one
// global subscription would have been much less code than
// worker/alliance.go, and would have shipped exactly this bug. Hence a test
// rather than a sentence.
func TestGlobalRoutesRequireNoScope(t *testing.T) {
	scopes := routeSecurityScopes(t)

	var offenders []string
	for path, kind := range SubscribableRoutes() {
		if kind != sync.EntityGlobal {
			continue
		}
		if required := scopes[path]; len(required) > 0 {
			sort.Strings(required)
			offenders = append(offenders, path+" requires "+joinScopes(required))
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("%d global-scoped route(s) require an ESI scope.\n"+
			"ReconcileGlobalSubscriptions has no scope gate (a global row has no acting character to gate on),\n"+
			"so these would be created ENABLED and 403 on every attempt forever, spending Governor 2's\n"+
			"installation-wide error budget. Move them to an owner-scoped worker instead: %v",
			len(offenders), offenders)
	}
}

// TestScopedSubscribableRoutesAreOwnerScoped is the same invariant stated
// from the other end, and it is the one that would have caught the mistake
// while it was being made: every route in the sync set that needs a scope
// must belong to an entity kind whose reconcile statement HAS the NOT EXISTS
// gate — character, corporation or alliance.
func TestScopedSubscribableRoutesAreOwnerScoped(t *testing.T) {
	scopes := routeSecurityScopes(t)
	gated := map[sync.EntityKind]bool{
		sync.EntityCharacter:   true,
		sync.EntityCorporation: true,
		sync.EntityAlliance:    true,
	}

	var ungated []string
	for path, kind := range SubscribableRoutes() {
		if len(scopes[path]) == 0 {
			continue
		}
		if !gated[kind] {
			ungated = append(ungated, string(kind)+" "+path)
		}
	}
	sort.Strings(ungated)
	if len(ungated) > 0 {
		t.Errorf("scoped route(s) are subscribable under an entity kind whose reconcile statement has no scope gate: %v", ungated)
	}
}

// TestStationRouteIsUnauthenticatedAndStructureRouteIsNot pins the measured
// fact that decided capability #41's shape: the two halves of location
// resolution differ in exactly one respect, and it is the one that forced
// them into different workers.
func TestStationRouteIsUnauthenticatedAndStructureRouteIsNot(t *testing.T) {
	scopes := routeSecurityScopes(t)

	if got := scopes[stationDetailPath]; len(got) != 0 {
		t.Errorf("%s now requires %v; it was unauthenticated when it was made a GLOBAL subscription, "+
			"and a scoped global route 403s forever (see TestGlobalRoutesRequireNoScope)", stationDetailPath, got)
	}
	want := "esi-universe.read_structures.v1"
	got := scopes[structureDetailPath]
	if len(got) != 1 || got[0] != want {
		t.Errorf("%s requires %v, want exactly [%s] — the scope is what makes it character-scoped "+
			"and what makes docking-access 403s readable as data", structureDetailPath, got, want)
	}
}

// TestAllianceRoutesAreDispatchedNotFannedOut pins capability #37's shape.
// All four alliance routes take the alliance id as their ONLY path
// parameter, which is exactly what a subscription's entity_id holds — so
// there is no second identifier for a fan-out to enumerate, and treating one
// as a fan-out would be inventing work.
func TestAllianceRoutesAreDispatchedNotFannedOut(t *testing.T) {
	subscribable := SubscribableRoutes()
	fanout := fanoutRoutes()

	for _, path := range []string{
		allianceSheetPath, allianceContactsPath, allianceContactLabelsPath, allianceCorporationsPath,
	} {
		kind, ok := subscribable[path]
		if !ok {
			t.Errorf("%s is not subscribable — capability #37 is unreachable again", path)
			continue
		}
		if kind != sync.EntityAlliance {
			t.Errorf("%s is subscribable as %q, want alliance", path, kind)
		}
		if _, isFanout := fanout[path]; isFanout {
			t.Errorf("%s is registered as a fan-out; its only path parameter is the alliance id, "+
				"which the subscription already carries", path)
		}
		if _, hasHandler := allianceDispatch[path]; !hasHandler {
			t.Errorf("%s has no alliance dispatch entry", path)
		}
	}
}

func joinScopes(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
