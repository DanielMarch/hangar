package sde

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ── PHASE 23: THE GUARD FOR A DEFECT THAT SURVIVED THREE PHASES ──────────
//
// `hangar admin import-sde` had never worked against a real export and
// could not have. The importer asked its SourceProvider for
// `<postgres table name>.jsonl` — `category.jsonl`, `group_.jsonl`,
// `type.jsonl` — and CCP ships `categories.jsonl`, `groups.jsonl`,
// `types.jsonl`: plural, camelCase, and prefixed for the map tables. Not
// one of the twenty-two names matched. The command downloaded 99 MB, found
// nothing, imported zero rows into every table and correctly refused to
// swap on the first smoke query.
//
// Every test passed throughout, because testdata/sde/*.jsonl had been named
// after the POSTGRES TABLES — fixtures invented to match the code instead of
// recorded from the thing the code reads. tools/gate4-traceability's header
// names that failure mode exactly: "a document that agrees with itself".
//
// So the fixtures are now verbatim rows from build 3475087, and this test
// closes the loop the other way: every source name a spec asks for must
// appear in a RECORDED listing of the real archive. A future table added
// with a guessed file name fails here, at `go test`, instead of failing an
// operator four phases later with 99 MB of wasted download.

const exportManifest = "../../testdata/sde/EXPORT-MANIFEST-3475087.txt"

func recordedExportFiles(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(exportManifest)
	require.NoError(t, err, "the recorded export listing must exist; regenerate it from a real SDE zip")
	defer func() { _ = f.Close() }()

	files := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		files[line] = true
	}
	require.NoError(t, scanner.Err())
	require.Greater(t, len(files), 90, "the recorded listing looks truncated")
	return files
}

// TestEverySourceFileExistsInCCPsExport is the defect, stated as the check
// that was missing.
func TestEverySourceFileExistsInCCPsExport(t *testing.T) {
	files := recordedExportFiles(t)
	for _, spec := range tableSpecs() {
		name := spec.sourceFile() + ".jsonl"
		require.Truef(t, files[name],
			"sde.%s reads %q, which is not in CCP's export. Every file it does ship is listed in %s — "+
				"the table names are plural and camelCase (categories, groups, types, mapSolarSystems), "+
				"never the Postgres table name", spec.table, name, exportManifest)
	}
}

// TestEveryFixtureIsNamedAfterTheExport is the other half. A fixture named
// after the Postgres table is exactly what made the defect invisible: the
// DirSource finds it, the import succeeds, and nothing has compared the
// name to reality.
func TestEveryFixtureIsNamedAfterTheExport(t *testing.T) {
	files := recordedExportFiles(t)
	entries, err := os.ReadDir("../../testdata/sde")
	require.NoError(t, err)

	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		found++
		require.Truef(t, files[name],
			"testdata/sde/%s is not a file CCP ships. A fixture named after the code rather than after the "+
				"export is how import-sde stayed broken for three phases with every test green", name)
	}
	require.Greater(t, found, 20, "the fixture set looks incomplete")
}

// TestEverySpecHasExactlyOneExtractor — the two extractor shapes are
// mutually exclusive, and a spec with neither would silently import an
// empty table while Build reported success.
func TestEverySpecHasExactlyOneExtractor(t *testing.T) {
	for _, spec := range tableSpecs() {
		hasOne := spec.extract != nil
		hasMany := spec.expand != nil
		require.Truef(t, hasOne != hasMany,
			"sde.%s must set exactly one of extract and expand (extract=%t, expand=%t)", spec.table, hasOne, hasMany)
	}
}

// TestNoTwoSpecsShareATable guards the map Build builds from tableSpecs():
// a duplicate would silently win and the loser's table would import empty.
// Two specs legitimately share a SOURCE (blueprint and blueprint_activity
// both read blueprints.jsonl), which is why this checks the table only.
func TestNoTwoSpecsShareATable(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range tableSpecs() {
		require.Falsef(t, seen[spec.table], "two specs claim sde.%s", spec.table)
		seen[spec.table] = true
	}
}
