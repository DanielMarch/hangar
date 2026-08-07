package scopes_test

import (
	"context"
	"testing"

	"github.com/hangar-project/hangar/internal/scopes"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	seen map[string]int
}

func newFakeStore() *fakeStore { return &fakeStore{seen: map[string]int{}} }

func (f *fakeStore) UpsertEsiScope(ctx context.Context, scope string) error {
	f.seen[scope]++
	return nil
}

// TestScopeCatalogueIngestsBothGrammars (roadmap exit criterion): both live
// grammars ingest, and an adversarial third, invented grammar also
// ingests — proving nothing here parses, regexes, or pattern-checks a
// scope string.
func TestScopeCatalogueIngestsBothGrammars(t *testing.T) {
	store := newFakeStore()

	grammar1 := "esi-characters.read_contacts.v1"          // legacy dotted/versioned form
	grammar2 := "esi.activity.char:read"                   // 2026-08-04 colon-suffixed form
	adversarial := "🚀completely::unexpected//shape v9!!!;" // an invented third grammar

	err := scopes.Ingest(context.Background(), store, []string{grammar1, grammar2, adversarial})
	require.NoError(t, err, "no scope string, however unexpected its shape, may be rejected")

	require.Equal(t, 1, store.seen[grammar1])
	require.Equal(t, 1, store.seen[grammar2])
	require.Equal(t, 1, store.seen[adversarial], "an adversarial, invented grammar must round-trip untouched")
}

func TestScopeCatalogueSkipsEmptyWithoutFailingBatch(t *testing.T) {
	store := newFakeStore()
	err := scopes.Ingest(context.Background(), store, []string{"a.scope", "", "b.scope"})
	require.NoError(t, err)
	require.Equal(t, 1, store.seen["a.scope"])
	require.Equal(t, 1, store.seen["b.scope"])
	require.NotContains(t, store.seen, "")
}

func TestScopeSetMembershipAndMissing(t *testing.T) {
	granted := scopes.NewSet([]string{"esi-characters.read_contacts.v1", "esi.activity.char:read"})
	require.True(t, granted.Has("esi-characters.read_contacts.v1"))
	require.False(t, granted.Has("esi-characters.read_mail.v1"))

	missing := granted.Missing([]string{"esi-characters.read_contacts.v1", "esi-characters.read_mail.v1"})
	require.Equal(t, []string{"esi-characters.read_mail.v1"}, missing)

	require.True(t, scopes.NeedsReauthorization(granted, []string{"esi-characters.read_mail.v1"}))
	require.False(t, scopes.NeedsReauthorization(granted, []string{"esi.activity.char:read"}))
}

func TestMergeScopesDeduplicatesAdditively(t *testing.T) {
	merged := scopes.MergeScopes(
		[]string{"a", "b"},
		[]string{"b", "c"},
	)
	require.ElementsMatch(t, []string{"a", "b", "c"}, merged)
}
