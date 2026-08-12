package events_test

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/events"
)

// fixedSecret/fixedBody/fixedTime make every signing assertion in this file
// reproducible — a signature test whose inputs move cannot tell a broken
// construction from a changed fixture.
var (
	fixedSecret = mustHex("deadbeef0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c")
	fixedBody   = []byte(`{"event_id":"0194f0d2-0000-7000-8000-000000000001","event_type":"rbac.user_role.assigned","aggregate":"user","aggregate_id":"11111111-1111-4111-8111-111111111111","occurred_at":"2026-08-11T09:00:00Z","payload":{"role_id":"22222222-2222-4222-8222-222222222222"}}`)
	fixedTime   = time.Unix(1770000000, 0).UTC()
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestSigningPayloadIsTimestampDotBody pins the one thing every third-party
// verifier must reproduce exactly. Written against a literal rather than
// against SigningPayload's own logic, because a test that re-derives the
// format from the code under test would happily accept a changed format.
func TestSigningPayloadIsTimestampDotBody(t *testing.T) {
	got := events.SigningPayload(fixedTime, []byte(`{"a":1}`))
	require.Equal(t, `1770000000.{"a":1}`, string(got))
}

func TestSignProducesParsableHeader(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)
	require.Equal(t, "t=1770000000,v1=", header[:len("t=1770000000,v1=")])

	ts, sig, err := events.ParseSignatureHeader(header)
	require.NoError(t, err)
	require.Equal(t, fixedTime, ts)
	require.Len(t, sig, 32)
}

func TestVerifyAcceptsItsOwnSignature(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)
	require.NoError(t, events.Verify(fixedSecret, fixedBody, header, fixedTime, events.DefaultReplayWindow))
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)
	tampered := append([]byte(nil), fixedBody...)
	tampered[len(tampered)-2] = '9'
	require.ErrorIs(t, events.Verify(fixedSecret, tampered, header, fixedTime, events.DefaultReplayWindow), events.ErrBadSignature)
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)
	other := mustHex("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	require.ErrorIs(t, events.Verify(other, fixedBody, header, fixedTime, events.DefaultReplayWindow), events.ErrBadSignature)
}

// TestVerifyRejectsReplayInBothDirections is the reason the timestamp is
// inside the signed string. A window enforced only against the past leaves
// an attacker free to capture a delivery and stamp it with a timestamp of
// their choosing far in the future.
func TestVerifyRejectsReplayInBothDirections(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)

	stale := fixedTime.Add(events.DefaultReplayWindow + time.Second)
	require.ErrorIs(t, events.Verify(fixedSecret, fixedBody, header, stale, events.DefaultReplayWindow), events.ErrStaleSignature)

	early := fixedTime.Add(-(events.DefaultReplayWindow + time.Second))
	require.ErrorIs(t, events.Verify(fixedSecret, fixedBody, header, early, events.DefaultReplayWindow), events.ErrStaleSignature)

	// Just inside the window, both ways, still valid.
	require.NoError(t, events.Verify(fixedSecret, fixedBody, header, fixedTime.Add(events.DefaultReplayWindow-time.Second), events.DefaultReplayWindow))
	require.NoError(t, events.Verify(fixedSecret, fixedBody, header, fixedTime.Add(-(events.DefaultReplayWindow-time.Second)), events.DefaultReplayWindow))
}

// TestVerifyRejectsTimestampSubstitution proves the binding directly: take
// a valid header, move only the t= element, and the signature must fail
// even inside the window.
func TestVerifyRejectsTimestampSubstitution(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)
	moved := strings.Replace(header, "t=1770000000", "t=1770000060", 1)
	require.NotEqual(t, header, moved)
	require.ErrorIs(t, events.Verify(fixedSecret, fixedBody, moved, fixedTime.Add(time.Minute), events.DefaultReplayWindow), events.ErrBadSignature)
}

func TestVerifyAcceptsUpperCaseHexAndUnknownElements(t *testing.T) {
	header := events.Sign(fixedSecret, fixedBody, fixedTime)

	// Only the hex VALUE is case-insensitive. The element keys (`t`, `v1`)
	// are part of the wire format and stay lower-case — upper-casing those
	// is a different header, not a differently-spelled one.
	ts, sig, err := events.ParseSignatureHeader(header)
	require.NoError(t, err)
	upperHex := fmt.Sprintf("t=%d,v1=%s", ts.Unix(), strings.ToUpper(hex.EncodeToString(sig)))
	require.NoError(t, events.Verify(fixedSecret, fixedBody, upperHex, fixedTime, events.DefaultReplayWindow),
		"hex case must not matter — a receiver library that upper-cases is not wrong")

	// A future v2= must not break a v1 verifier.
	forward := header + ",v2=" + strings.Repeat("ab", 48)
	require.NoError(t, events.Verify(fixedSecret, fixedBody, forward, fixedTime, events.DefaultReplayWindow))
}

func TestParseSignatureHeaderRejectsMalformed(t *testing.T) {
	for name, header := range map[string]string{
		"empty":          "",
		"no elements":    "garbage",
		"missing v1":     "t=1770000000",
		"missing t":      "v1=" + strings.Repeat("ab", 32),
		"t not a number": "t=yesterday,v1=" + strings.Repeat("ab", 32),
		"v1 not hex":     "t=1770000000,v1=zzzz",
		"v1 wrong size":  "t=1770000000,v1=abcd",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := events.ParseSignatureHeader(header)
			require.ErrorIs(t, err, events.ErrMalformedSignature)
		})
	}
}

// TestWebhookSignatureVerifiesWithReferenceScript is a Phase 19 exit
// criterion: deploy/verify-webhook-signature.sh must validate a live
// payload.
//
// It runs the ACTUAL shipped script — not a Go reimplementation of it —
// against a signature this package just produced, which is the only way the
// script can be evidence of anything. The script is the documentation for
// the construction (the roadmap's "a webhook endpoint whose signature
// verification the receiver gets wrong is the common support case"), so a
// silent drift between it and sign.go is precisely the failure this guards.
//
// Deliberately does NOT t.Skip when sh or openssl are missing: `make
// ci-strict` requires zero skips, and "the reference script could not be
// executed" is a failure of this criterion, not a reason to pass anyway.
func TestWebhookSignatureVerifiesWithReferenceScript(t *testing.T) {
	script := repoPath(t, "deploy", "verify-webhook-signature.sh")
	require.FileExists(t, script)

	shell := findShell(t)

	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "body.json")
	require.NoError(t, os.WriteFile(bodyPath, fixedBody, 0o600))

	header := events.Sign(fixedSecret, fixedBody, fixedTime)
	secretHex := hex.EncodeToString(fixedSecret)

	run := func(t *testing.T, sigHeader string, now time.Time) (string, error) {
		t.Helper()
		cmd := exec.Command(shell, toShellPath(script),
			"--signature", sigHeader,
			"--body-file", toShellPath(bodyPath),
			"--now", fmt.Sprint(now.Unix()),
		)
		cmd.Env = append(os.Environ(), "HANGAR_WEBHOOK_SECRET="+secretHex)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	t.Run("valid signature", func(t *testing.T) {
		out, err := run(t, header, fixedTime)
		require.NoError(t, err, "the reference script rejected a signature this package produced:\n%s", out)
		require.Contains(t, out, "SIGNATURE VALID")
		// The script must agree on the digest itself, not merely on the verdict.
		_, sig, parseErr := events.ParseSignatureHeader(header)
		require.NoError(t, parseErr)
		require.Contains(t, out, hex.EncodeToString(sig))
	})

	t.Run("tampered body is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(bodyPath, append(append([]byte(nil), fixedBody...), ' '), 0o600))
		out, err := run(t, header, fixedTime)
		require.Error(t, err, "the script accepted a body one byte different from the signed one:\n%s", out)
		require.Contains(t, out, "SIGNATURE INVALID")
		require.NoError(t, os.WriteFile(bodyPath, fixedBody, 0o600))
	})

	t.Run("replayed delivery is rejected", func(t *testing.T) {
		out, err := run(t, header, fixedTime.Add(time.Hour))
		require.Error(t, err, "the script accepted an hour-old delivery:\n%s", out)
		require.Contains(t, out, "replay window")
	})

	t.Run("substituted timestamp is rejected", func(t *testing.T) {
		moved := strings.Replace(header, "t=1770000000", "t=1770000060", 1)
		out, err := run(t, moved, fixedTime.Add(time.Minute))
		require.Error(t, err, "the script accepted a header whose timestamp was moved:\n%s", out)
		require.Contains(t, out, "SIGNATURE INVALID")
	})
}

// findShell locates a POSIX shell. On Windows the Git-for-Windows sh.exe is
// the one every other tool in this repo already relies on (the Makefile's
// SHELL is `/usr/bin/env bash`), so requiring it here adds no new
// dependency.
func findShell(t *testing.T) string {
	t.Helper()
	candidates := []string{"sh"}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			`C:\Program Files\Git\usr\bin\sh.exe`,
			`C:\Program Files (x86)\Git\usr\bin\sh.exe`,
		)
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatalf("no POSIX shell found: deploy/verify-webhook-signature.sh is a release artefact and this criterion "+
		"is that a third party can run it, so an environment that cannot execute it fails rather than skips (tried %v)", candidates)
	return ""
}

// toShellPath converts a Windows path to the forward-slash form MSYS's sh
// accepts. A no-op elsewhere.
func toShellPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	return strings.ReplaceAll(p, `\`, "/")
}

// repoPath resolves a path relative to the repository root from a package's
// test working directory.
func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return filepath.Join(append([]string{root}, parts...)...)
}
