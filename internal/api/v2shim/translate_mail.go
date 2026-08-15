package v2shim

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// translate_mail.go — legacy's character.mail route.
//
// One of only TWO routes that genuinely needed the full-set store query
// reasonNoKeysetWindow described (see classification.go's B57 note on the
// four that turned out not to need it because they cannot be served at all).
// ListMailHeadersByCharacter is new in Phase 20.10; ListMailHeadersPage keeps
// serving /api/v1.
//
// ── THE FIELD LIST IS THREE COLUMNS AND THREE RELATIONS ──────────────────
// MailResource is `parent::toArray()` with character_id, created_at,
// updated_at and `from` forgotten, then `body` flattened out of its relation
// and `recipients` remapped. Read from eveseat/api at the pinned commit, so
// the following is source, not deduction:
//
//	mail_id, subject, timestamp, sender, body, recipients
//
// `labels` and `is_read` are NOT missing from the recording by accident and
// are not hidden by the model — migration 2019_10_30_131410 MOVED both from
// `mail_headers` to `mail_recipients` and dropped them from the header. So
// the header genuinely has three visible columns. HANGAR keeps both on
// app.mail_header (its table is character-scoped, which is where legacy's
// ended up too), and neither reaches this wire.
//
// The three relations follow the columns in the controller's eager-load
// order — `with('sender', 'body', 'recipients', 'recipients.entity')` — which
// is how an Eloquent model serialises: attributes first, then relations in
// load order.
//
// ── AN EMPTY BODY IS "" AND NOT null ─────────────────────────────────────
// MailHeader::body() is `hasOne(MailBody)->withDefault(['body' => ''])`, so a
// mail whose body has not been fetched renders as the empty string. HANGAR
// has exactly that state — the body fan-out is a separate request per mail
// (worker.doMailBodyFanout), so a freshly-synced header has no app.mail_body
// row — and this is the branch the corpus does NOT exercise, because its one
// mail has a body. Handled explicitly rather than left to a nil dereference.

// characterMail — legacy's `mail_headers` row with its three relations.
func characterMail(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()
	characterID := req.IDs[0]

	// Ordered by mail_id in SQL, which is legacy's clustered-index order —
	// see the query's own note. No re-sort needed.
	headers, err := req.Deps.Store.ListMailHeadersByCharacter(ctx, characterID)
	if err != nil {
		return nil, internalError("listing mail headers", err)
	}

	page := Window(headers, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, header := range page {
		body, err := mailBody(req, characterID, header.MailID)
		if err != nil {
			return nil, err
		}
		recipients, err := mailRecipients(req, characterID, header.MailID)
		if err != nil {
			return nil, err
		}

		rows = append(rows, NewObj(6).
			Set("mail_id", Int(header.MailID)).
			// `subject` is NOT NULL in legacy (`$table->string('subject')`)
			// and nullable here, so it takes the NOT NULL rule in entity.go
			// — the empty string, which is what legacy's column would have
			// held. The corpus cannot pin this: its one mail has a subject.
			Set("subject", legacyStringNotNull(header.Subject)).
			Set("timestamp", legacyTime(header.SentAt)).
			// `from` is NOT NULL in legacy too, and the hasOne default copies
			// it into entity_id, so a missing sender is entity_id 0 rather
			// than null — exactly what the contract acceptor reads as in the
			// contracts recording.
			Set("sender", entityNameFirst(header.FromID)).
			Set("body", body).
			Set("recipients", recipients))
	}
	return req.PageOf(rows, int64(len(headers))), nil
}

func mailBody(req *Request, characterID, mailID int64) (string, error) {
	row, err := req.Deps.Store.GetMailBody(req.HTTP.Context(), characterID, mailID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The withDefault case above: no body row yet, so legacy's "".
		return "", nil
	}
	if err != nil {
		return "", internalError("reading mail body", err)
	}
	return row.Body, nil
}

func mailRecipients(req *Request, characterID, mailID int64) (Arr, error) {
	recipients, err := req.Deps.Store.ListMailRecipients(req.HTTP.Context(), characterID, mailID)
	if err != nil {
		return nil, internalError("listing mail recipients", err)
	}

	// Never nil: a mail with no recipient row renders `[]`, and a nil Arr
	// would encode as `null`.
	out := make(Arr, 0, len(recipients))
	for _, recipient := range recipients {
		id := recipient.RecipientID
		out = append(out, entityNameFirst(&id))
	}
	return out, nil
}
