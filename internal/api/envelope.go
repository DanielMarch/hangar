package api

import "time"

// Sync is every collection response's (and most detail responses') data
// freshness envelope — SRS §6: "Every collection response carries the
// `_sync` envelope." LastModifiedAt/NextDueAt come from the owning
// app.sync_subscription row; Stale is true once NextDueAt has passed.
//
// BlockedByPin distinguishes "this ESI route is currently gated by
// internal/esi's compatibility pin" from an ordinary not-yet-synced route.
// Per the roadmap's Phase 15 edge cases: "blocked_by_pin data renders as
// unavailable with an explanation, never as an empty list — empty and
// unavailable are different states." A non-nil BlockedByPin is always
// paired with a nil Data (Item) or nil/omitted Data slice (Collection),
// never with an empty-but-present one.
type Sync struct {
	LastModifiedAt *time.Time `json:"last_modified_at"`
	NextDueAt      *time.Time `json:"next_due_at"`
	Stale          bool       `json:"stale"`
	BlockedByPin   *string    `json:"blocked_by_pin,omitempty"`
}

// PageInfo carries the opaque cursors for the adjacent pages plus the
// effective limit. NextCursor/PrevCursor are the ZeroSentinel ("0") when
// there is no further page in that direction — never an empty string, so a
// client that blindly forwards the value back as `after`/`before` always
// gets a well-formed request.
type PageInfo struct {
	NextCursor string `json:"next_cursor"`
	PrevCursor string `json:"prev_cursor"`
	Limit      int32  `json:"limit"`
}

// Collection is the standard response shape for every list endpoint: rows,
// pagination and the freshness envelope together. Data is nil (encodes as
// JSON `null`) only for the blocked_by_pin case; a genuinely empty result
// set is an empty, non-nil slice (`[]`) — the two must never be conflated.
type Collection[T any] struct {
	Data []T      `json:"data"`
	Page PageInfo `json:"page"`
	Sync Sync     `json:"_sync"`
}

// Item is the standard response shape for a single-resource detail route
// that still carries freshness. Data is a pointer specifically so the
// blocked_by_pin case can encode `"data": null` rather than a zero-valued
// struct standing in for "no data".
type Item[T any] struct {
	Data *T   `json:"data"`
	Sync Sync `json:"_sync"`
}

// UnavailableItem builds the blocked_by_pin response for a detail route:
// Data is nil, Sync.BlockedByPin explains why, Stale is forced true (there
// is by definition no fresh answer available).
func UnavailableItem[T any](reason string) Item[T] {
	return Item[T]{Data: nil, Sync: Sync{BlockedByPin: &reason, Stale: true}}
}

// UnavailableCollection is UnavailableItem's collection-route counterpart.
// Data is nil, not an empty slice — see Collection's doc comment.
func UnavailableCollection[T any](reason string) Collection[T] {
	return Collection[T]{Data: nil, Page: PageInfo{Limit: DefaultLimit, NextCursor: ZeroSentinel, PrevCursor: ZeroSentinel}, Sync: Sync{BlockedByPin: &reason, Stale: true}}
}

// EmptyPage is the PageInfo for a single unpaginated (naturally-bounded)
// collection response — both cursors are the sentinel because there is
// exactly one page.
func EmptyPage(limit int32) PageInfo {
	return PageInfo{NextCursor: ZeroSentinel, PrevCursor: ZeroSentinel, Limit: limit}
}
