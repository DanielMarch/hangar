package catalogue

import (
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

// EmbeddedFS holds the fallback OpenAPI snapshot the catalogue boots from
// when the live spec cannot be fetched (01_ARCHITECTURE.md §5.1 "Offline
// boot"). It ships in the binary — no network or filesystem access is
// required to reach a usable, if stale, route catalogue.
//
//go:embed embedded/openapi.snapshot.json embedded/snapshot_meta.json
var EmbeddedFS embed.FS

// SnapshotMeta records the D_max the embedded snapshot was captured at, so
// a catalogue that falls back to it can report exactly how stale it is
// rather than merely "not live".
type SnapshotMeta struct {
	DMax       string    `json:"d_max"`
	CapturedAt time.Time `json:"captured_at"`
	Source     string    `json:"source"`
}

// LoadEmbeddedSnapshot returns the embedded spec bytes and its metadata.
func LoadEmbeddedSnapshot() ([]byte, SnapshotMeta, error) {
	specBytes, err := EmbeddedFS.ReadFile("embedded/openapi.snapshot.json")
	if err != nil {
		return nil, SnapshotMeta{}, fmt.Errorf("catalogue: reading embedded snapshot: %w", err)
	}
	metaBytes, err := EmbeddedFS.ReadFile("embedded/snapshot_meta.json")
	if err != nil {
		return nil, SnapshotMeta{}, fmt.Errorf("catalogue: reading embedded snapshot metadata: %w", err)
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, SnapshotMeta{}, fmt.Errorf("catalogue: parsing embedded snapshot metadata: %w", err)
	}
	if len(specBytes) == 0 {
		return nil, SnapshotMeta{}, fmt.Errorf("catalogue: embedded snapshot is empty")
	}
	return specBytes, meta, nil
}

// DMaxDate parses the snapshot's recorded D_max as a compatibility date.
func (m SnapshotMeta) DMaxDate() (time.Time, error) {
	return ParseDate(m.DMax)
}
