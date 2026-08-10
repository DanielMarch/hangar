package sde

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Row is one decoded JSONL line: `_key` pulled out (the entry's id — see
// manifest.go's header on the `{"_key": ..., ...}` shape), the remaining
// fields kept as a generic map so table-specific extractors (below) can
// pull out the handful of columns each `sde.*` table indexes, while the
// row as a whole is preserved verbatim into that table's `data` column.
type Row struct {
	Key    any
	Fields map[string]any
}

// ReadJSONL decodes a JSONL stream one line at a time and calls fn for
// each row, never holding more than one line in memory — the streaming
// requirement (roadmap: "SDE download is large and compressed. Stream it;
// never buffer it entirely in memory"). fn returning an error stops the
// scan and propagates.
func ReadJSONL(r io.Reader, fn func(Row) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024) // a handful of SDE rows (dogma attribute lists) run large
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			return fmt.Errorf("sde: decoding JSONL line: %w", err)
		}
		key := fields["_key"]
		delete(fields, "_key")
		if v, ok := fields["_value"]; ok && len(fields) == 1 {
			// Scalar-valued entry (roadmap upstream format: `{"_key":
			// ..., "_value": ...}` when the entry itself isn't an
			// object) — surface _value back under the row's own Fields
			// so an extractor can still find it, but Key is what
			// identifies the row.
			fields = map[string]any{"_value": v}
		}
		if err := fn(Row{Key: key, Fields: fields}); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("sde: scanning JSONL: %w", err)
	}
	return nil
}

// englishName extracts the `name.en` (or bare `name` string) field CCP's
// localized-name shape uses, falling back to an empty string rather than
// erroring — some SDE tables (type_dogma_attribute, type_material) carry
// no name at all, and a handful of others occasionally omit `en`.
func englishName(fields map[string]any) string {
	switch v := fields["name"].(type) {
	case string:
		return v
	case map[string]any:
		if en, ok := v["en"].(string); ok {
			return en
		}
	}
	return ""
}

func asInt64(v any) (int64, bool) {
	f, ok := v.(float64) // encoding/json decodes every JSON number as float64
	if !ok {
		return 0, false
	}
	return int64(f), true
}

func asInt32(v any) (int32, bool) {
	i, ok := asInt64(v)
	return int32(i), ok
}

func asFloat64(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}
