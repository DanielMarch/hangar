// Package sde implements streaming import of CCP's Static Data Export
// (02_DATABASE_SCHEMA.md §6) via the atomic sde/sde_next schema swap.
//
// Upstream: https://developers.eveonline.com/docs/services/static-data/.
// The manifest is itself a JSONL file, `latest.jsonl`, one `{"_key": ...,
// ...}` record per line; the build number currently live is the record
// whose key is "sde". Table data ships as a zip of one JSONL file per
// table (`eve-online-static-data-<build>-jsonl.zip`), each line shaped
// `{"_key": <id>, ...fields}` for object-valued entries (integer keys are
// pulled out of the object itself into `_key` rather than left as the
// JSON object's own key, since JSON object keys are always strings) or
// `{"_key": <id>, "_value": <scalar>}` when the entry itself isn't an
// object.
package sde

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ManifestEntry is one line of latest.jsonl.
type ManifestEntry struct {
	Key   string `json:"_key"`
	Build int64  `json:"buildNumber"`
}

// Manifest is the parsed latest.jsonl: every entry keyed by its `_key`.
type Manifest struct {
	Entries map[string]ManifestEntry
}

// LatestBuild returns the build number for the "sde" key — "The latest
// build number is in the record with the key `sde`" per the upstream docs.
func (m Manifest) LatestBuild() (int64, error) {
	e, ok := m.Entries["sde"]
	if !ok {
		return 0, fmt.Errorf("sde: manifest has no \"sde\" entry")
	}
	return e.Build, nil
}

// ParseManifest reads a JSONL manifest stream (one ManifestEntry per line)
// without buffering the whole body — the manifest itself is small, but the
// same streaming reader this package uses for the much larger table data
// is reused here for one code path rather than two.
func ParseManifest(r io.Reader) (Manifest, error) {
	m := Manifest{Entries: map[string]ManifestEntry{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e ManifestEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return Manifest{}, fmt.Errorf("sde: parsing manifest line: %w", err)
		}
		m.Entries[e.Key] = e
	}
	if err := sc.Err(); err != nil {
		return Manifest{}, fmt.Errorf("sde: scanning manifest: %w", err)
	}
	return m, nil
}

// FetchManifest downloads and parses latest.jsonl over HTTP.
func FetchManifest(ctx context.Context, client *http.Client, manifestURL string) (Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("sde: building manifest request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("sde: fetching manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("sde: manifest fetch returned status %d", resp.StatusCode)
	}
	return ParseManifest(resp.Body)
}
