package dto

import "testing"

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
