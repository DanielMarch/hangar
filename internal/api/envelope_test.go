package api

import (
	"encoding/json"
	"testing"
)

// TestBlockedByPinRendersUnavailableNotEmpty — Phase 15 exit criterion:
// "_sync.blocked_by_pin set, data explicitly unavailable". Proves the two
// states are distinguishable on the wire: an unavailable collection
// encodes `"data": null` (this test), a genuinely empty one encodes
// `"data": []` (TestEmptyCollectionIsNotUnavailable below) — never the
// same JSON shape for two different real-world states.
func TestBlockedByPinRendersUnavailableNotEmpty(t *testing.T) {
	reason := "route blocked by the compatibility pin"
	c := UnavailableCollection[map[string]any](reason)

	if c.Sync.BlockedByPin == nil || *c.Sync.BlockedByPin != reason {
		t.Fatalf("expected Sync.BlockedByPin to be set to %q, got %v", reason, c.Sync.BlockedByPin)
	}
	if !c.Sync.Stale {
		t.Fatal("an unavailable collection must report Stale=true — there is no fresh answer available")
	}
	if c.Data != nil {
		t.Fatalf("expected Data to be nil (unavailable), got %v", c.Data)
	}

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}
	if decoded["data"] != nil {
		t.Fatalf(`expected "data": null on the wire, got %v (raw: %s)`, decoded["data"], raw)
	}
	sync, ok := decoded["_sync"].(map[string]any)
	if !ok {
		t.Fatalf("expected a _sync object, got %v", decoded["_sync"])
	}
	if sync["blocked_by_pin"] != reason {
		t.Fatalf("expected _sync.blocked_by_pin = %q, got %v", reason, sync["blocked_by_pin"])
	}

	item := UnavailableItem[map[string]any](reason)
	rawItem, _ := json.Marshal(item)
	var decodedItem map[string]any
	_ = json.Unmarshal(rawItem, &decodedItem)
	if decodedItem["data"] != nil {
		t.Fatalf(`Item: expected "data": null, got %v`, decodedItem["data"])
	}
}

// TestEmptyCollectionIsNotUnavailable is the converse proof: a
// synced-but-zero-rows collection encodes "data": [], never null, and
// never sets blocked_by_pin — empty and unavailable are different states
// (roadmap Phase 15 edge cases), and this is what keeps them different on
// the wire.
func TestEmptyCollectionIsNotUnavailable(t *testing.T) {
	c := Collection[map[string]any]{Data: []map[string]any{}, Page: EmptyPage(0), Sync: Sync{}}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}
	data, ok := decoded["data"].([]any)
	if !ok {
		t.Fatalf(`expected "data": [] (an array), got %v (raw: %s)`, decoded["data"], raw)
	}
	if len(data) != 0 {
		t.Fatalf("expected zero rows, got %d", len(data))
	}
	sync := decoded["_sync"].(map[string]any)
	if _, present := sync["blocked_by_pin"]; present && sync["blocked_by_pin"] != nil {
		t.Fatalf("a genuinely empty collection must not set blocked_by_pin, got %v", sync["blocked_by_pin"])
	}
}
