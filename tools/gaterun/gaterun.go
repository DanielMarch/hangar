// Package gaterun holds the plumbing every gate runner needs: locating the
// binary under test, refusing to run against a database that is not
// disposable, and writing JSON artefacts.
//
// It lives under tools/ rather than internal/ deliberately. The reachability
// guard analyses reachability from cmd/hangar and skips tools/ and test/, so
// a helper used only by the runners would otherwise have to be declared as a
// permanent exception in test/reachability/allowlist.txt — an allowlist entry
// for code that is not a defect is how an allowlist stops meaning anything.
package gaterun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultBinary is where `make build` leaves the binary on this platform.
func DefaultBinary() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("bin", "hangar.exe")
	}
	return filepath.Join("bin", "hangar")
}

// GuardDatabase refuses to run against a database that does not look
// disposable.
//
// Every gate runner migrates its database and seeds thousands of rows into
// it; Gate 1 additionally clears app.esi_replica. Pointed at a development
// installation, that destroys the installation. The standing hazard with
// `make ci-strict` is exactly this — its e2e global-setup reseeds whatever
// HANGAR_DB_URL names — and these runners write considerably more than it
// does, so they check rather than warn.
//
// The check is a name convention, not a schema probe, because the property
// being asserted is the OPERATOR'S INTENT that this database be disposable,
// and no amount of inspecting the database reveals that.
func GuardDatabase(dbURL string, force bool) error {
	if force || strings.Contains(dbURL, "gate") {
		return nil
	}
	return fmt.Errorf("refusing to run against %q: a gate run migrates the database and seeds "+
		"thousands of rows, so it needs a THROWAWAY database. Point HANGAR_DB_URL at one whose name "+
		"contains \"gate\" — derive it from .env by substituting ONLY the database name, never by "+
		"hand-writing the credentials — or pass -force if you are certain", RedactDBURL(dbURL))
}

// RedactDBURL removes the password from a connection string so it can be
// printed in an error or an artefact.
func RedactDBURL(dbURL string) string {
	at := strings.LastIndex(dbURL, "@")
	scheme := strings.Index(dbURL, "://")
	if at < 0 || scheme < 0 || scheme >= at {
		return dbURL
	}
	return dbURL[:scheme+3] + "***" + dbURL[at:]
}

// RunBinary runs one hangar subcommand with the supplied environment and
// returns its combined output on failure.
func RunBinary(ctx context.Context, binary string, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("`hangar %s` failed: %w\n%s", strings.Join(args, " "), err, out)
	}
	fmt.Printf("gate: hangar %s\n%s", strings.Join(args, " "), out)
	return nil
}

// WriteJSON writes an indented JSON artefact.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

// EvidenceDir resolves the evidence directory for a gate, defaulting to
// docs/gate-evidence/<version>/<gate>.
func EvidenceDir(explicit, version, gate string) (string, error) {
	dir := explicit
	if dir == "" {
		dir = filepath.Join("docs", "gate-evidence", version, gate)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating evidence directory: %w", err)
	}
	return dir, nil
}
