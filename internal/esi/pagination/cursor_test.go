package pagination

import (
	"context"
	"testing"
)

func TestCursorQueryMutualExclusivity(t *testing.T) {
	after := CursorQuery(After, "abc")
	if after.Get("after") != "abc" || after.Has("before") {
		t.Errorf("After direction must set only after=, got %v", after)
	}
	before := CursorQuery(Before, "xyz")
	if before.Get("before") != "xyz" || before.Has("after") {
		t.Errorf("Before direction must set only before=, got %v", before)
	}
	if after.Get("limit") != "100" || before.Get("limit") != "100" {
		t.Error("HANGAR always requests the full limit (100)")
	}
}

func TestFetchAllCursor(t *testing.T) {
	pages := []CursorPage{
		{Body: []byte("1"), NextCursor: "c1", HasMore: true},
		{Body: []byte("2"), NextCursor: "c2", HasMore: true},
		{Body: []byte("3"), NextCursor: "", HasMore: false},
	}
	var seenCursors []string
	i := 0
	fetch := func(ctx context.Context, dir Direction, cursor string) (CursorPage, error) {
		if dir != After {
			t.Errorf("FetchAllCursor must always fetch After from the start of the set")
		}
		seenCursors = append(seenCursors, cursor)
		p := pages[i]
		i++
		return p, nil
	}

	bodies, err := FetchAllCursor(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(bodies))
	}
	wantCursors := []string{StartOfSet, "c1", "c2"}
	for i, want := range wantCursors {
		if seenCursors[i] != want {
			t.Errorf("cursor[%d] = %q, want %q — cursors must pass through verbatim, never synthesised", i, seenCursors[i], want)
		}
	}
}
