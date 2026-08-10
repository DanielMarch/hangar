// Package store is the repository facade over internal/store/gen (sqlc's
// generated, mechanical query layer). A generated Queries method is
// reachable directly for callers that only need a single row or page; this
// package exists for the cross-cutting behaviour sqlc cannot generate:
// partition maintenance, the recursive asset-tree walk's Go-side
// conversion, and anything that needs a domain type rather than a raw
// gen.* row. `pgtype.Numeric`/gen.* types are acceptable at this boundary
// (02_DATABASE_SCHEMA.md §3.1) but must not escape further up the stack.
package store

import (
	"github.com/hangar-project/hangar/internal/store/gen"
)

// Store embeds the generated Queries so every sqlc query is reachable as
// store.Store.<Method>, and layers facade methods (asset.go, partition.go)
// on top for the behaviour that isn't a single mechanical query.
type Store struct {
	*gen.Queries
	db gen.DBTX
}

// New wraps a pgx-compatible handle (pool or transaction) in a Store.
func New(db gen.DBTX) *Store {
	return &Store{Queries: gen.New(db), db: db}
}

// DBTX exposes the handle a Store was built on. Every gen.Queries method is
// already reachable directly on Store; this exists for the rare caller that
// needs the raw handle itself rather than a query result — e.g. River's
// InsertTx(ctx, tx, args, opts), which needs the underlying pgx.Tx to
// enqueue a job in the SAME transaction as a Store-mediated write
// (internal/provisioning's urgent-revocation path, Phase 11). Inside
// store.WithTx, a type assertion `s.DBTX().(pgx.Tx)` always succeeds — the
// handle WithTx builds Store from is exactly the transaction it opened.
func (s *Store) DBTX() gen.DBTX {
	return s.db
}
