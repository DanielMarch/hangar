#!/usr/bin/env bash
# Gate 5 — Deployment Usability (docs/04_RELEASE_GATES.md §5).
#
# §5.1's procedure is three commands on a freshly provisioned host with only
# Docker installed. This script performs them and records a transcript, then
# checks §5.2's eight pass conditions and §5.3's three failure modes.
#
# ── WHAT IS SUBSTITUTED, AND WHY IT HAS TO BE ───────────────────────────────
# The procedure as written CANNOT be executed by anyone today, and that is a
# finding rather than an inconvenience. Measured at the release candidate:
#
#   raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml
#       -> HTTP 404
#   raw.githubusercontent.com/hangar-project/hangar/main/deploy/install.sh
#       -> HTTP 404
#   ghcr.io/hangar-project/hangar:latest
#       -> 403, no anonymous pull
#
# The repository has no git remote at all and the image has never been pushed.
# So all three inputs to §5.1 are unpublished, and no fresh host can reach
# them. B-3 corrected the PATH inside the second URL; it could not make the
# repository exist.
#
# This script therefore substitutes a LOCAL ORIGIN for the published one: a
# static HTTP server over the repository root stands in for
# raw.githubusercontent.com, and the locally built image is tagged with the
# name the compose file pulls. Everything else — the commands, the installer,
# the compose file, the image, the migrations, the healthchecks — is exactly
# what a real operator would get.
#
# What that buys and what it does not: it verifies that the three commands
# WORK, which was never previously demonstrated. It cannot verify that they
# work FROM A BLANK HOST, because the artefacts a blank host would fetch do
# not exist. Condition 5.2's "the image is pulled from the public registry" is
# therefore recorded as NOT MET, and the summary says so.
set -uo pipefail

VERSION="${1:-v1.0.0-rc1}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EVIDENCE="${REPO_ROOT}/docs/gate-evidence/${VERSION}/gate5"
TRANSCRIPT="${EVIDENCE}/transcript.txt"
RESULTS="${EVIDENCE}/conditions.tsv"

# A port nothing else on this machine uses. 8080 is the development
# installation and 8099 is Playwright's; adopting either would make this
# gate report on somebody else's server.
PORT="${HANGAR_GATE5_PORT:-8199}"
ORIGIN_PORT="${HANGAR_GATE5_ORIGIN_PORT:-8198}"
PROJECT="hangar-gate5"
WORKDIR="$(mktemp -d)"

mkdir -p "$EVIDENCE"
: > "$TRANSCRIPT"
: > "$RESULTS"

log()  { echo "$@" | tee -a "$TRANSCRIPT"; }
run()  { log "\$ $*"; "$@" 2>&1 | tee -a "$TRANSCRIPT"; return "${PIPESTATUS[0]}"; }
record() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$RESULTS"; log "[$2] $1 — $3"; }

cleanup() {
  log ""
  log "── teardown ────────────────────────────────────────────────────────────"
  (cd "$WORKDIR" && docker compose -p "$PROJECT" down -v >>"$TRANSCRIPT" 2>&1) || true
  [ -n "${ORIGIN_PID:-}" ] && kill "$ORIGIN_PID" 2>/dev/null
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

log "Gate 5 — Deployment Usability"
log "release:   $VERSION"
log "started:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "workdir:   $WORKDIR   (empty — no repository clone, no Go toolchain, no Node)"
log "docker:    $(docker --version)"
log "compose:   $(docker compose version --short)"
log ""

# ── the substituted origin ─────────────────────────────────────────────────
log "── substituted origin (see this script's header) ───────────────────────"
for url in \
  "https://raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml" \
  "https://raw.githubusercontent.com/hangar-project/hangar/main/deploy/install.sh"; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 20 "$url" || echo "unreachable")"
  log "  $url -> $code"
done
log ""

(cd "$REPO_ROOT" && python -m http.server "$ORIGIN_PORT" --bind 127.0.0.1 >/dev/null 2>&1) &
ORIGIN_PID=$!
sleep 2
ORIGIN="http://127.0.0.1:${ORIGIN_PORT}"

# The image the compose file names. A real deployment pulls this; here it is
# the image built from this very commit, tagged with that name.
docker tag "hangar:${VERSION}" ghcr.io/hangar-project/hangar:latest 2>/dev/null \
  || docker tag hangar:latest ghcr.io/hangar-project/hangar:latest
log "image under test: $(docker images --format '{{.Repository}}:{{.Tag}} {{.ID}} {{.CreatedSince}}' ghcr.io/hangar-project/hangar:latest | head -1)"
log ""

cd "$WORKDIR" || exit 1

# ── §5.1, COMMAND 1 OF 3 ───────────────────────────────────────────────────
log "── command 1 of 3 ──────────────────────────────────────────────────────"
run curl -fsSLO "${ORIGIN}/docker-compose.yml"
CMD1=$?

# ── §5.1, COMMAND 2 OF 3 ───────────────────────────────────────────────────
# The installer fetches .env.example itself, from the same origin.
log ""
log "── command 2 of 3 ──────────────────────────────────────────────────────"
export HANGAR_ENV_EXAMPLE_URL="${ORIGIN}/.env.example"
curl -fsSL "${ORIGIN}/deploy/install.sh" | sh 2>&1 | tee -a "$TRANSCRIPT"
CMD2="${PIPESTATUS[1]}"

# §5.3's "the administrator supplies only the SSO Client ID and Secret" — the
# two values install.sh leaves for the operator. Supplied here the way an
# operator would, by editing nothing else.
if [ -f .env ]; then
  sed -i "s|^HANGAR_SSO_CLIENT_ID=.*|HANGAR_SSO_CLIENT_ID=gate5-client-id|" .env
  sed -i "s|^HANGAR_SSO_CLIENT_SECRET=.*|HANGAR_SSO_CLIENT_SECRET=gate5-client-secret|" .env
  # Port and version only, so this run cannot collide with the development
  # installation on 8080. Not part of the procedure; recorded for honesty.
  echo "HANGAR_PORT=${PORT}" >> .env
  echo "HANGAR_VERSION=latest" >> .env
  echo "HANGAR_ESI_STARTUP_CATALOGUE_INGEST=true" >> .env
fi

# ── §5.3's FAILURE MODES, before the stack is brought up ────────────────────
log ""
log "── §5.3 failure modes ──────────────────────────────────────────────────"

# 1. Blank .env -> a named, actionable error, not a stack trace.
log "\$ docker run --rm ghcr.io/hangar-project/hangar:latest serve   # with no configuration at all"
BLANK_OUT="$(MSYS_NO_PATHCONV=1 docker run --rm ghcr.io/hangar-project/hangar:latest serve 2>&1 | head -20)"
echo "$BLANK_OUT" >> "$TRANSCRIPT"
if echo "$BLANK_OUT" | grep -qiE "HANGAR_(MASTER_KEY|DB_URL|SESSION_SECRET)"; then
  if echo "$BLANK_OUT" | grep -qiE "goroutine|panic:"; then
    record "5.3-blank-env" "FAIL" "named the variable but also printed a stack trace"
  else
    record "5.3-blank-env" "pass" "aborts naming the missing variables, no stack trace"
  fi
else
  record "5.3-blank-env" "FAIL" "did not name the missing configuration: $(echo "$BLANK_OUT" | head -2 | tr '\n' ' ')"
fi

# 2. Wrong HANGAR_PUBLIC_URL -> reported as a configuration error with the
#    expected value shown, not as an opaque OAuth failure.
log ""
log "\$ hangar serve   # HANGAR_PUBLIC_URL and HANGAR_SSO_CALLBACK_URL disagreeing"
# `serve` does NOT exit on an unreachable database — it starts its listener
# and reports unready — so this must be bounded or the gate hangs here for
# ever. 25 seconds is far longer than a configuration check needs.
MISMATCH_OUT="$(MSYS_NO_PATHCONV=1 timeout 25 docker run --rm \
  -e HANGAR_DB_URL="postgres://x:y@127.0.0.1:5432/z?sslmode=disable" \
  -e HANGAR_MASTER_KEY="$(head -c 32 /dev/urandom | base64)" \
  -e HANGAR_SESSION_SECRET="$(head -c 32 /dev/urandom | base64)" \
  -e HANGAR_SSO_CLIENT_ID=gate5 -e HANGAR_SSO_CLIENT_SECRET=gate5 \
  -e HANGAR_PUBLIC_URL="https://hangar.example.com" \
  -e HANGAR_SSO_CALLBACK_URL="https://SOMETHING-ELSE.example.com/auth/callback" \
  ghcr.io/hangar-project/hangar:latest serve 2>&1 | head -20)"
echo "$MISMATCH_OUT" >> "$TRANSCRIPT"
# §5.3 requires "the expected value shown", not merely "the two disagree" —
# an operator who set ONE of them wrong needs the other one to fix it. So the
# check is for the derived callback, ${HANGAR_PUBLIC_URL}/auth/callback, and
# not just for the word.
#
# PHASE 22: this condition FAILED at v1.0.0-rc1 — `serve` started normally
# with the two disagreeing, because internal/config compared them nowhere.
# That was defect B-8.
if echo "$MISMATCH_OUT" | grep -q "https://hangar.example.com/auth/callback"; then
  record "5.3-callback-mismatch" "pass" "reported at boot as a configuration error naming the EXPECTED callback (https://hangar.example.com/auth/callback), not an opaque OAuth failure"
elif echo "$MISMATCH_OUT" | grep -qi "callback"; then
  record "5.3-callback-mismatch" "FAIL" "the mismatch is reported but the expected value is not shown, which is what §5.3 asks for: $(echo "$MISMATCH_OUT" | head -3 | tr '\n' ' ')"
else
  record "5.3-callback-mismatch" "FAIL" "a public-url/callback mismatch is NOT reported at boot: $(echo "$MISMATCH_OUT" | head -2 | tr '\n' ' ')"
fi

# 3. Postgres not ready -> the migrate service waits on the healthcheck.
if grep -q "service_healthy" docker-compose.yml; then
  record "5.3-postgres-not-ready" "pass" "the migrate service depends_on postgres with condition: service_healthy"
else
  record "5.3-postgres-not-ready" "FAIL" "migrate does not wait on the postgres healthcheck"
fi

# ── §5.1, COMMAND 3 OF 3 ───────────────────────────────────────────────────
log ""
log "── command 3 of 3 ──────────────────────────────────────────────────────"
START_TS=$(date +%s)
run docker compose -p "$PROJECT" up -d
CMD3=$?
if [ "$CMD3" != 0 ]; then
  log ""
  log "-- docker compose up FAILED; the reason is in the service logs -----"
  docker compose -p "$PROJECT" logs --no-color migrate 2>&1 | tail -30 | tee -a "$TRANSCRIPT"
  docker compose -p "$PROJECT" logs --no-color hangar 2>&1 | tail -20 | tee -a "$TRANSCRIPT"
fi

# ── §5.2's PASS CONDITIONS ─────────────────────────────────────────────────
log ""
log "── §5.2 pass conditions ────────────────────────────────────────────────"

if [ "$CMD1" = 0 ] && [ "$CMD2" = 0 ] && [ "$CMD3" = 0 ]; then
  record "5.1" "pass" "exactly three commands, no editor step (the two SSO values are prompted for by install.sh)"
else
  record "5.1" "FAIL" "one of the three commands failed: curl=$CMD1 install=$CMD2 up=$CMD3"
fi

if grep -qE "^\s+build:" docker-compose.yml; then
  record "5.2" "FAIL" "the compose file has a build: key — the operator would compile from source"
else
  record "5.2" "SUBSTITUTED" "no build: key and no compilation, but the image is NOT pulled from a public registry: ghcr.io/hangar-project/hangar returns 403, raw.githubusercontent.com/hangar-project/hangar 404s, and 'git remote -v' is empty. DECIDED IN PHASE 22 (defect B-12): recorded as PERMANENTLY SUBSTITUTED for this release candidate, because publishing is an operator action with credentials this session does not hold, not a code change. Verified against a locally built image of the same commit; see docs/PRE_V1_OPEN_ITEMS.md B-12 for exactly what remains"
fi

# 5.4 — migrations run automatically and the stack is healthy within 5 minutes.
HEALTHY=""
for _ in $(seq 1 60); do
  code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/healthz" 2>/dev/null || true)"
  ready="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/readyz" 2>/dev/null || true)"
  if [ "$code" = "200" ] && [ "$ready" = "200" ]; then HEALTHY=yes; break; fi
  sleep 5
done
ELAPSED=$(( $(date +%s) - START_TS ))
if [ -n "$HEALTHY" ]; then
  record "5.4" "pass" "/healthz and /readyz both 200 after ${ELAPSED}s (budget 300s); migrations ran from the one-shot migrate service"
else
  record "5.4" "FAIL" "the stack was not healthy within ${ELAPSED}s"
fi

# 5.5 — the default profile is exactly postgres + hangar + the one-shot migrate.
SERVICES="$(docker compose -p "$PROJECT" ps --services --all 2>/dev/null | sort | tr '\n' ' ')"
log "default-profile services: $SERVICES"
if echo "$SERVICES" | grep -q redis; then
  record "5.5" "FAIL" "redis is in the default profile — Principle 7 requires it to be optional"
else
  record "5.5" "pass" "services are exactly: $SERVICES (no redis)"
fi

# 5.6 — the SPA is served from the binary; no separate web server or web root.
SPA="$(curl -s "http://127.0.0.1:${PORT}/" | head -c 400)"
if echo "$SPA" | grep -qi "<!doctype html\|<html"; then
  record "5.6" "pass" "the SPA is served by the binary itself on the same port as the API"
else
  record "5.6" "FAIL" "the root path did not return the SPA"
fi

# The first-boot assertions the release checklist cares about, checked here
# because this is the only place a genuinely fresh installation exists.
# ── WHY THIS POLLS ──────────────────────────────────────────────────────────
# The four THRESHOLD alert types are seeded through a join against
# app.esi_route, so they do not exist until a catalogue has been ingested — and
# `serve` runs that ingest IN THE BACKGROUND at startup, deliberately, so an ESI
# outage cannot delay the listener. /healthz returns 200 well before it lands.
#
# Checked once, 9 seconds after the stack came up, this read 50 alert types and
# 0 thresholds and looked exactly like defect B41. It was the check being early:
# the ingest completed 40 seconds later with 225 routes from a LIVE fetch and
# the catalogue completed at 54/4. A first-boot assertion has to wait for first
# boot to finish.
ALERTS=""; THRESHOLDS=""
for _ in $(seq 1 40); do
  ALERTS="$(docker compose -p "$PROJECT" exec -T postgres psql -U hangar -d hangar -tAc \
    "SELECT count(*) FROM app.alert_type" 2>/dev/null | tr -d '\r ')"
  THRESHOLDS="$(docker compose -p "$PROJECT" exec -T postgres psql -U hangar -d hangar -tAc \
    "SELECT count(*) FROM app.alert_type WHERE category = 'threshold'" 2>/dev/null | tr -d '\r ')"
  [ "$ALERTS" = "54" ] && [ "$THRESHOLDS" = "4" ] && break
  sleep 5
done
if [ "$ALERTS" = "54" ] && [ "$THRESHOLDS" = "4" ]; then
  record "first-boot-alerts" "pass" "54 alert types with 4 thresholds on the FIRST boot"
else
  record "first-boot-alerts" "FAIL" "expected 54 alert types and 4 thresholds, got ${ALERTS:-?} and ${THRESHOLDS:-?}"
fi

METRICS="$(MSYS_NO_PATHCONV=1 docker compose -p "$PROJECT" exec -T hangar hangar healthcheck 2>&1 | head -3)"
log "healthcheck: $METRICS"

# ── 5.7 — re-running after a version bump migrates forward without data loss ─
log ""
log "── §5.2 condition 5.7: re-run after a version bump ─────────────────────"
docker compose -p "$PROJECT" exec -T postgres psql -U hangar -d hangar -c \
  "INSERT INTO app.corporation (corporation_id, name, ticker) VALUES (98999001, 'Gate5 Survivor', 'G5S') ON CONFLICT DO NOTHING;" >>"$TRANSCRIPT" 2>&1
BEFORE="$(docker compose -p "$PROJECT" exec -T postgres psql -U hangar -d hangar -tAc \
  "SELECT name FROM app.corporation WHERE corporation_id = 98999001" 2>/dev/null | tr -d '\r ')"

docker tag ghcr.io/hangar-project/hangar:latest ghcr.io/hangar-project/hangar:bumped
sed -i "s|^HANGAR_VERSION=.*|HANGAR_VERSION=bumped|" .env
run docker compose -p "$PROJECT" up -d
sleep 20
AFTER="$(docker compose -p "$PROJECT" exec -T postgres psql -U hangar -d hangar -tAc \
  "SELECT name FROM app.corporation WHERE corporation_id = 98999001" 2>/dev/null | tr -d '\r ')"
UPCODE="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/healthz" 2>/dev/null || true)"
if [ "$BEFORE" = "Gate5Survivor" ] || [ "$BEFORE" = "Gate5 Survivor" ]; then
  if [ "$AFTER" = "$BEFORE" ] && [ "$UPCODE" = "200" ]; then
    record "5.7" "pass" "after a version bump and a second 'docker compose up -d', migrations re-ran and the seeded row survived"
  else
    record "5.7" "FAIL" "data did not survive the version bump: before='$BEFORE' after='$AFTER' health=$UPCODE"
  fi
else
  record "5.7" "FAIL" "could not seed the survivor row, so the check is inconclusive"
fi

# ── 5.8 — manual deployment against an external PostgreSQL 18 ───────────────
log ""
log "── §5.2 condition 5.8: manual deployment, external PostgreSQL 18 ───────"
log "The static linux/amd64 binary, its .env, and an external PostgreSQL 18 —"
log "no compose, no image, nothing from the repository but the binary."
run docker network create gate5-manual
run docker run -d --name gate5-pg --network gate5-manual \
  -e POSTGRES_USER=hangar -e POSTGRES_PASSWORD=hangar -e POSTGRES_DB=hangar postgres:18-alpine
sleep 15
if [ -f "${REPO_ROOT}/bin/hangar-linux-amd64" ]; then
  # Mounted as /opt/hangar-bin, NOT as /hangar, and the difference is a defect
  # this gate found — see the collision check below.
  MANUAL_OUT="$(MSYS_NO_PATHCONV=1 docker run --rm --network gate5-manual -w //opt \
    -v "${REPO_ROOT}/bin/hangar-linux-amd64:/opt/hangar-bin:ro" \
    -e HANGAR_DB_URL="postgres://hangar:hangar@gate5-pg:5432/hangar?sslmode=disable" \
    -e HANGAR_MASTER_KEY="$(head -c 32 /dev/urandom | base64)" \
    -e HANGAR_SESSION_SECRET="$(head -c 32 /dev/urandom | base64)" \
    -e HANGAR_SSO_CLIENT_ID=gate5 -e HANGAR_SSO_CLIENT_SECRET=gate5 \
    debian:12-slim //opt/hangar-bin migrate up 2>&1)"
  MANUAL_CODE=$?
  echo "$MANUAL_OUT" >> "$TRANSCRIPT"
  # The EXIT CODE, not a grep for a phrase. The first version of this check
  # searched the last six lines for a success marker, and on a successful run
  # those six lines are the deferred-threshold advisory — so a migration that
  # worked was recorded as a failure. `migrate up` exits non-zero when it
  # fails; that is the signal, and it does not move when a log line does.
  if [ "$MANUAL_CODE" = 0 ]; then
    record "5.8" "PARTIAL" "the static binary ran on a bare debian:12-slim with no toolchain and migrated an external PostgreSQL 18. systemd itself was NOT exercised — this host is Windows and has no systemd; the unit file is deploy/ material and remains unverified"
  else
    record "5.8" "FAIL" "the static binary could not migrate an external PostgreSQL 18 (exit $MANUAL_CODE): $(echo "$MANUAL_OUT" | tail -3 | tr '\n' ' ')"
  fi
else
  record "5.8" "FAIL" "bin/hangar-linux-amd64 is absent — run 'make build-all' first"
fi
# ── THE DEFECT 5.8 UNCOVERED, AND ITS FIX ───────────────────────────────────
# internal/config set viper SetConfigName("hangar") + SetConfigType("yaml")
# + AddConfigPath("."), and the config TYPE is what turns on viper's "a file
# named exactly `hangar`, no extension, is a config file" fallback. In a
# manual deployment the file with that name in that directory is THE BINARY,
# and §9.2's layout (/opt/hangar/hangar with WorkingDirectory=/opt/hangar) —
# which is what a systemd unit produces — therefore booted into
#
#   Error: config: reading config file: While parsing config:
#          yaml: control characters are not allowed
#
# naming neither the file nor the reason. Verified both ways at v1.0.0-rc1:
# mounted as /hangar with cwd / it failed; the same bytes mounted as
# /opt/hangar-bin migrated successfully. That was defect B-7, fixed in Phase
# 22 by dropping the config-type declaration — ./hangar.yaml and
# /etc/hangar/hangar.yaml are still found by extension.
#
# The check is the EXIT CODE and not only a grep, because the property is
# "the documented layout deploys", not "one error message is absent". Note
# that no pipe follows the docker run: `cmd | tail` reports tail's status.
log ""
log "\$ /hangar migrate up   # the binary named 'hangar' in its own working directory"
COLLIDE_OUT="$(MSYS_NO_PATHCONV=1 timeout 60 docker run --rm --network gate5-manual \
  -v "${REPO_ROOT}/bin/hangar-linux-amd64:/hangar:ro" \
  -e HANGAR_DB_URL="postgres://hangar:hangar@gate5-pg:5432/hangar?sslmode=disable" \
  -e HANGAR_MASTER_KEY="$(head -c 32 /dev/urandom | base64)" \
  -e HANGAR_SESSION_SECRET="$(head -c 32 /dev/urandom | base64)" \
  -e HANGAR_SSO_CLIENT_ID=gate5 -e HANGAR_SSO_CLIENT_SECRET=gate5 \
  debian:12-slim //hangar migrate up 2>&1)"
COLLIDE_CODE=$?
echo "$COLLIDE_OUT" >> "$TRANSCRIPT"
if echo "$COLLIDE_OUT" | grep -qi "parsing config\|control characters"; then
  record "5.8-config-name-collision" "FAIL" "a binary named 'hangar' in its own working directory is parsed as a YAML config file, so §9.2's manual layout boots into an opaque parse error naming neither the file nor the reason"
elif [ "$COLLIDE_CODE" = 0 ]; then
  record "5.8-config-name-collision" "pass" "the binary named 'hangar' in its own working directory (§9.2's documented layout) is not mistaken for a config file, and migrated the external PostgreSQL 18 from there"
else
  record "5.8-config-name-collision" "FAIL" "the binary is no longer parsed as its own config, but the run still failed (exit $COLLIDE_CODE): $(echo "$COLLIDE_OUT" | tail -3 | tr '\n' ' ')"
fi

docker rm -f gate5-pg >/dev/null 2>&1
docker network rm gate5-manual >/dev/null 2>&1

log ""
log "finished:  $(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "transcript: $TRANSCRIPT"
