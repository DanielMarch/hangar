package capability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	hangarsync "github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/worker"
)

// repoRoot walks up from the test's working directory until it finds go.mod.
// The three artefacts these tests read — the embedded spec snapshot, the
// recorded ESI responses, and the committed OpenAPI document — all live at
// fixed paths from the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqualf(t, parent, dir, "walked to the filesystem root without finding go.mod")
		dir = parent
	}
}

// ── LINK 1: THE ROUTE IS REACHABLE ───────────────────────────────────────

// requireDispatched asserts every named upstream ESI route is subscribable
// under the given entity kind.
//
// worker.SubscribableRoutes() is DERIVED from the four dispatch tables and
// the fan-out registry (worker/syncset.go), never restated, so "present
// here" means exactly "some worker has a handler for it and the reconciler
// will create a subscription". That is the property whose absence was B30,
// B47 and B48, and it cannot be satisfied by adding a name to a list.
func requireDispatched(t *testing.T, kind hangarsync.EntityKind, paths ...string) {
	t.Helper()
	subscribable := worker.SubscribableRoutes()
	for _, path := range paths {
		got, ok := subscribable[path]
		require.Truef(t, ok,
			"%s is not subscribable: no worker dispatches it, so no subscription can name it and this capability "+
				"produces nothing on every installation (defect class B30/B47/B48)", path)
		require.Equalf(t, kind, got, "%s is subscribable as %q, expected %q", path, got, kind)
	}
}

// requireDeliberatelyUnmapped is requireDispatched's opposite, for the
// routes a capability names and HANGAR deliberately does not poll.
//
// Appendix A lists four routes under capability #36 and HANGAR dispatches
// two: the regional order book and its type list are owner-scoped
// projections over app.market_order, not mirrors of ESI's public feed, and
// mirroring one is a different product (~300,000 live orders for a single
// trade hub). That is a DECISION, recorded with a reason in
// worker/unmapped.go — and a decision nothing asserts decays into an
// oversight, which is what defect B47 was. Asserting the route is classified
// keeps the two apart.
func requireDeliberatelyUnmapped(t *testing.T, paths ...string) {
	t.Helper()
	unmapped := worker.DeliberatelyUnmapped()
	subscribable := worker.SubscribableRoutes()
	for _, path := range paths {
		reason, ok := unmapped[path]
		require.Truef(t, ok, "%s is expected to be recorded as deliberately unmapped, and is not", path)
		require.NotEmptyf(t, reason, "%s is recorded as unmapped with no reason", path)
		require.NotContainsf(t, subscribable, path,
			"%s is both dispatched and recorded as deliberately unmapped — one of the two is a lie", path)
	}
}

// requireNotSubscribable asserts a route deliberately carries no
// subscription of its own. Used where a capability's delivery involves a
// call that cannot be scheduled — a POST with a request body, or a detail
// route whose parent must fetch it — so that "absent" stays a decision.
func requireNotSubscribable(t *testing.T, paths ...string) {
	t.Helper()
	subscribable := worker.SubscribableRoutes()
	for _, path := range paths {
		require.NotContainsf(t, subscribable, path,
			"%s must NOT be subscribable: a subscription for it would be a polling schedule for a call that "+
				"cannot be polled", path)
	}
}

// ── LINK 2: THE DTO MATCHES THE SPEC ─────────────────────────────────────

type specDoc struct {
	Paths map[string]map[string]struct {
		Responses map[string]struct {
			Content map[string]struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	} `json:"paths"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

var (
	specOnce   sync.Once
	specLoaded *specDoc
	specErr    error
)

func loadSpec(t *testing.T) *specDoc {
	t.Helper()
	specOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "esi", "catalogue", "embedded", "openapi.snapshot.json"))
		if err != nil {
			specErr = err
			return
		}
		var doc specDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			specErr = err
			return
		}
		specLoaded = &doc
	})
	require.NoError(t, specErr, "loading the embedded ESI spec snapshot")
	require.NotNil(t, specLoaded)
	require.NotEmpty(t, specLoaded.Paths, "the embedded snapshot yielded no paths — every check would pass vacuously")
	return specLoaded
}

// schemaNode is the subset of JSON Schema these tests navigate.
type schemaNode struct {
	Ref        string                     `json:"$ref"`
	Type       string                     `json:"type"`
	Items      json.RawMessage            `json:"items"`
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// resolve follows $ref and descends through arrays until it reaches the
// OBJECT whose properties a DTO must carry. An envelope field (the projects
// listing's "projects", the killmail victim's nesting) is named by the
// caller when the DTO models an inner object rather than the whole body.
func resolve(t *testing.T, doc *specDoc, raw json.RawMessage, envelope string) schemaNode {
	t.Helper()
	for range 12 { // bounded: a self-referential $ref chain must fail, not hang
		var node schemaNode
		require.NoError(t, json.Unmarshal(raw, &node))
		switch {
		case node.Ref != "":
			name := strings.TrimPrefix(node.Ref, "#/components/schemas/")
			next, ok := doc.Components.Schemas[name]
			require.Truef(t, ok, "spec references #/components/schemas/%s, which is absent", name)
			raw = next
		case envelope != "" && node.Properties[envelope] != nil:
			raw = node.Properties[envelope]
			envelope = ""
		case node.Type == "array" && len(node.Items) > 0:
			raw = node.Items
		default:
			return node
		}
	}
	t.Fatalf("spec schema $ref chain did not terminate")
	return schemaNode{}
}

// responseFields returns the property names a DTO for this route's 200
// response must carry, and whether the spec itself marked them required.
//
// ── WHEN THE SPEC MARKS NOTHING REQUIRED, THE BAR GOES UP, NOT AWAY ──────
// A handful of ESI schemas declare properties and no `required` list at all
// (the calendar summary is one). Skipping the check for those would leave
// the capability test looking checked while asserting nothing — the exact
// failure mode B51 is about. So in that case EVERY declared property must be
// present in the DTO instead: with no guarantee from the spec about which
// fields arrive, the only safe assumption is that all of them can, and a DTO
// that drops one drops data ESI really sends.
func responseFields(t *testing.T, upstreamPath, envelope string) (fields []string, wereRequired bool) {
	t.Helper()
	doc := loadSpec(t)
	ops, ok := doc.Paths[upstreamPath]
	require.Truef(t, ok, "the embedded spec snapshot has no path %s", upstreamPath)
	op, ok := ops["get"]
	require.Truef(t, ok, "the embedded spec snapshot has no GET operation for %s", upstreamPath)
	resp, ok := op.Responses["200"]
	require.Truef(t, ok, "%s declares no 200 response", upstreamPath)
	content, ok := resp.Content["application/json"]
	require.Truef(t, ok, "%s's 200 response declares no application/json content", upstreamPath)

	node := resolve(t, doc, content.Schema, envelope)
	if len(node.Required) > 0 {
		return node.Required, true
	}
	for name := range node.Properties {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	require.NotEmptyf(t, fields,
		"%s's 200 response schema declares neither required nor optional properties — the DTO check would "+
			"pass vacuously, so the capability test naming this route must assert values instead", upstreamPath)
	return fields, false
}

// requireDTOCoversSpec is defect B50's guard, generalised.
//
// B50 was a DTO whose field names matched nothing ESI sends, discovered only
// by reading the spec next to the struct. This asserts the comparison
// mechanically: every property the spec marks required on the route's 200
// response must appear as a json tag somewhere in dto's type, at any nesting
// depth.
//
// It checks REQUIRED properties only, deliberately. An optional property a
// DTO omits is a scope decision — app.location has no column for a station's
// reprocessing efficiency and never will — whereas a required property a DTO
// omits means the handler cannot represent a response ESI is guaranteed to
// send.
//
// ── OMISSIONS ARE ALLOWED, AND HAVE TO BE WRITTEN DOWN ───────────────────
// A few DTOs deliberately drop a required property because the value is
// already held elsewhere: a calendar event's detail body repeats the title,
// date and importance the LIST route already landed, and the fan-out
// supplies event_id from the request path rather than the body. Passing
// those names as `omit` is how a test says so out loud. An omit entry that
// is NOT in the spec's field list fails — so the escape hatch cannot outlive
// the spec that justified it, which is the same rule the reachability
// allowlist lives by.
func requireDTOCoversSpec(t *testing.T, upstreamPath, envelope string, dto any, omit ...string) {
	t.Helper()
	fields, wereRequired := responseFields(t, upstreamPath, envelope)
	tags := jsonTagsOf(reflect.TypeOf(dto))

	declared := map[string]bool{}
	for _, f := range fields {
		declared[f] = true
	}
	excused := map[string]bool{}
	for _, o := range omit {
		require.Truef(t, declared[o],
			"%s: the test excuses %q, which the spec does not declare on this response — a stale omission "+
				"hides a real one", upstreamPath, o)
		excused[o] = true
	}

	var absent []string
	for _, field := range fields {
		if !tags[field] && !excused[field] {
			absent = append(absent, field)
		}
	}
	qualifier := "declares (and marks none required, so all of them count)"
	if wereRequired {
		qualifier = "marks REQUIRED"
	}
	require.Emptyf(t, absent,
		"%s: the DTO does not carry %v, which the live spec %s on this route's 200 response. "+
			"A DTO that cannot represent a field ESI sends silently drops it (defect B50). DTO json tags: %v",
		upstreamPath, absent, qualifier, sortedKeys(tags))
}

// jsonTagsOf collects every json tag name in a type, following slices,
// pointers, maps and nested structs.
func jsonTagsOf(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	var walk func(reflect.Type, int)
	walk = func(t reflect.Type, depth int) {
		if t == nil || depth > 8 {
			return
		}
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(t.Elem(), depth+1)
			return
		case reflect.Struct:
			for i := range t.NumField() {
				f := t.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					out[name] = true
				}
				walk(f.Type, depth+1)
			}
		}
	}
	walk(typ, 0)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── LINK 3: THE RECORDED RESPONSE PARSES ─────────────────────────────────

// fixture reads a captured ESI response from testdata/esi.
//
// These are real bodies recorded off Tranquility, not hand-written examples,
// which is what makes asserting values on them worth doing: a spec-shaped
// literal only proves the DTO agrees with whoever wrote the literal.
func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "testdata", "esi", rel))
	require.NoErrorf(t, err, "reading recorded ESI response testdata/esi/%s", rel)
	require.NotEmptyf(t, raw, "testdata/esi/%s is empty", rel)
	return raw
}

// parsed runs a Parse* function over a recorded fixture and fails loudly if
// it errors. The returned value is asserted on by the caller — a parse that
// merely succeeds proves nothing about whether the DTO understood the body.
func parsed[T any](t *testing.T, rel string, parse func([]byte) (T, error)) T {
	t.Helper()
	got, err := parse(fixture(t, rel))
	require.NoErrorf(t, err, "parsing testdata/esi/%s", rel)
	return got
}

// ── LINK 4: THE DATA IS SERVED ───────────────────────────────────────────

var (
	endpointOnce sync.Once
	endpointSet  map[string]bool
	endpointErr  error
)

// requireEndpoints asserts every named /api/v1 path is registered in the
// committed OpenAPI document — the contract `make verify-generated` proves
// is in step with the Huma router and that web/src/api/schema.d.ts is
// generated from.
//
// A path may be named verbatim or as a proper prefix of a registered path,
// the same rule tools/gate4-traceability/capability_endpoints.go applies and
// for the same reason: "/api/v1/admin/sync" names a board served as three
// sibling paths.
func requireEndpoints(t *testing.T, paths ...string) {
	t.Helper()
	endpointOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "openapi.json"))
		if err != nil {
			endpointErr = err
			return
		}
		var doc struct {
			Paths map[string]json.RawMessage `json:"paths"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			endpointErr = err
			return
		}
		endpointSet = map[string]bool{}
		for p := range doc.Paths {
			endpointSet[p] = true
		}
	})
	require.NoError(t, endpointErr, "reading docs/openapi.json")
	require.NotEmpty(t, endpointSet, "docs/openapi.json declares no paths")

	for _, want := range paths {
		if endpointSet[want] {
			continue
		}
		found := false
		for p := range endpointSet {
			if strings.HasPrefix(p, want+"/") {
				found = true
				break
			}
		}
		require.Truef(t, found,
			"%s is registered in docs/openapi.json neither verbatim nor as a path prefix — the capability's data "+
				"has nowhere to be read from (defect class B52)", want)
	}
}

// ── a small assertion used by several tests ──────────────────────────────

// requireLen fails with the collection's own contents in the message, which
// is what makes a fixture drift diagnosable rather than a bare count
// mismatch.
func requireLen[T any](t *testing.T, got []T, want int, what string) {
	t.Helper()
	require.Lenf(t, got, want, "%s: recorded fixture yielded %d, expected %d (%v)", what, len(got), want, fmt.Sprintf("%v", got))
}
