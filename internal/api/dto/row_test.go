package dto

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestToSnakeCase(t *testing.T) {
	cases := map[string]string{
		"CharacterID": "character_id",
		"TypeID":      "type_id",
		"IPAddress":   "ip_address",
		"OwnerHash":   "owner_hash",
		"ISK":         "isk",
		"HTTPStatus":  "http_status",
		"ID":          "id",
	}
	for in, want := range cases {
		if got := toSnakeCase(in); got != want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

type rowFixture struct {
	CharacterID int64
	TypeID      int32
	Name        string
	Secret      []byte
}

func TestRow(t *testing.T) {
	got := Row(rowFixture{CharacterID: 1, TypeID: 2, Name: "Rifter", Secret: []byte{0xde, 0xad}})
	if got["character_id"] != int64(1) {
		t.Errorf("character_id = %v", got["character_id"])
	}
	if got["name"] != "Rifter" {
		t.Errorf("name = %v", got["name"])
	}
	if got["secret"] != "dead" {
		t.Errorf("secret = %v", got["secret"])
	}
}

// TestJSONBFieldsEmitNestedJSON is Phase 18's exit criterion for SRS v3.1
// defect B12 (§0, §6: "a jsonb column is emitted as nested JSON, never as
// an encoded scalar"). It runs against REAL generated models rather than a
// synthetic fixture, because the defect was precisely that the generated
// field type — json.RawMessage, which IS []byte — fell through to the
// binary path by accident: a fixture written by hand would have proved
// only that the fixture's own field type behaves, not that
// internal/store/gen's does.
//
// Both halves are asserted: structured columns reach the wire as nested
// JSON, and genuinely binary columns (bytea — hashes, ciphertext, wrapped
// key material) still hex-encode.
func TestJSONBFieldsEmitNestedJSON(t *testing.T) {
	t.Run("route_diff is a nested object", func(t *testing.T) {
		// The very field Phase 18's pin-advance board has to render.
		diff := json.RawMessage(`{"newly_blocked":[{"operation_id":"get_x"}],"newly_unblocked":[]}`)
		row := Row(gen.AppEsiPinHistory{RouteDiff: diff})

		encoded, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshalling the row: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("decoding the row: %v", err)
		}
		got, ok := decoded["route_diff"].(map[string]any)
		if !ok {
			t.Fatalf("route_diff reached the wire as %T (%v), want a nested JSON object — "+
				"no client may decode a HANGAR response field (SRS §6, B12)",
				decoded["route_diff"], decoded["route_diff"])
		}
		blocked, ok := got["newly_blocked"].([]any)
		if !ok || len(blocked) != 1 {
			t.Fatalf("route_diff.newly_blocked = %#v, want a one-element array", got["newly_blocked"])
		}
	})

	t.Run("every jsonb model field is nested, never hex", func(t *testing.T) {
		// One representative per structured column named in B12, plus the
		// nullable-jsonb ones sqlc.yaml originally missed. Each is set to a
		// distinguishable document so a hex string is unmistakable.
		cases := []struct {
			name string
			row  map[string]any
			key  string
		}{
			{"starbase fuels", Row(gen.AppStarbaseDetail{Fuels: json.RawMessage(`[{"type_id":4051,"quantity":9000}]`)}), "fuels"},
			{"skyhook reagents", Row(gen.AppCorporationSkyhook{Reagents: json.RawMessage(`[{"type_id":81143}]`)}), "reagents"},
			{"sov hub reagents", Row(gen.AppCorporationSovereigntyHub{Reagents: json.RawMessage(`[{"type_id":81143}]`)}), "reagents"},
			{"structure services", Row(gen.AppCorporationStructure{Services: json.RawMessage(`[{"name":"Clone Bay"}]`)}), "services"},
			{"colony pins", Row(gen.AppPlanetColonyDetail{Pins: json.RawMessage(`[{"pin_id":1}]`)}), "pins"},
			{"colony links", Row(gen.AppPlanetColonyDetail{Links: json.RawMessage(`[{"source_pin_id":1}]`)}), "links"},
			{"colony routes", Row(gen.AppPlanetColonyDetail{Routes: json.RawMessage(`[{"route_id":1}]`)}), "routes"},
			{"route spec fragment", Row(gen.AppEsiRoute{SpecFragment: json.RawMessage(`{"operationId":"get_x"}`)}), "spec_fragment"},
			// Nullable jsonb — sqlc generated plain []byte for these until
			// this phase added the `nullable: true` override.
			{"unknown-type sample payload", Row(gen.AppNotificationUnknownType{SamplePayload: json.RawMessage(`{"foo":1}`)}), "sample_payload"},
			{"corporation palette", Row(gen.AppCorporation{Palette: json.RawMessage(`{"colour":"#fff"}`)}), "palette"},
			{"notification payload", Row(gen.AppCharacterNotification{Payload: json.RawMessage(`{"bar":2}`)}), "payload"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				encoded, err := json.Marshal(tc.row)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}
				var decoded map[string]any
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatalf("decoding: %v", err)
				}
				switch decoded[tc.key].(type) {
				case map[string]any, []any:
					// nested JSON — correct
				default:
					t.Fatalf("%s reached the wire as %T (%v), want nested JSON",
						tc.key, decoded[tc.key], decoded[tc.key])
				}
			})
		}
	})

	t.Run("binary columns still hex-encode", func(t *testing.T) {
		// bytea, not jsonb: an api token's hashed secret and a character
		// token's wrapped DEK/nonce/ciphertext. None of these may reach a
		// client at all — callers exclude them before calling Row — but the
		// converter's own behaviour for a genuinely binary column must not
		// change, or a hash would start 500ing the response it appears in.
		row := Row(gen.AppApiToken{HashedSecret: []byte{0xde, 0xad, 0xbe, 0xef}})
		if got := row["hashed_secret"]; got != "deadbeef" {
			t.Errorf("hashed_secret = %#v, want the hex string \"deadbeef\"", got)
		}
		tok := Row(gen.AppCharacterToken{
			WrappedDek: []byte{0x01, 0x02},
			Nonce:      []byte{0x03},
			Ciphertext: []byte{0x04},
		})
		for key, want := range map[string]string{"wrapped_dek": "0102", "nonce": "03", "ciphertext": "04"} {
			if got := tok[key]; got != want {
				t.Errorf("%s = %#v, want %q", key, got, want)
			}
		}
	})

	t.Run("a NULL jsonb column is JSON null, not an empty-document error", func(t *testing.T) {
		// json.RawMessage's own MarshalJSON returns its bytes verbatim, and
		// zero bytes is not a JSON document: without rawJSON's normalisation
		// encoding/json fails the WHOLE response with "unexpected end of
		// JSON input".
		for _, empty := range []json.RawMessage{nil, {}} {
			row := Row(gen.AppNotificationUnknownType{SamplePayload: empty})
			encoded, err := json.Marshal(row)
			if err != nil {
				t.Fatalf("marshalling a row with an empty jsonb column: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if decoded["sample_payload"] != nil {
				t.Errorf("sample_payload = %#v, want null", decoded["sample_payload"])
			}
		}
	})

	t.Run("a date column is a YYYY-MM-DD string, not a struct", func(t *testing.T) {
		// Same class of defect as B12 and found the same way: pgtype.Date
		// implements no json.Marshaler, so the converter's generic struct
		// branch used to recurse into it and emit
		// {"time":...,"infinity_modifier":0,"valid":true}. Phase 18's pin
		// history was the first screen that had to render one.
		row := Row(gen.AppEsiPinHistory{
			OldPin: pgtype.Date{Time: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), Valid: true},
			NewPin: pgtype.Date{Time: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC), Valid: true},
		})
		if got := row["new_pin"]; got != "2026-08-11" {
			t.Errorf("new_pin = %#v, want the string \"2026-08-11\"", got)
		}
		if got := row["old_pin"]; got != "2026-08-04" {
			t.Errorf("old_pin = %#v, want the string \"2026-08-04\"", got)
		}
		// A NULL date is null, not a zero date — old_pin is nullable and
		// "0001-01-01" would be a fabricated reading.
		nullRow := Row(gen.AppEsiPinHistory{OldPin: pgtype.Date{}})
		if got := nullRow["old_pin"]; got != nil {
			t.Errorf("a NULL date = %#v, want null", got)
		}
	})

	t.Run("an interval column is whole seconds, not a struct", func(t *testing.T) {
		row := Row(gen.AppEsiRoute{
			CacheAge:        pgtype.Interval{Microseconds: 300_000_000, Valid: true},
			RateLimitWindow: pgtype.Interval{Days: 1, Valid: true},
		})
		if got := row["cache_age"]; got != int64(300) {
			t.Errorf("cache_age = %#v, want 300 seconds", got)
		}
		if got := row["rate_limit_window"]; got != int64(86_400) {
			t.Errorf("rate_limit_window = %#v, want 86400 seconds", got)
		}
		if got := Row(gen.AppEsiRoute{})["cache_age"]; got != nil {
			t.Errorf("a NULL interval = %#v, want null", got)
		}
	})

	t.Run("no generated jsonb field can regress to []byte", func(t *testing.T) {
		// The converter distinguishes structured from binary BY TYPE, so the
		// rule is only as good as the generated types. This walks every
		// exported field of a representative set of models and fails if a
		// field whose name matches a known jsonb column has drifted back to
		// a plain []byte — which is what happens if sqlc.yaml loses either
		// jsonb override.
		jsonbFields := map[reflect.Type][]string{
			reflect.TypeOf(gen.AppEsiPinHistory{}):           {"RouteDiff"},
			reflect.TypeOf(gen.AppEsiRoute{}):                {"SpecFragment", "IdentifierTypes"},
			reflect.TypeOf(gen.AppStarbaseDetail{}):          {"Fuels"},
			reflect.TypeOf(gen.AppPlanetColonyDetail{}):      {"Pins", "Links", "Routes"},
			reflect.TypeOf(gen.AppCorporationSkyhook{}):      {"Reagents"},
			reflect.TypeOf(gen.AppCorporationStructure{}):    {"Services"},
			reflect.TypeOf(gen.AppNotificationUnknownType{}): {"SamplePayload"},
			reflect.TypeOf(gen.AppCorporation{}):             {"Palette"},
			reflect.TypeOf(gen.AppCharacterNotification{}):   {"Payload"},
		}
		rawMessage := reflect.TypeOf(json.RawMessage{})
		for typ, fields := range jsonbFields {
			for _, name := range fields {
				f, ok := typ.FieldByName(name)
				if !ok {
					t.Errorf("%s has no field %s — the column was renamed or dropped; update this test", typ, name)
					continue
				}
				if f.Type != rawMessage {
					t.Errorf("%s.%s is %s, want json.RawMessage — a jsonb column typed as plain []byte "+
						"hex-encodes on the wire (SRS §0 defect B12); check sqlc.yaml's two jsonb overrides",
						typ, name, f.Type)
				}
			}
		}
	})
}
