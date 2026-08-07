package domain

import "time"

// MailHeader mirrors app.mail_header.
type MailHeader struct {
	CharacterID int64
	MailID      int64
	FromID      *int64
	Subject     *string
	SentAt      time.Time
	IsRead      *bool
	Labels      []int64
}

// MailBody mirrors app.mail_body — split from MailHeader because ESI
// itself splits the list-metadata fetch from the body fetch.
type MailBody struct {
	CharacterID int64
	MailID      int64
	Body        string
}

// MailRecipient mirrors app.mail_recipient.
type MailRecipient struct {
	CharacterID   int64
	MailID        int64
	RecipientID   int64
	RecipientType string // open vocabulary
}

// MailLabel mirrors app.mail_label. UnreadCount is not money.
type MailLabel struct {
	CharacterID int64
	LabelID     int64
	Name        string
	Color       *string
	UnreadCount *int32
}
