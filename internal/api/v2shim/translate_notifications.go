package v2shim

import (
	"fmt"
	"net/http"
	"strconv"

	"gopkg.in/yaml.v3"
)

// translate_notifications.go — legacy's character.notifications route, and
// the one place on this surface where the shim must re-parse a stored string
// instead of reading the column HANGAR already parsed it into.
//
// ── WHY app.character_notification.payload CANNOT BE USED ────────────────
// HANGAR parses each notification's YAML at sync time and stores the result
// in `payload`, a jsonb column. Reading it back would be the obvious
// implementation and it produces the wrong bytes, for a reason that has
// nothing to do with the parse being wrong: POSTGRES JSONB DOES NOT PRESERVE
// KEY ORDER. It normalises object keys by (length, then bytewise), so the
// corpus's
//
//	{"solarsystemID":30000142,"structureID":1000000000004}
//
// comes back out of jsonb as
//
//	{"structureID": 1000000000004, "solarsystemID": 30000142}
//
// — measured against the live database, not assumed. Legacy's
// CharacterNotification::getTextAttribute runs Symfony's `Yaml::parse` and
// hands PHP an associative array in DOCUMENT order, which json_encode then
// emits in that order.
//
// So the shim re-parses `text`, the raw YAML, which the sync stores
// alongside the payload (handlers.SyncCharacterNotifications writes both).
// That is the whole reason both columns exist and it is worth stating: a
// route that reads `payload` would look completely correct, pass any test
// written against a decoded map, and fail the byte comparison.
//
// ── THE FIELD LIST, FROM SOURCE ──────────────────────────────────────────
// NotificationResource is `parent::toArray()` with `id` and `character_id`
// forgotten; the model hides created_at/updated_at and casts `is_read` to
// boolean and `timestamp` to datetime. Read from eveseat at the commit
// testdata/legacy-api-v2/README.md pins. The physical column order of
// `character_notifications` therefore gives:
//
//	notification_id, type, sender_id, sender_type, timestamp, is_read, text
//
// `timestamp` is Carbon-serialised (ISO-8601 with six fractional digits)
// because the model DOES cast it — the same split translate_skills.go
// documents, and the second of only two routes in the corpus that take that
// branch.

// characterNotifications — legacy's `character_notifications` row.
func characterNotifications(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	notifications, err := req.Deps.Store.ListCharacterNotificationsByCharacter(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing notifications", err)
	}

	page := Window(notifications, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, n := range page {
		text, err := legacyNotificationText(n.Text, n.NotificationID)
		if err != nil {
			return nil, err
		}
		// sender_id, sender_type and is_read are all NOT NULL in legacy and
		// all nullable here, so all three go through the NOT NULL rule in
		// entity.go. `is_read` is the one that matters in practice: ESI omits
		// it, so EVERY notification on the live installation has it NULL, and
		// the corpus — whose fixture supplies false — could not have caught
		// that. Found by reading a real response, not by a test.
		rows = append(rows, NewObj(7).
			Set("notification_id", Int(n.NotificationID)).
			Set("type", n.Type).
			Set("sender_id", legacyIntNotNull(n.SenderID)).
			Set("sender_type", legacyStringNotNull(n.SenderType)).
			Set("timestamp", legacyCarbonTime(n.SentAt)).
			Set("is_read", legacyBoolNotNull(n.IsRead)).
			Set("text", text))
	}
	return req.PageOf(rows, int64(len(notifications))), nil
}

// errUnparseableNotification is what the shim answers when a stored
// notification's YAML will not parse.
//
// ── WHY THIS IS A 500 AND NOT A SUBSTITUTED VALUE ────────────────────────
// Legacy's accessor calls Symfony's Yaml::parse with no try/catch, so an
// unparseable payload threw a ParseException and the whole page 500'd. That
// is not a good behaviour, but it IS the behaviour, and the alternatives are
// worse for a compatibility shim: emitting `null`, or the raw string, or
// HANGAR's `{"raw": …}` wrapper would each hand a client a value legacy could
// never have produced, in a field the client parses as structured data.
//
// UNMEASURED, and marked so: the corpus has no unparseable notification, so
// what legacy did with one is read from the source rather than from the
// bytes. HANGAR does know which rows these are — app.character_notification
// .parse_failed, set by the sync per Principle 14 — and the message names the
// notification so an operator can find it, which legacy's stack trace did not.
func errUnparseableNotification(id int64) error {
	return &shimError{
		Status: http.StatusInternalServerError,
		Message: "Server Error: notification " + strconv.FormatInt(id, 10) +
			" carries a payload that is not valid YAML, which legacy's Yaml::parse would " +
			"also have rejected. See /api/v1 for the raw text and the unknown-types board.",
	}
}

// legacyNotificationText renders `text` the way legacy's accessor did:
// Yaml::parse over the RAW stored string, in document order.
//
// A NULL text is JSON null — getTextAttribute short-circuits on a null value
// — and so is an empty document, because Yaml::parse("") is null too.
func legacyNotificationText(raw *string, id int64) (any, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(*raw), &node); err != nil {
		return nil, errUnparseableNotification(id)
	}
	if len(node.Content) == 0 {
		// A document containing only comments or whitespace.
		return nil, nil
	}
	value, err := yamlNodeToLegacyJSON(node.Content[0])
	if err != nil {
		return nil, errUnparseableNotification(id)
	}
	return value, nil
}

// yamlNodeToLegacyJSON converts one yaml.Node to the shim's encodable types,
// PRESERVING MAPPING ORDER — which is the entire reason this walks a Node
// tree instead of unmarshalling into map[string]any, whose iteration order Go
// randomises and whose json.Marshal would then sort.
//
// The scalar rules reproduce what PHP holds after Yaml::parse and hands to
// json_encode: an integer stays an integer (never gains a ".0"), a float goes
// through the same PHP double formatter every other number on this surface
// uses, and `true`/`false`/`null` are JSON literals rather than strings.
func yamlNodeToLegacyJSON(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.MappingNode:
		obj := NewObj(len(node.Content) / 2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			value, err := yamlNodeToLegacyJSON(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			obj.Set(key, value)
		}
		return obj, nil

	case yaml.SequenceNode:
		// Non-nil even when empty: an empty YAML sequence is `[]`.
		out := make(Arr, 0, len(node.Content))
		for _, item := range node.Content {
			value, err := yamlNodeToLegacyJSON(item)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil

	case yaml.AliasNode:
		if node.Alias == nil {
			return nil, fmt.Errorf("v2shim: YAML alias with no anchor")
		}
		return yamlNodeToLegacyJSON(node.Alias)

	case yaml.ScalarNode:
		return yamlScalarToLegacyJSON(node), nil

	default:
		return nil, fmt.Errorf("v2shim: unsupported YAML node kind %v", node.Kind)
	}
}

func yamlScalarToLegacyJSON(node *yaml.Node) any {
	// A quoted scalar is a string whatever it looks like — !!str is what
	// yaml.v3 resolves `"30000142"` to, and PHP agrees.
	switch node.Tag {
	case "!!null":
		return nil
	case "!!bool":
		return node.Value == "true" || node.Value == "True" || node.Value == "TRUE" ||
			node.Value == "y" || node.Value == "yes" || node.Value == "on"
	case "!!int":
		if parsed, err := strconv.ParseInt(node.Value, 0, 64); err == nil {
			return Int(parsed)
		}
		// An integer too large for int64. PHP would hold it as a float, so
		// so does this rather than truncating it.
		if parsed, err := strconv.ParseFloat(node.Value, 64); err == nil {
			return Num(parsed)
		}
		return node.Value
	case "!!float":
		if parsed, err := strconv.ParseFloat(node.Value, 64); err == nil {
			return Num(parsed)
		}
		return node.Value
	default:
		return node.Value
	}
}
