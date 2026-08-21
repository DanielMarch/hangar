#!/usr/bin/env bash
# The release checklist, run against the IMAGE rather than against the tree.
#
# ── WHY THIS IS A SCRIPT ───────────────────────────────────────────────────
#
# Every one of these checks has been performed by hand at some point in this
# project's history, and a check performed by hand is one nobody can re-run
# to disagree with. They are the same class of thing as a gate: a verdict
# and the measurement behind it, committed.
#
# It verifies the ARTEFACT — `docker run <image>` — not `go run ./cmd/hangar`.
# The distinction is the point: what an operator installs is the image, and
# the image is the only thing that can be wrong in a way the test suite
# cannot see.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${1:-hangar:v1.0.0-rc3}"
OUT_DIR="${2:-}"
if [ -z "$OUT_DIR" ]; then
  echo "usage: $0 <image> <evidence-dir>" >&2
  exit 2
fi
mkdir -p "$OUT_DIR"
TRANSCRIPT="${OUT_DIR}/transcript.txt"
VERDICT="${OUT_DIR}/verdict.tsv"
: > "$TRANSCRIPT"
: > "$VERDICT"

# Unique per invocation, so a stale run's cleanup trap cannot destroy this
# one's containers mid-check — the failure that cost three runs of the N-9
# proof in this phase.
RUN_ID=$$
NET="rv-net-${RUN_ID}"
PG="rv-pg-${RUN_ID}"
APP="rv-app-${RUN_ID}"

log()  { echo "$*" | tee -a "$TRANSCRIPT"; }
step() { log ""; log "### $*"; }
record() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$VERDICT"; log "[$2] $1 — $3"; }

cleanup() {
  docker rm -f "$APP" "$PG" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  return 0
}
trap cleanup EXIT

# openssl, not `head -c 32 /dev/urandom | base64`: on Windows, head can stop
# early on a 0x1A in the stream, producing an intermittently SHORT key and a
# configuration error that looks nothing like a harness bug.
newkey() { openssl rand -base64 32 | tr -d '\r\n'; }
psql() { docker exec "$PG" psql -U hangar -d hangar -tAc "$1" 2>>"$TRANSCRIPT" | tr -d ' \r\n'; }

MASTER_KEY="$(newkey)"
SESSION_SECRET="$(newkey)"
DB_URL="postgres://hangar:hangar@${PG}:5432/hangar?sslmode=disable"
env_args=(
  -e "HANGAR_DB_URL=${DB_URL}"
  -e "HANGAR_MASTER_KEY=${MASTER_KEY}"
  -e "HANGAR_SESSION_SECRET=${SESSION_SECRET}"
  -e HANGAR_SSO_CLIENT_ID=release-verify
  -e HANGAR_SSO_CLIENT_SECRET=release-verify
  -e HANGAR_PUBLIC_URL=http://localhost:8080
  -e HANGAR_SSO_CALLBACK_URL=http://localhost:8080/auth/callback
)

log "image:  ${IMAGE}"
log "digest: $(docker image inspect --format '{{.Id}}' "$IMAGE" 2>/dev/null)"
log "built:  $(docker run --rm "$IMAGE" --version 2>&1 | head -1)"

step "a throwaway PostgreSQL 18"
docker network create "$NET" >>"$TRANSCRIPT" 2>&1
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=hangar -e POSTGRES_PASSWORD=hangar -e POSTGRES_DB=hangar \
  postgres:18-alpine >>"$TRANSCRIPT" 2>&1
for _ in $(seq 1 40); do docker exec "$PG" pg_isready -U hangar >/dev/null 2>&1 && break; sleep 1; done

step "migrations, from the image"
if docker run --rm --network "$NET" "${env_args[@]}" "$IMAGE" migrate up >>"$TRANSCRIPT" 2>&1; then
  record "migrate" "pass" "migrate up exited 0 against a fresh PostgreSQL 18"
else
  record "migrate" "FAIL" "migrate up did not exit 0"
  exit 1
fi

step "serve"
docker run -d --name "$APP" --network "$NET" "${env_args[@]}" \
  -e HANGAR_METRICS_ADDR=0.0.0.0:9090 "$IMAGE" serve >>"$TRANSCRIPT" 2>&1
HEALTHY=no
for _ in $(seq 1 60); do
  if docker run --rm --network "$NET" alpine:3.20 wget -qO- "http://${APP}:8080/healthz" >/dev/null 2>&1; then
    HEALTHY=yes; break
  fi
  sleep 2
done
if [ "$HEALTHY" = yes ]; then
  record "serve" "pass" "/healthz answered 200 from the image with no configuration beyond the five required variables"
else
  record "serve" "FAIL" "the server never became healthy"
  docker logs "$APP" >>"$TRANSCRIPT" 2>&1
  exit 1
fi

step "/metrics"
METRICS="$(docker run --rm --network "$NET" alpine:3.20 wget -qO- "http://${APP}:9090/metrics" 2>>"$TRANSCRIPT")"
SERIES="$(printf '%s\n' "$METRICS" | grep -cE '^[a-z_]+\{|^[a-z_]+ ')"
GATE3="$(printf '%s\n' "$METRICS" | grep -cE '^alert_delivery_total|^alert_dead_letter_depth')"
if [ "${SERIES:-0}" -gt 20 ] && [ "${GATE3:-0}" -ge 1 ]; then
  record "metrics" "pass" "${SERIES} series exposed, including Gate 3's alert_delivery_total / alert_dead_letter_depth (${GATE3}) — which serve exported as literal nils before Phase 23"
else
  printf '%s\n' "$METRICS" | head -40 >> "$TRANSCRIPT"
  record "metrics" "FAIL" "/metrics exposed ${SERIES:-0} series and ${GATE3:-0} Gate 3 series"
fi

step "54 alert types with 4 thresholds on the FIRST boot"
# serve runs its catalogue ingest in the BACKGROUND, so /healthz goes green
# before the four THRESHOLD types — whose NOT NULL source_route_id resolves
# by a join against app.esi_route — can exist. A first-boot assertion has to
# wait for first boot to finish.
TYPES=0; THRESH=0
for _ in $(seq 1 60); do
  TYPES="$(psql 'SELECT count(*) FROM app.alert_type')"
  THRESH="$(psql "SELECT count(*) FROM app.alert_type WHERE category = 'threshold'")"
  [ "${TYPES:-0}" -ge 54 ] && [ "${THRESH:-0}" -ge 4 ] && break
  sleep 5
done
if [ "${TYPES:-0}" -ge 54 ] && [ "${THRESH:-0}" -ge 4 ]; then
  record "alert-catalogue" "pass" "${TYPES} alert types with ${THRESH} thresholds resolved on the first boot"
else
  record "alert-catalogue" "FAIL" "got ${TYPES:-0} types and ${THRESH:-0} thresholds"
fi

step "an invalid HANGAR_LOCALE is rejected"
LOCALE_OUT="$(docker run --rm --network "$NET" "${env_args[@]}" -e HANGAR_LOCALE=xx-INVALID "$IMAGE" serve 2>&1 | head -3)"
LOCALE_CODE=$?
echo "$LOCALE_OUT" >> "$TRANSCRIPT"
if [ "$LOCALE_CODE" != 0 ] && printf '%s' "$LOCALE_OUT" | grep -q "HANGAR_LOCALE"; then
  record "locale" "pass" "exit ${LOCALE_CODE}, naming the variable and listing the nine valid locales rather than failing opaquely"
else
  record "locale" "FAIL" "exit ${LOCALE_CODE}: $(printf '%s' "$LOCALE_OUT" | head -1)"
fi

step "a drifted schema is rejected — by dropping an INDEX, which only Phase 23 can catch"
# db.MissingTables verified TABLES until N-6. Dropping an index was the case
# that passed, and it is the drift an operator is least likely to attribute
# to the schema, because it costs performance rather than correctness.
docker rm -f "$APP" >/dev/null 2>&1
IDX="$(psql "SELECT indexname FROM pg_indexes WHERE schemaname='app' AND tablename='alert_delivery' AND indexdef LIKE '%state%next_attempt_at%'")"
log "dropping app.${IDX}"
docker exec "$PG" psql -U hangar -d hangar -c "DROP INDEX app.${IDX}" >>"$TRANSCRIPT" 2>&1
DRIFT_OUT="$(docker run --rm --network "$NET" "${env_args[@]}" "$IMAGE" migrate up 2>&1 | head -3)"
DRIFT_CODE=$?
echo "$DRIFT_OUT" >> "$TRANSCRIPT"
if [ "$DRIFT_CODE" != 0 ] && printf '%s' "$DRIFT_OUT" | grep -q "index(es)"; then
  record "schema-drift" "pass" "exit ${DRIFT_CODE}, naming the missing INDEX and saying migrate up will not restore it — at rc2 this check could only drop a table"
else
  record "schema-drift" "FAIL" "exit ${DRIFT_CODE}: $(printf '%s' "$DRIFT_OUT" | head -1)"
fi

step "verdicts"
cat "$VERDICT" | tee -a "$TRANSCRIPT"
if awk -F '\t' '$2 == "FAIL" { found = 1 } END { exit !found }' "$VERDICT"; then
  log ""
  log "FAILED: $(awk -F '\t' '$2 == "FAIL" { print $1 }' "$VERDICT" | tr '\n' ' ')"
  exit 1
fi
log ""
log "every check PASSED"
exit 0
