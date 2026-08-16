#!/usr/bin/env bash
# Renders Gate 5's SUMMARY.md from the conditions.tsv run.sh records.
#
# Separate from run.sh because Gate 5 is the one gate whose procedure is
# manual (§5.1 is three commands a human runs on a fresh host), so its
# evidence is a transcript plus a verdict rather than a program's return
# value. This turns the transcript into the same report shape the other six
# gates emit from test/load.WriteSummary, so a reviewer comparing seven gates
# compares measurements and not formats.
#
#   bash tools/gate5-deploy/summarise.sh v1.0.0-rc1
set -euo pipefail

VERSION="${1:-v1.0.0-rc1}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EVIDENCE="${REPO_ROOT}/docs/gate-evidence/${VERSION}/gate5"
RESULTS="${EVIDENCE}/conditions.tsv"
OUT="${EVIDENCE}/SUMMARY.md"

[ -f "$RESULTS" ] || { echo "no conditions.tsv — run tools/gate5-deploy/run.sh first" >&2; exit 1; }

failed=$(awk -F'\t' '$2 == "FAIL"' "$RESULTS" | wc -l | tr -d ' ')
subst=$(awk -F'\t' '$2 == "SUBSTITUTED"' "$RESULTS" | wc -l | tr -d ' ')
partial=$(awk -F'\t' '$2 == "PARTIAL"' "$RESULTS" | wc -l | tr -d ' ')
total=$(wc -l < "$RESULTS" | tr -d ' ')
verdict="PASS"
[ "$failed" != "0" ] && verdict="FAIL"
[ "$verdict" = "PASS" ] && [ "$subst" != "0" ] && verdict="PASS (WITH SUBSTITUTION)"

started="$(grep -m1 '^started:' "${EVIDENCE}/transcript.txt" | sed 's/^started: *//')"
finished="$(grep -m1 '^finished:' "${EVIDENCE}/transcript.txt" | sed 's/^finished: *//')"

{
  echo "# Gate 5 — Deployment Usability"
  echo
  echo "**Verdict: ${verdict}**"
  echo
  echo "The three-command deployment of §5.1 was performed against the release-candidate image on"
  echo "an empty directory with no repository clone, no Go toolchain and no Node. ${total} conditions"
  echo "were evaluated: ${failed} failed, ${partial} passed only in part, and ${subst} could only be"
  echo "verified under substitution."
  echo
  echo "| | |"
  echo "| :-- | :-- |"
  echo "| Release | \`${VERSION}\` |"
  echo "| Started | ${started} |"
  echo "| Finished | ${finished} |"
  echo "| Procedure | \`tools/gate5-deploy/run.sh\` |"
  echo "| Transcript | \`transcript.txt\` — every command and its output, verbatim |"
  echo
  echo "## Pass conditions"
  echo
  echo "| # | Verdict | Measurement |"
  echo "| :-- | :-- | :-- |"
  awk -F'\t' '{
    v = ($2 == "pass") ? "pass" : "**" $2 "**";
    gsub(/\|/, "\\|", $3);
    printf "| %s | %s | %s |\n", $1, v, $3
  }' "$RESULTS"
  echo
  cat <<'NOTES'
## What this gate found

Gate 5 had never been run. Running it turned up three defects that no test in the suite could
have caught, because every test builds its own connection string and its own configuration.

**The installer generated a password that broke the deployment.** `install.sh` produced
`POSTGRES_PASSWORD` with `openssl rand -base64 32`, and `docker-compose.yml` interpolates that
value into `HANGAR_DB_URL`. base64's alphabet contains `/`, which terminates a URL's authority, so
`migrate` exited 1 with a parse error and the whole stack failed at the third command — for
roughly one installation in two, non-deterministically. **Fixed in this phase**: both installers
now generate the database password as hex, which is URL-safe by construction. The two 32-byte
base64 secrets are unchanged; neither is ever placed in a URL.

**A `HANGAR_PUBLIC_URL` / callback mismatch is not reported.** §5.3 requires it to surface as a
configuration error naming the expected value. Nothing anywhere compares the two, so the operator
learns about it when a user clicks "log in" and EVE SSO rejects the `redirect_uri` — precisely the
opaque OAuth failure the condition forbids. Not fixed here: it is a change to `internal/config`
and therefore to the binary every gate in this directory measured.

**The binary parses a same-named file in its working directory as its config.** viper is
configured with `SetConfigName("hangar")` + `SetConfigType("yaml")` + `AddConfigPath(".")`, so a
file named exactly `hangar` is read as YAML — and in a manual deployment that file is the binary.
`/opt/hangar/hangar` with `WorkingDirectory=/opt/hangar`, which is what a systemd unit does, boots
into "yaml: control characters are not allowed", naming neither the file nor the reason. The same
bytes under any other name migrate an external PostgreSQL 18 without complaint, so §9.2's static
binary itself is sound.

## What "SUBSTITUTED" means here, and why it is not a pass

§5.1's three commands fetch `docker-compose.yml` and `install.sh` from
`raw.githubusercontent.com/hangar-project/hangar` and pull the image from
`ghcr.io/hangar-project/hangar`. Measured at this release candidate: **404**, **404**, and **403**.
The repository has no git remote and the image has never been pushed.

So the gate was run against a local origin standing in for the published one, and the image the
compose file pulls was supplied locally from this same commit. Everything else — the commands, the
installer, the compose file, the migrations, the healthchecks, the version bump — is exactly what
an operator would get. What this **cannot** show is that the procedure works from a blank host,
because the artefacts a blank host would fetch do not exist. Condition 5.2 is recorded as
substituted for that reason and not as met.

## §5.8, and the one part of it that was not exercised

The static `linux/amd64` binary was run on a bare `debian:12-slim` with no toolchain of any kind,
against an external PostgreSQL 18 in its own container, and migrated it successfully. **systemd
itself was not exercised** — this host is Windows and has no systemd — so the unit file remains
unverified. That is a gap in the evidence, not a claim about the unit file.
NOTES
} > "$OUT"

echo "wrote $OUT (verdict: $verdict)"
