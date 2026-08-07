package domain

import "time"

// WalletBalance mirrors app.wallet_balance. Division is 1-7 for
// corporations (wallet divisions), always 1 for characters.
type WalletBalance struct {
	Owner    Owner
	Division int16
	Balance  Money
}

// WalletJournalEntry mirrors app.wallet_journal (02_DATABASE_SCHEMA.md
// §5.3, verbatim). RefType is a CCP open vocabulary — Principle 14 forbids
// rejecting an unrecognised one, since that would drop a journal row, which
// is a money-loss bug.
type WalletJournalEntry struct {
	Owner         Owner
	Division      int16
	JournalID     int64
	RefType       string
	Amount        *Money
	Balance       *Money
	Tax           *Money
	TaxReceiverID *int64
	FirstPartyID  *int64
	SecondPartyID *int64
	ContextID     *int64
	ContextIDType *string
	Reason        *string
	Description   string
	Date          time.Time
}

// WalletTransaction mirrors app.wallet_transaction. Quantity is NOT money
// (Principle 9); UnitPrice is.
type WalletTransaction struct {
	Owner         Owner
	Division      int16
	TransactionID int64
	ClientID      *int64
	Date          time.Time
	IsBuy         bool
	IsPersonal    *bool
	JournalRefID  *int64
	LocationID    int64
	Quantity      int64
	TypeID        int32
	UnitPrice     Money
}
