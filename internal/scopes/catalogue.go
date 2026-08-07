// Package scopes implements 01_ARCHITECTURE.md §7.5's opaque scope
// catalogue. Scopes are text primary keys read verbatim from the spec's
// securitySchemes and each operation's security block, or from a token's
// scp claim. HANGAR must never parse, validate, version-match, or
// pattern-check a scope string — two grammars are live simultaneously
// (esi-characters.read_contacts.v1 and esi.activity.char:read), and any
// regex, any strings.Split assuming a fixed part count, any HasPrefix
// check is a defect this package exists to make impossible to write by
// accident: every function here takes a scope as an opaque string and
// does nothing with its internal structure.
package scopes

import "context"

// Store is the subset of gen.Querier this package needs.
type Store interface {
	UpsertEsiScope(ctx context.Context, scope string) error
}

// Ingest records every scope in scopeList as seen, first-seen-wins,
// exactly as handed back by ESI — no normalisation, no case-folding, no
// trimming beyond what the caller already did. Unknown scopes are
// ingested and surfaced (via ListUnacknowledgedEsiScopes /
// GET /api/v1/admin/scopes/unknown in a later phase), never rejected
// (Principle 14).
func Ingest(ctx context.Context, store Store, scopeList []string) error {
	for _, s := range scopeList {
		if s == "" {
			continue // an empty scp element is not a scope; skip without erroring the whole batch
		}
		if err := store.UpsertEsiScope(ctx, s); err != nil {
			return err
		}
	}
	return nil
}
