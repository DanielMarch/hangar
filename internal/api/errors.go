package api

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/hangar-project/hangar/internal/api/filters"
)

// ErrActingCharacterRequired is SRS §6.7 / roadmap Phase 15's specific
// error for POST /api/v1/support/search when the caller's session has no
// resolved acting character: "An unauthenticated or character-less session
// hitting /support/search gets a specific error explaining the
// acting-character requirement, not a generic 403." internal/api/v1's
// search handler returns this directly (never wrapped into a bare 403) so
// a client can render the exact explanation.
var ErrActingCharacterRequired = huma.Error400BadRequest(
	"an acting character must be selected before searching — link a character and select it as active, then retry",
)

// PageError translates ParsePageRequest's sentinel errors into the
// HTTP-facing status huma expects. Every case is a client error (never
// 500): a malformed or conflicting cursor is the caller's mistake, not the
// server's.
func PageError(err error) error {
	switch {
	case errors.Is(err, ErrCursorBothDirections):
		return huma.Error400BadRequest("`after` and `before` are mutually exclusive; supply at most one", err)
	case errors.Is(err, ErrCursorLimitOutOfRange):
		return huma.Error400BadRequest("`limit` must be between 10 and 100 (default 50)", err)
	case errors.Is(err, ErrCursorMalformed):
		return huma.Error400BadRequest("cursor is malformed", err)
	default:
		return huma.Error400BadRequest("invalid pagination parameters", err)
	}
}

// FilterError translates filters.Validate's rejection into a 422 —
// "Adversarial filters: unknown fields, SQL fragments, type-confused
// values must produce 422 — never 500, and never a successful query"
// (roadmap Phase 15 edge cases).
func FilterError(err error) error {
	var invalid *filters.ErrInvalidFilter
	if errors.As(err, &invalid) {
		return huma.Error422UnprocessableEntity("filter rejected: "+invalid.Error(), err)
	}
	return huma.Error422UnprocessableEntity("filter rejected", err)
}

// NotFound is the shared 404 helper — kept here so every v1 handler wraps
// pgx.ErrNoRows the same way instead of hand-rolling the message.
func NotFound(resource string) error {
	return huma.Error404NotFound(resource + " not found")
}

// Forbidden is the shared 403 helper for RBAC-visible-but-not-permitted
// cases that aren't middleware.RequirePermission's blanket route guard
// (e.g. support/search's "restrict results to entities the caller can
// already see under RBAC" per-row filter).
func Forbidden(msg string) error {
	return huma.Error403Forbidden(msg)
}

// RateLimited is the shared 429 helper.
func RateLimited(msg string) error {
	return huma.Error429TooManyRequests(msg)
}

// Internal wraps an unexpected error as a 500 without leaking internal
// detail into the message (the wrapped err still reaches structured logs
// via huma's error handling, just not the HTTP body).
func Internal(context string, err error) error {
	return huma.Error500InternalServerError(context, err)
}
