package v2shim

import (
	"context"
	"sort"

	"github.com/hangar-project/hangar/internal/store/gen"
)

// Contacts — legacy's ContactResource, shared by AllianceController,
// CharacterController and CorporationController.
//
// The field list is Seat\Api\Http\Resources\ContactResource::toArray in its
// literal order: contact_id, standing, contact_type, is_watched,
// is_blocked, labels. HANGAR's app.contact carries all six, so this route
// family is byte-reproducible without any documented gap.
//
// `labels` is legacy's `$this->labels->pluck('name')` — the NAMES, not the
// ids. HANGAR stores app.contact.label_ids and the names in
// app.contact_label, so the shim resolves them, and a label id with no
// matching row is DROPPED rather than rendered as its number: legacy's
// pluck could only ever produce names, and emitting an id where a name
// belongs would give a client a value it cannot distinguish from a real
// label called "42".
func contactRow(contact gen.AppContact, labelNames map[int64]string) *Obj {
	labels := make(Arr, 0, len(contact.LabelIds))
	for _, id := range contact.LabelIds {
		if name, ok := labelNames[id]; ok {
			labels = append(labels, name)
		}
	}

	return NewObj(6).
		Set("contact_id", Int(contact.ContactID)).
		// standing is a float in BOTH schemas and NOT NULL in HANGAR's, which
		// is exactly what FloatValue is for. It was Num(...) until Phase 20.6
		// — same output, but it left the float/money distinction implicit at
		// the one call site where a reader most wants to see it stated.
		Set("standing", FloatValue(contact.Standing)).
		Set("contact_type", contact.ContactType).
		Set("is_watched", contact.IsWatched).
		Set("is_blocked", contact.IsBlocked).
		Set("labels", labels)
}

// listContacts serves all three contact routes; ownerKind selects which.
func listContacts(ownerKind string) func(*Request) (any, error) {
	return func(req *Request) (any, error) {
		if len(req.IDs) == 0 {
			return nil, errBadID
		}
		ownerID := req.IDs[0]
		ctx := req.HTTP.Context()

		contacts, err := req.Deps.Store.ListContacts(ctx, ownerKind, ownerID)
		if err != nil {
			return nil, internalError("listing contacts", err)
		}
		labelNames, err := contactLabelNames(ctx, req, ownerKind, ownerID)
		if err != nil {
			return nil, err
		}

		// ListContacts orders by contact_id, which is what legacy's
		// unordered `->paginate()` produced too: MySQL returned
		// character_contacts in primary-key order, and the recorded corpus
		// confirms it. Sorted explicitly here rather than relied upon,
		// because "the database happened to return it that way" is not a
		// contract and this one IS observable in the bytes.
		sort.SliceStable(contacts, func(i, j int) bool { return contacts[i].ContactID < contacts[j].ContactID })

		page := Window(contacts, req.Page, LegacyPerPage)
		rows := make(Arr, 0, len(page))
		for _, contact := range page {
			rows = append(rows, contactRow(contact, labelNames))
		}
		return req.PageOf(rows, int64(len(contacts))), nil
	}
}

func contactLabelNames(ctx context.Context, req *Request, ownerKind string, ownerID int64) (map[int64]string, error) {
	labels, err := req.Deps.Store.ListContactLabels(ctx, ownerKind, ownerID)
	if err != nil {
		return nil, internalError("listing contact labels", err)
	}
	names := make(map[int64]string, len(labels))
	for _, label := range labels {
		names[label.LabelID] = label.Name
	}
	return names, nil
}
