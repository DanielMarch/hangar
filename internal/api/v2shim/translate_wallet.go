package v2shim

// translate_wallet.go serves character.wallet-journal — the FIRST wallet
// route the shim has ever been able to serve, and the seventeenth served
// route overall.
//
// ── PHASE 23 (N-1): A ROUTE UNSERVED FOR A REASON THAT TURNED OUT FALSE ──
//
// character.wallet-journal was StatusPending on `reasonMySQLDoubleRounding`
// for five phases. The corpus records
//
//	"amount": 9007199254741000
//
// where the fixture value is 9007199254740993.01, and Money's round-trip
// produced 9007199254740994 — the nearest float64. The reachability
// allowlist recorded it as "MySQL's/PHP's 14-significant-digit rounding, not
// IEEE-754 nearest" and said closing it "needs the PHP recorder re-run with
// serialize_precision measured".
//
// Phase 23 ran that measurement and it was neither of the guesses:
// json_encode gives 9007199254740994 and MySQL renders the double exactly,
// but the value SITTING IN THE TABLE is 9.007199254741e15. PDO stringifies a
// bound float at the `precision` ini — 14 — so legacy's WRITE path is where
// the digits went. See v2shim.phpPrecision.
//
// With the rounding reproducible the reason was false, and the second
// candidate blocker does not apply either: CharacterWalletJournal declares
// `$incrementing = false` and its key is (character_id, id), so the `id` on
// the wire is ESI's journal-entry id — a real EVE identifier HANGAR has —
// and not the auto-increment surrogate that keeps the other three wallet
// routes unshimmable.
//
// So it is served, and this file is what the entity.go note anticipated:
// "the second will arrive with the first wallet route that becomes
// servable".

import (
	"github.com/hangar-project/hangar/internal/store/gen"
)

// legacyCharacterWalletDivision is the division a character's journal lives
// in. Characters have exactly one wallet; legacy has no division column on
// character_wallet_journals at all, and HANGAR's schema defaults it to 1
// (00014's own comment: "1 for characters").
const legacyCharacterWalletDivision = int16(1)

// characterWalletJournal is legacy's CharacterWalletJournalResource.
func characterWalletJournal(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	entries, err := req.Deps.Store.ListWalletJournalForShim(ctx, "character", req.IDs[0], legacyCharacterWalletDivision)
	if err != nil {
		return nil, internalError("listing wallet journal", err)
	}

	window := Window(entries, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(window))
	for _, entry := range window {
		row, rowErr := characterWalletJournalRow(entry)
		if rowErr != nil {
			return nil, rowErr
		}
		rows = append(rows, row)
	}

	// req.PageOf, not a hand-built Page: it is what carries this route's
	// `appends` setting into the pagination links, and character.wallet-
	// journal is the first served route with a real SECOND page, so those
	// links are load-bearing here in a way they have never been before.
	return req.PageOf(rows, int64(len(entries))), nil
}

// characterWalletJournalRow is one entry.
//
// The field order is character_wallet_journals' physical column order minus
// the model's $hidden (character_id, first_party_id, second_party_id,
// created_at, updated_at), with the two entity relations appended where the
// resource puts them — id, date, ref_type, amount, balance, reason,
// tax_receiver_id, tax, context_id, context_id_type, description,
// first_party, second_party. Read off the recording, not invented.
func characterWalletJournalRow(entry gen.AppWalletJournal) (*Obj, error) {
	amount, err := MoneyOrNull(entry.Amount)
	if err != nil {
		return nil, err
	}
	balance, err := MoneyOrNull(entry.Balance)
	if err != nil {
		return nil, err
	}
	// `tax` is money and goes through the same lossy conversion, which is
	// why it is MoneyOrNull rather than Float: legacy's column is a double
	// like the other two, and a reader grepping for Money must find every
	// money field on this row.
	tax, err := MoneyOrNull(entry.Tax)
	if err != nil {
		return nil, err
	}

	return NewObj(13).
		Set("id", Int(entry.JournalID)).
		Set("date", legacyTime(entry.Date)).
		Set("ref_type", entry.RefType).
		Set("amount", amount).
		Set("balance", balance).
		Set("reason", optString(entry.Reason)).
		Set("tax_receiver_id", optInt(entry.TaxReceiverID)).
		Set("tax", tax).
		Set("context_id", optInt(entry.ContextID)).
		Set("context_id_type", optString(entry.ContextIDType)).
		// `description` is NOT NULL in both schemas.
		Set("description", entry.Description).
		// {entity_id, name, category} — the WALLET resources' key order,
		// which is not CorporationSheetResource's {entity_id, category,
		// name}. Two SeAT resource classes built the same object with their
		// keys in different orders and JSON key order is part of the bytes;
		// entity.go records the divergence and this is the route that
		// needed the second ordering.
		Set("first_party", entityNameFirst(entry.FirstPartyID)).
		Set("second_party", entityNameFirst(entry.SecondPartyID)), nil
}
