//go:build integration

package v1_test

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// TestKeysetPagesAreTotallyOrdered is defect B46's regression test.
//
// ── WHAT B46 ACTUALLY WAS ────────────────────────────────────────────────
// Five paginated endpoints share one query shape: "the newest N rows for
// this owner, before the row the caller last saw", ordered by a timestamp.
// Four of them wrote that as an UNCAST row comparison —
//
//	(date, journal_id) < (sqlc.arg(before_date), sqlc.arg(before_journal_id))
//
// — and sqlc types a row comparison's right-hand side from the leftmost
// column it can resolve, applying that one type to every argument in the
// tuple. Both parameters therefore generated as time.Time, including the one
// bound to a bigint column. The call sites had no choice but to pass the
// same time.Time twice in order to compile, and Postgres rejected it on
// arrival with `invalid input syntax for type bigint: "9999-01-01 00:00:00
// +0000 UTC" (SQLSTATE 22P02)`.
//
// So GET wallet/journal, wallet/transactions (character AND corporation) and
// mail returned 500 on EVERY call, cursor or no cursor, from Phase 15 until
// Phase 20.6. Not a regression — a screen nobody had ever seen work. The
// fifth, notifications, had a single timestamp argument, typed correctly,
// compiled, ran, and was broken a different way (see below).
//
// ── WHY THIS TEST IS ABOUT ORDERING AND NOT ABOUT THE 500 ────────────────
// Asserting "does not return an error" would pass against a query that
// serves the same page forever, and the whole reason this defect survived
// four phases is that nothing ever executed these queries at all. So the
// assertion is the property a keyset page must actually have:
//
//	paging all the way through, with any page size, yields every row
//	exactly once, in the declared order.
//
// That fails if the arguments are mistyped (error), if the tiebreak is
// missing (rows sharing a timestamp are skipped or repeated at a page
// boundary), or if the cursor carries too little to name a row.
//
// ── THE TIES ARE REAL, NOT SYNTHETIC ─────────────────────────────────────
// Every fixture below deliberately puts several rows on ONE timestamp,
// because ESI does. The live installation this was found on has exactly this
// shape in app.wallet_journal: journal_id 25893163003 and 25893163002 both
// stamped 2026-08-06T17:10:55, a corporation registration fee and its
// matching credit. With the old date-only cursor, a page boundary landing
// between those two dropped the second one on the floor.
func TestKeysetPagesAreTotallyOrdered(t *testing.T) {
	ctx := context.Background()
	pool := newUnknownBoardsPool(t)
	s := store.New(pool)

	const characterID = int64(2124613505)
	_, err := pool.Exec(ctx,
		`INSERT INTO app.character (character_id, name, owner_hash) VALUES ($1, 'CEODude', 'h')`, characterID)
	require.NoError(t, err)

	// base is deliberately not "now": these tables are PARTITION BY RANGE on
	// their time column with only a DEFAULT partition, and a fixed instant
	// keeps the test independent of when it runs.
	base := time.Date(2026, 8, 6, 17, 0, 0, 0, time.UTC)

	// stamps assigns 12 rows to 5 distinct instants, so three of the
	// instants carry a tie and no page size divides the set evenly.
	stamps := func(i int) time.Time { return base.Add(time.Duration(i/3) * time.Minute) }

	t.Run("wallet journal", func(t *testing.T) {
		for i := range 12 {
			_, err := pool.Exec(ctx, `
				INSERT INTO app.wallet_journal
				    (owner_kind, owner_id, division, journal_id, ref_type, description, date)
				VALUES ('character', $1, 1, $2, 'player_donation', 'd', $3)`,
				characterID, int64(25893163000+i), stamps(i))
			require.NoError(t, err)
		}

		for _, pageSize := range []int32{1, 2, 5, 12} {
			t.Run(fmt.Sprintf("page size %d", pageSize), func(t *testing.T) {
				beforeDate, beforeID := farFutureTest, int64(math.MaxInt64)
				var seen []int64
				for range 100 {
					rows, err := s.ListWalletJournalPage(ctx, gen.ListWalletJournalPageParams{
						OwnerKind: "character", OwnerID: characterID, Division: 1,
						BeforeDate: beforeDate, BeforeJournalID: beforeID, PageSize: pageSize,
					})
					require.NoError(t, err, "B46: this errored with SQLSTATE 22P02 on every call before 20.6")
					if len(rows) == 0 {
						break
					}
					for _, r := range rows {
						seen = append(seen, r.JournalID)
					}
					last := rows[len(rows)-1]
					beforeDate, beforeID = last.Date, last.JournalID
				}
				requireStrictlyDescending(t, seen, 12)
			})
		}
	})

	t.Run("wallet transactions", func(t *testing.T) {
		for i := range 12 {
			_, err := pool.Exec(ctx, `
				INSERT INTO app.wallet_transaction
				    (owner_kind, owner_id, division, transaction_id, date, is_buy,
				     location_id, quantity, type_id, unit_price)
				VALUES ('character', $1, 1, $2, $3, false, 60003760, 1, 587, 1.00)`,
				characterID, int64(7100000000+i), stamps(i))
			require.NoError(t, err)
		}

		for _, pageSize := range []int32{1, 5, 12} {
			t.Run(fmt.Sprintf("page size %d", pageSize), func(t *testing.T) {
				beforeDate, beforeID := farFutureTest, int64(math.MaxInt64)
				var seen []int64
				for range 100 {
					rows, err := s.ListWalletTransactionsPage(ctx, gen.ListWalletTransactionsPageParams{
						OwnerKind: "character", OwnerID: characterID, Division: 1,
						BeforeDate: beforeDate, BeforeTransactionID: beforeID, PageSize: pageSize,
					})
					require.NoError(t, err)
					if len(rows) == 0 {
						break
					}
					for _, r := range rows {
						seen = append(seen, r.TransactionID)
					}
					last := rows[len(rows)-1]
					beforeDate, beforeID = last.Date, last.TransactionID
				}
				requireStrictlyDescending(t, seen, 12)
			})
		}
	})

	t.Run("mail headers", func(t *testing.T) {
		for i := range 12 {
			_, err := pool.Exec(ctx, `
				INSERT INTO app.mail_header (character_id, mail_id, from_id, subject, sent_at)
				VALUES ($1, $2, 90000001, 's', $3)`,
				characterID, int64(440000000+i), stamps(i))
			require.NoError(t, err)
		}

		for _, pageSize := range []int32{1, 5, 12} {
			t.Run(fmt.Sprintf("page size %d", pageSize), func(t *testing.T) {
				beforeAt, beforeID := farFutureTest, int64(math.MaxInt64)
				var seen []int64
				for range 100 {
					rows, err := s.ListMailHeadersPage(ctx, gen.ListMailHeadersPageParams{
						CharacterID: characterID, BeforeSentAt: beforeAt,
						BeforeMailID: beforeID, PageSize: pageSize,
					})
					require.NoError(t, err)
					if len(rows) == 0 {
						break
					}
					for _, r := range rows {
						seen = append(seen, r.MailID)
					}
					last := rows[len(rows)-1]
					beforeAt, beforeID = last.SentAt, last.MailID
				}
				requireStrictlyDescending(t, seen, 12)
			})
		}
	})

	// Notifications never returned 500 — a lone timestamptz argument types
	// correctly — so it is the one member of the family that would have been
	// declared healthy by any check that only looked at status codes. It had
	// no tiebreak at all: `sent_at < $2` with `ORDER BY sent_at DESC`, over
	// data ESI delivers in same-second batches.
	t.Run("character notifications", func(t *testing.T) {
		for i := range 12 {
			_, err := pool.Exec(ctx, `
				INSERT INTO app.character_notification
				    (character_id, notification_id, sent_at, type)
				VALUES ($1, $2, $3, 'CorpAppNewMsg')`,
				characterID, int64(3000000000+i), stamps(i))
			require.NoError(t, err)
		}

		for _, pageSize := range []int32{1, 5, 12} {
			t.Run(fmt.Sprintf("page size %d", pageSize), func(t *testing.T) {
				beforeAt, beforeID := farFutureTest, int64(math.MaxInt64)
				var seen []int64
				for range 100 {
					rows, err := s.ListCharacterNotificationsPage(ctx, gen.ListCharacterNotificationsPageParams{
						CharacterID: characterID, BeforeSentAt: beforeAt,
						BeforeNotificationID: beforeID, PageSize: pageSize,
					})
					require.NoError(t, err)
					if len(rows) == 0 {
						break
					}
					for _, r := range rows {
						seen = append(seen, r.NotificationID)
					}
					last := rows[len(rows)-1]
					beforeAt, beforeID = last.SentAt, last.NotificationID
				}
				requireStrictlyDescending(t, seen, 12)
			})
		}
	})
}

// farFutureTest mirrors the handler package's start-of-set sentinel. It is
// duplicated rather than exported: the sentinel is an internal detail of the
// handlers, and a test that imported it would agree with the implementation
// by construction even if both were wrong.
var farFutureTest = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// requireStrictlyDescending is the whole assertion. Strictly descending over
// the id proves three things at once, given the ids were inserted in
// ascending order alongside ascending timestamps:
//
//   - every row was served (length), so no page boundary skipped a tie;
//   - no row was served twice (strictness), so no boundary repeated one;
//   - the order is the declared one, and it is TOTAL — a tie resolved
//     arbitrarily would show up here as an out-of-order pair.
func requireStrictlyDescending(t *testing.T, seen []int64, want int) {
	t.Helper()
	require.Len(t, seen, want, "paging must yield every row exactly once")
	for i := 1; i < len(seen); i++ {
		require.Greater(t, seen[i-1], seen[i],
			"ids must strictly descend across page boundaries; a repeat or a reordered tie means the keyset is not a total order")
	}
}
