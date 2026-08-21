#!/usr/bin/env bash
# N-9's proof: a STOCK compose stack generates an alert event and delivers a
# message.
#
# ── WHAT IT IS PROVING ─────────────────────────────────────────────────────
#
# Before Phase 23, wireAlertGeneration, runThresholdEvaluator,
# runAlertDispatcher and ensureDefaultAlertChannels were called from
# cmd/hangar/work.go and nowhere else, and docker-compose.yml's only `hangar`
# service is `command: ["serve"]`. So a default installation synchronised,
# provisioned, served and swept, and §4.4 was entirely absent from it: no
# alert event was ever written, and no message was ever delivered.
#
# Gate 3 could not see this. It runs the pump itself, which is exactly why a
# subsystem can pass a gate and not exist on any installation.
#
# So this asserts the thing Gate 3 cannot: with NOTHING running but what
# `docker compose up` starts, an event lands in app.alert_event and a
# delivery reaches a channel. The channel is a container running netcat that
# records what arrives, because "the pump marked it sent" is the pump's
# opinion and a receiver's transcript is not.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Git Bash: a container bind-mount source has to be a Windows path, and
# MSYS_NO_PATHCONV suppresses translation for both sides of the argument.
WIN_TMP="$(cd /tmp && pwd -W 2>/dev/null || echo /tmp)"

# ── KEYS THAT CANNOT SHORT-READ ──────────────────────────────────────────
# `head -c 32 /dev/urandom | base64` failed a run with "HANGAR_SESSION_
# SECRET: must be base64-encoded 32 bytes". On Windows, head can stop early
# on a 0x1A in the byte stream, so the key was intermittently SHORT — a
# harness bug that looks exactly like a configuration bug, and only
# sometimes. openssl reads a fixed count.
newkey() { openssl rand -base64 32 | tr -d '\r\n'; }
OUT_DIR="${1:-}"
IMAGE="${HANGAR_IMAGE:-hangar:phase23-dev}"
if [ -z "$OUT_DIR" ]; then
  echo "usage: $0 <evidence-dir>" >&2
  exit 2
fi
mkdir -p "$OUT_DIR"
TRANSCRIPT="${OUT_DIR}/n9-compose-transcript.txt"
VERDICT="${OUT_DIR}/n9-compose-verdict.txt"
: > "$TRANSCRIPT"
: > "$VERDICT"

# ── EVERY NAME IS UNIQUE TO THIS INVOCATION ─────────────────────────────
#
# Trap 27 says name every container so it can be force-removed. That is
# right and it is not enough: three runs of this script were destroyed
# MID-RUN by the EXIT trap of an EARLIER invocation that was still alive and
# owned the same names. Each time the symptom was "No such container" from
# a psql that had worked a moment before, which reads like a database fault.
#
# So the names are unique per invocation and the cleanup only ever removes
# its own. A runner whose result depends on what else is running is the same
# defect as a gate whose verdict depends on whether it has been run before.
RUN_ID=$$
NET=n9-proof-net-${RUN_ID}
PG=n9-proof-pg-${RUN_ID}
APP=n9-proof-hangar-${RUN_ID}
SINK=n9-proof-sink-${RUN_ID}

log()  { echo "$*" | tee -a "$TRANSCRIPT"; }
step() { log ""; log "### $*"; }
record() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$VERDICT"; log "[$2] $1 — $3"; }

# Trap 27.
cleanup() {
  docker rm -f "$APP" "$SINK" "$PG" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  return 0
}
trap cleanup EXIT

psql() { docker exec "$PG" psql -U hangar -d hangar -tAc "$1" 2>>"$TRANSCRIPT"; }

step "a fresh PostgreSQL 18, as compose's `postgres` service"
docker network create "$NET" >>"$TRANSCRIPT" 2>&1
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=hangar -e POSTGRES_PASSWORD=hangar -e POSTGRES_DB=hangar \
  postgres:18-alpine >>"$TRANSCRIPT" 2>&1
for _ in $(seq 1 40); do docker exec "$PG" pg_isready -U hangar >/dev/null 2>&1 && break; sleep 1; done

step "a webhook sink — the receiver, so 'delivered' is not the pump's own opinion"
# A real HTTP server that records every request body and answers 200 with the
# body Slack itself returns.
#
# ── TWO EARLIER VERSIONS OF THIS WERE THE BUG, NOT HANGAR ────────────────
#
# The first used `socat -u`, and -u is UNIDIRECTIONAL: it piped the request
# into the handler and never wrote the handler's reply back. HANGAR posted a
# correct, fully rendered Slack payload, waited for headers that were never
# coming, and recorded a retry — exactly right against a receiver that does
# not answer. The verdict read "no delivery reached 'sent'" and the whole
# fault was here.
#
# The second dropped -u and drowned in shell quoting three levels deep.
#
# So it is a Python one-file server: no nested quoting, a real HTTP stack,
# and a transcript written by the RECEIVER rather than inferred from the
# sender's own log.
cat > /tmp/n9-sink.py <<'SINKPY'
import http.server


class Sink(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        with open("/tmp/received.txt", "ab") as handle:
            handle.write(self.path.encode() + b" " + body + b"\n")
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, *args):
        pass


http.server.HTTPServer(("0.0.0.0", 9000), Sink).serve_forever()
SINKPY
# docker cp rather than a bind mount. A Windows bind-mount source has to be
# a translated path AND inside Docker Desktop's shared drives, and when it is
# not, the run fails with a message about the mount rather than about the
# path. Copying into a container that is already up has neither failure mode.
docker run -d --name "$SINK" --network "$NET" python:3.12-alpine sleep infinity >>"$TRANSCRIPT" 2>&1
sleep 3
MSYS_NO_PATHCONV=1 docker cp "${WIN_TMP}/n9-sink.py" "${SINK}:/tmp/sink.py" >>"$TRANSCRIPT" 2>&1
docker exec -d "$SINK" sh -c ': > /tmp/received.txt && python /tmp/sink.py' >>"$TRANSCRIPT" 2>&1
sleep 5
if [ "$(docker inspect -f '{{.State.Running}}' "$SINK" 2>/dev/null)" != true ]; then
  record "n9-sink" "FAIL" "the webhook sink container is not running"
  docker logs "$SINK" >>"$TRANSCRIPT" 2>&1
  exit 1
fi

step "migrate up — compose's one-shot `hangar-migrate` service"
docker run --rm --network "$NET" \
  -e HANGAR_DB_URL="postgres://hangar:hangar@${PG}:5432/hangar?sslmode=disable" \
  -e HANGAR_MASTER_KEY="$(newkey)" \
  -e HANGAR_SESSION_SECRET="$(newkey)" \
  -e HANGAR_SSO_CLIENT_ID=n9 -e HANGAR_SSO_CLIENT_SECRET=n9 \
  "$IMAGE" migrate up >>"$TRANSCRIPT" 2>&1
MIGRATE_CODE=$?
if [ "$MIGRATE_CODE" != 0 ]; then
  record "n9-migrate" "FAIL" "migrate up exited ${MIGRATE_CODE}"
  exit 1
fi

MASTER_KEY="$(newkey)"
SESSION_SECRET="$(newkey)"

step "ONE hangar service, command: [\"serve\"] — exactly what compose runs"
# HANGAR_SLACK_DEFAULT_WEBHOOK points at the sink, which is the ONLY thing
# here that differs from a stock stack: an installation with no channel
# configured has nowhere to deliver, and that is a valid installation rather
# than a defect. The routing rule below is likewise an operator action — the
# one N-4's admin surface now makes possible without SQL.
docker run -d --name "$APP" --network "$NET" \
  -e HANGAR_DB_URL="postgres://hangar:hangar@${PG}:5432/hangar?sslmode=disable" \
  -e HANGAR_MASTER_KEY="$MASTER_KEY" \
  -e HANGAR_SESSION_SECRET="$SESSION_SECRET" \
  -e HANGAR_SSO_CLIENT_ID=n9 -e HANGAR_SSO_CLIENT_SECRET=n9 \
  -e HANGAR_PUBLIC_URL=http://localhost:8080 \
  -e HANGAR_SSO_CALLBACK_URL=http://localhost:8080/auth/callback \
  -e HANGAR_SLACK_DEFAULT_WEBHOOK="http://${SINK}:9000/hook" \
  -e HANGAR_ALERT_DISPATCH_INTERVAL=5s \
  -e HANGAR_ALERT_COALESCE_WINDOW=10s \
  -e HANGAR_ALERT_THRESHOLD_INTERVAL=15s \
  -e HANGAR_METRICS_ADDR=0.0.0.0:9090 \
  "$IMAGE" serve >>"$TRANSCRIPT" 2>&1

HEALTHY=no
for _ in $(seq 1 60); do
  if docker exec "$APP" /hangar healthcheck >/dev/null 2>&1 ||
     docker run --rm --network "$NET" alpine:3.20 sh -c "wget -qO- http://${APP}:8080/healthz" >/dev/null 2>&1; then
    HEALTHY=yes; break
  fi
  sleep 2
done
if [ "$HEALTHY" != yes ]; then
  record "n9-serve" "FAIL" "the serve container never became healthy"
  docker logs "$APP" >>"$TRANSCRIPT" 2>&1
  exit 1
fi
record "n9-serve" "PASS" "one container, command serve, healthy"

step "wait for the alert catalogue — 54 types with the 4 thresholds resolved (trap 22)"
# `serve` runs its catalogue ingest in the BACKGROUND, so /healthz goes green
# before it finishes. Four of the 54 alert types are THRESHOLD types whose
# NOT NULL source_route_id is resolved by a join against app.esi_route, which
# is empty until the spec is ingested — so until this completes no routing
# rule can be created for them and they cannot fire. A stock compose stack
# does exactly this ingest; disabling it would have measured a different
# installation from the one an operator gets.
TYPES=0
THRESHOLDS=0
for _ in $(seq 1 60); do
  TYPES="$(psql "SELECT count(*) FROM app.alert_type")"
  THRESHOLDS="$(psql "SELECT count(*) FROM app.alert_type WHERE source_route_id IS NOT NULL")"
  [ "${TYPES:-0}" -ge 54 ] && [ "${THRESHOLDS:-0}" -ge 4 ] && break
  sleep 5
done
if [ "${TYPES:-0}" -ge 54 ] && [ "${THRESHOLDS:-0}" -ge 4 ]; then
  record "n9-catalogue" "PASS" "${TYPES} alert types with ${THRESHOLDS} thresholds resolved by serve's own startup ingest"
else
  record "n9-catalogue" "FAIL" "the catalogue never completed: ${TYPES} types, ${THRESHOLDS} thresholds"
  docker logs "$APP" >>"$TRANSCRIPT" 2>&1
  exit 1
fi

step "the default channel — provisioned by ensureDefaultAlertChannels, which only ran under \`work\` before"
CHANNELS="$(psql "SELECT count(*) FROM app.alert_channel WHERE name = 'default-slack'")"
if [ "${CHANNELS:-0}" -ge 1 ]; then
  record "n9-default-channel" "PASS" "serve provisioned app.alert_channel 'default-slack' from HANGAR_SLACK_DEFAULT_WEBHOOK"
else
  record "n9-default-channel" "FAIL" "no default-slack channel row: ensureDefaultAlertChannels did not run under serve"
  exit 1
fi

step "route one alert type to it — the operator action §4.4 requires"
CHANNEL_ID="$(psql "SELECT channel_id FROM app.alert_channel WHERE name = 'default-slack' LIMIT 1")"
psql "INSERT INTO app.alert_routing_rule (alert_type, target_kind, target_ref, channel_id)
      VALUES ('StructureUnderAttack', 'installation', NULL, '${CHANNEL_ID}')" >>"$TRANSCRIPT" 2>&1

step "produce an alert event through the EMITTER, not by inserting a row"
# app.alert_event is written by alerting.Emitter and by nothing else. The
# notification producer is a package-level hook fired from the sync handlers,
# which need real ESI; the THRESHOLD producer needs only data, so this seeds
# a structure whose fuel expires inside the threshold window and lets
# `serve`'s own evaluator find it. Nothing here writes to app.alert_event.
psql "INSERT INTO app.corporation (corporation_id, name, ticker) VALUES (98000001, 'N9 Proof Corp', 'N9PRF')
      ON CONFLICT (corporation_id) DO NOTHING" >>"$TRANSCRIPT" 2>&1
psql "INSERT INTO app.corporation_structure (corporation_id, structure_id, type_id, system_id, state, fuel_expires)
      VALUES (98000001, 1035000000001, 35832, 30000142, 'shield_vulnerable', now() + interval '6 hours')
      ON CONFLICT (corporation_id, structure_id) DO UPDATE SET fuel_expires = EXCLUDED.fuel_expires" >>"$TRANSCRIPT" 2>&1
psql "INSERT INTO app.alert_routing_rule (alert_type, target_kind, target_ref, channel_id)
      VALUES ('corporation.structure.fuel_low', 'installation', NULL, '${CHANNEL_ID}')" >>"$TRANSCRIPT" 2>&1

step "wait for the threshold evaluator and the pump — both of which used to run only under \`work\`"
EVENTS=0
DELIVERED=0
for _ in $(seq 1 90); do
  EVENTS="$(psql "SELECT count(*) FROM app.alert_event")"
  DELIVERED="$(psql "SELECT count(*) FROM app.alert_delivery WHERE state = 'sent'")"
  [ "${DELIVERED:-0}" -ge 1 ] && break
  sleep 10
done

docker logs "$APP" >>"$TRANSCRIPT" 2>&1

if [ "${EVENTS:-0}" -ge 1 ]; then
  record "n9-event" "PASS" "app.alert_event holds ${EVENTS} row(s), written by serve's own threshold evaluator — before this phase it held none on any default installation"
else
  record "n9-event" "FAIL" "app.alert_event is empty: no producer ran under serve"
fi

if [ "${DELIVERED:-0}" -ge 1 ]; then
  record "n9-delivered" "PASS" "${DELIVERED} delivery(ies) reached state 'sent' through serve's own pump"
else
  PENDING="$(psql "SELECT count(*) FROM app.alert_delivery")"
  record "n9-delivered" "FAIL" "no delivery reached 'sent' (${PENDING:-0} enqueued)"
fi

step "the RECEIVER's transcript — not the pump's opinion"
RECEIVED="$(docker exec "$SINK" sh -c 'wc -c < /tmp/received.txt' 2>>"$TRANSCRIPT" | tr -d ' \r\n')"
docker exec "$SINK" sh -c 'cat /tmp/received.txt' >> "$TRANSCRIPT" 2>&1
if [ "${RECEIVED:-0}" -gt 0 ]; then
  record "n9-received" "PASS" "the webhook sink recorded ${RECEIVED} bytes of delivered message — see n9-compose-transcript.txt"
else
  record "n9-received" "FAIL" "the webhook sink received nothing"
fi

step "Gate 3's two metrics, which serve exported as nil before this phase"
# The names carry NO `hangar_` prefix — telemetry.NewRegistry sets no
# Namespace, so they are literally alert_delivery_total and
# alert_dead_letter_depth. An earlier version of this check grepped for the
# prefixed spelling, found nothing, and reported that serve exports neither
# Gate 3 metric. It was reading for a name that has never existed.
#
# On failure the scrape is written to the transcript, so the next reader
# sees what IS exported instead of only what was looked for.
SCRAPE="$(docker run --rm --network "$NET" alpine:3.20 sh -c "wget -qO- http://${APP}:9090/metrics" 2>>"$TRANSCRIPT")"
DELIVERY="$(printf '%s\n' "$SCRAPE" | grep -cE '^alert_delivery_total')"
DEADLETTER="$(printf '%s\n' "$SCRAPE" | grep -cE '^alert_dead_letter_depth')"
if [ "${DELIVERY:-0}" -ge 1 ] && [ "${DEADLETTER:-0}" -ge 1 ]; then
  record "n9-metrics" "PASS" "serve's /metrics exports alert_delivery_total (${DELIVERY} series) and alert_dead_letter_depth (${DEADLETTER}) — both were literal nils on serve's registry before this phase"
else
  printf '%s\n' "$SCRAPE" | grep -E '^[a-z]' >> "$TRANSCRIPT"
  record "n9-metrics" "FAIL" "serve's /metrics has alert_delivery_total=${DELIVERY} alert_dead_letter_depth=${DEADLETTER}; the full scrape is in the transcript"
fi

step "verdicts"
cat "$VERDICT" | tee -a "$TRANSCRIPT"
# ── THIS EXITED 0 WITH TWO FAILING VERDICTS ──────────────────────────────
# The test was `grep -q "<tab>FAIL<tab>"`, and the tabs did not survive
# being written to the file, so the pattern matched nothing and a run with
# two red verdicts reported success. A runner that cannot fail is not
# evidence — §10's whole lesson — so the check is now on the second
# FIELD via awk, which does not depend on how whitespace was typed.
if awk -F '\t' '$2 == "FAIL" { found = 1 } END { exit !found }' "$VERDICT"; then
  log ""
  log "FAILED: $(awk -F '\t' '$2 == "FAIL" { print $1 }' "$VERDICT" | tr '\n' ' ')"
  exit 1
fi
log ""
log "every verdict PASSED"
exit 0
