package api

import (
	"errors"
	"testing"
)

// TestCursorRejectsAfterAndBefore — Phase 15 exit criterion: "both
// supplied ⇒ client error".
func TestCursorRejectsAfterAndBefore(t *testing.T) {
	_, err := ParsePageRequest("some-cursor", "another-cursor", nil)
	if err == nil {
		t.Fatal("expected an error when both after and before are supplied")
	}
	if !errors.Is(err, ErrCursorBothDirections) {
		t.Fatalf("expected ErrCursorBothDirections, got %v", err)
	}
}

// TestCursorLimitBounds — Phase 15 exit criterion: "< 10 and > 100
// rejected; default 50".
func TestCursorLimitBounds(t *testing.T) {
	tooSmall := int32(9)
	if _, err := ParsePageRequest("", "", &tooSmall); !errors.Is(err, ErrCursorLimitOutOfRange) {
		t.Fatalf("limit=9: expected ErrCursorLimitOutOfRange, got %v", err)
	}
	tooBig := int32(101)
	if _, err := ParsePageRequest("", "", &tooBig); !errors.Is(err, ErrCursorLimitOutOfRange) {
		t.Fatalf("limit=101: expected ErrCursorLimitOutOfRange, got %v", err)
	}
	atFloor := int32(10)
	if _, err := ParsePageRequest("", "", &atFloor); err != nil {
		t.Fatalf("limit=10 (floor): expected no error, got %v", err)
	}
	atCeiling := int32(100)
	if _, err := ParsePageRequest("", "", &atCeiling); err != nil {
		t.Fatalf("limit=100 (ceiling): expected no error, got %v", err)
	}
	req, err := ParsePageRequest("", "", nil)
	if err != nil {
		t.Fatalf("no limit supplied: unexpected error %v", err)
	}
	if req.Limit != DefaultLimit {
		t.Fatalf("no limit supplied: expected default %d, got %d", DefaultLimit, req.Limit)
	}
}

// TestCursorZeroSentinelBothDirections — Phase 15 exit criterion: "'0' =
// start-of-set with after, end-of-set with before".
func TestCursorZeroSentinelBothDirections(t *testing.T) {
	fwd, err := ParsePageRequest(ZeroSentinel, "", nil)
	if err != nil {
		t.Fatalf("after=0: unexpected error %v", err)
	}
	if fwd.Direction != Forward {
		t.Fatalf("after=0: expected Forward direction, got %v", fwd.Direction)
	}
	if fwd.Cursor != nil {
		t.Fatalf("after=0: expected nil cursor (start-of-set), got %v", fwd.Cursor)
	}

	back, err := ParsePageRequest("", ZeroSentinel, nil)
	if err != nil {
		t.Fatalf("before=0: unexpected error %v", err)
	}
	if back.Direction != Backward {
		t.Fatalf("before=0: expected Backward direction, got %v", back.Direction)
	}
	if back.Cursor != nil {
		t.Fatalf("before=0: expected nil cursor (end-of-set), got %v", back.Cursor)
	}

	// The sentinel round-trips: an empty keyset always re-encodes to "0",
	// in either direction.
	if got := EncodeCursor(nil); got != ZeroSentinel {
		t.Fatalf("EncodeCursor(nil) = %q, want %q", got, ZeroSentinel)
	}
}

func TestCursorMalformedRejected(t *testing.T) {
	if _, err := ParsePageRequest("not-valid-base64!!!", "", nil); !errors.Is(err, ErrCursorMalformed) {
		t.Fatalf("expected ErrCursorMalformed, got %v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	ks := Keyset{"item_id": float64(12345)}
	encoded := EncodeCursor(ks)
	if encoded == ZeroSentinel {
		t.Fatal("a non-empty keyset must not encode to the sentinel")
	}
	req, err := ParsePageRequest(encoded, "", nil)
	if err != nil {
		t.Fatalf("decoding own-encoded cursor: %v", err)
	}
	if req.Cursor["item_id"] != float64(12345) {
		t.Fatalf("round-tripped keyset mismatch: %v", req.Cursor)
	}
}
