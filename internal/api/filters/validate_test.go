package filters

import "testing"

var testSpec = New("character",
	Field{Name: "name", Type: FieldString},
	Field{Name: "corporation_id", Type: FieldInt},
	Field{Name: "active", Type: FieldBool},
)

// TestAdversarialFiltersRejected — Phase 15 exit criterion: "non-whitelisted
// filters produce 422" (this package's contribution: Validate returns
// *ErrInvalidFilter, which internal/api.FilterError turns into a 422 —
// tested at that boundary too, but the rejection itself is proven here).
func TestAdversarialFiltersRejected(t *testing.T) {
	cases := map[string]map[string]string{
		"unknown field":           {"drop_table": "x"},
		"sql injection attempt":   {"name": "'; DROP TABLE app.character; --"},
		"type-confused int field": {"corporation_id": "not-a-number"},
		"type-confused bool":      {"active": "maybe"},
		"sql comment marker":      {"name": "x -- "},
		"union select":            {"name": "x UNION SELECT * FROM app.user"},
		"null byte":               {"name": "x\x00y"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Validate(testSpec, raw)
			if err == nil {
				t.Fatalf("expected rejection for %v", raw)
			}
			var invalid *ErrInvalidFilter
			if !asInvalidFilter(err, &invalid) {
				t.Fatalf("expected *ErrInvalidFilter, got %T: %v", err, err)
			}
		})
	}
}

func TestWhitelistedFiltersAccepted(t *testing.T) {
	out, err := Validate(testSpec, map[string]string{"name": "Rifter", "corporation_id": "1000009", "active": "true"})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if out["name"] != "Rifter" {
		t.Fatalf("name = %v", out["name"])
	}
	if out["corporation_id"] != int64(1000009) {
		t.Fatalf("corporation_id = %v", out["corporation_id"])
	}
	if out["active"] != true {
		t.Fatalf("active = %v", out["active"])
	}
}

func asInvalidFilter(err error, target **ErrInvalidFilter) bool {
	if e, ok := err.(*ErrInvalidFilter); ok {
		*target = e
		return true
	}
	return false
}
