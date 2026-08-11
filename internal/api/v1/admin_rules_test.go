package v1

import (
	"testing"

	"github.com/google/uuid"
)

// TestRuleSetPreviewTokenBindsPlatformAndRules is the server-side half of
// Phase 18's TestRuleEditorRequiresPreviewConfirmation. The browser half
// (a disabled Save button until the operator confirms a preview) is
// verified by web/e2e/rule-editor.spec.ts; this is what makes the rule
// unbypassable by a client that never renders the editor.
//
// The token must change whenever anything about WHAT WOULD BE SAVED
// changes, and must not change for a re-ordering of the same set.
func TestRuleSetPreviewTokenBindsPlatformAndRules(t *testing.T) {
	platform := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	other := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	groupA := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	groupB := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")

	base := []RuleInput{
		{SourceKind: "role", SourceRef: "r-1", GroupID: groupA, Effect: "grant"},
		{SourceKind: "corporation", SourceRef: "98000001", GroupID: groupB, Effect: "deny"},
	}
	token := RuleSetPreviewToken(platform, base)

	if token == "" {
		t.Fatal("empty token")
	}

	t.Run("stable for the same set", func(t *testing.T) {
		if got := RuleSetPreviewToken(platform, base); got != token {
			t.Errorf("token changed for an identical rule set")
		}
	})

	t.Run("order-insensitive", func(t *testing.T) {
		// A rule set is a SET. Re-ordering rows in the editor must not
		// invalidate a preview that already showed their exact effect —
		// otherwise the gate trains operators to preview twice and click
		// through, which is how a gate stops being read.
		reordered := []RuleInput{base[1], base[0]}
		if got := RuleSetPreviewToken(platform, reordered); got != token {
			t.Errorf("token changed for a re-ordering of the same rules")
		}
	})

	t.Run("changes when a rule changes", func(t *testing.T) {
		// This is the case that matters: previewing something harmless and
		// saving something else must be impossible.
		cases := map[string][]RuleInput{
			"effect flipped": {
				{SourceKind: "role", SourceRef: "r-1", GroupID: groupA, Effect: "deny"},
				base[1],
			},
			"different group": {
				{SourceKind: "role", SourceRef: "r-1", GroupID: groupB, Effect: "grant"},
				base[1],
			},
			"different source ref": {
				{SourceKind: "role", SourceRef: "r-2", GroupID: groupA, Effect: "grant"},
				base[1],
			},
			"different source kind": {
				{SourceKind: "squad", SourceRef: "r-1", GroupID: groupA, Effect: "grant"},
				base[1],
			},
			"rule removed": {base[0]},
			"rule added": append(append([]RuleInput{}, base...),
				RuleInput{SourceKind: "public", SourceRef: "", GroupID: groupA, Effect: "grant"}),
			"empty set": {},
		}
		for name, rules := range cases {
			if got := RuleSetPreviewToken(platform, rules); got == token {
				t.Errorf("%s: token unchanged — a save could reuse a preview of a different rule set", name)
			}
		}
	})

	t.Run("bound to the platform", func(t *testing.T) {
		// Without this, a preview taken on a test platform would authorise
		// the same rule set on a production one.
		if got := RuleSetPreviewToken(other, base); got == token {
			t.Errorf("token is not bound to the platform")
		}
	})

	t.Run("field boundaries are not confusable", func(t *testing.T) {
		// Concatenating fields without a separator would let a rule whose
		// source_kind/source_ref split differently hash identically.
		a := []RuleInput{{SourceKind: "role", SourceRef: "ab", GroupID: groupA, Effect: "grant"}}
		b := []RuleInput{{SourceKind: "rolea", SourceRef: "b", GroupID: groupA, Effect: "grant"}}
		if RuleSetPreviewToken(platform, a) == RuleSetPreviewToken(platform, b) {
			t.Errorf("field boundaries collide")
		}
	})
}
