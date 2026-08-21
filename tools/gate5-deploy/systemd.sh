#!/usr/bin/env bash
# Gate 5.8, the half this host could not do: EXECUTE deploy/hangar.service
# under a real systemd.
#
# ── WHY THIS EXISTS ────────────────────────────────────────────────────────
#
# SRS §9.2 requires HANGAR to "boot via a standard systemd unit given a valid
# .env and a manually provisioned PostgreSQL 18 instance". Gate 5.8 was
# recorded PARTIAL at rc1 and rc2 with the note that "the unit file is
# deploy/ material and remains unverified" — and PHASE 23 found there was no
# unit file. §9.2's requirement had never been met by anything, and the
# artefact said "unverified" where it could have said "absent".
#
# So this runs the real thing: a systemd-capable container
# (registry.access.redhat.com/ubi9/ubi-init, which ships systemd as PID 1),
# the static linux/amd64 binary installed at §9.2's documented layout, an
# external PostgreSQL 18 in its own container, and `systemctl start hangar`.
#
# ── WHAT IT PROVES, AND WHAT IT DOES NOT ───────────────────────────────────
#
# It proves the unit parses, its ExecStartPre migrates, the service reaches
# `active (running)`, /healthz answers from inside the unit's own network
# namespace, and `systemctl stop` returns it to `inactive (dead)` without a
# failed state. It is a container, so cgroup delegation and a few sandboxing
# directives (ProtectKernelTunables and friends) are enforced more weakly
# than on metal — that is a real limit and it is recorded in the artefact
# rather than glossed.
#
# ── TRAP 27 ────────────────────────────────────────────────────────────────
#
# `timeout` kills the docker CLI, not the container, and `--rm` only removes
# a container that has EXITED. Gate 5 leaked a `hangar serve` per run at rc1
# and three were still retrying 21 hours later. Every container here is
# NAMED and force-removed in a trap.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${1:-}"
if [ -z "$OUT_DIR" ]; then
  echo "usage: $0 <evidence-dir>" >&2
  exit 2
fi
mkdir -p "$OUT_DIR"
TRANSCRIPT="${OUT_DIR}/systemd-transcript.txt"
VERDICT="${OUT_DIR}/systemd-verdict.txt"
: > "$TRANSCRIPT"
: > "$VERDICT"

INIT_IMAGE="registry.access.redhat.com/ubi9/ubi-init:latest"
NET=gate5-systemd
PG=gate5-systemd-pg
HOST=gate5-systemd-host

log()  { echo "$*" | tee -a "$TRANSCRIPT"; }
step() { log ""; log "### $*"; }

cleanup() {
  docker rm -f "$HOST" >/dev/null 2>&1
  docker rm -f "$PG"   >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  return 0
}
trap cleanup EXIT

record() { # id verdict detail
  printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$VERDICT"
  log "[$2] $1 — $3"
}

BINARY="${REPO_ROOT}/bin/hangar-linux-amd64"
if [ ! -f "$BINARY" ]; then
  record "5.8-systemd" "FAIL" "bin/hangar-linux-amd64 is absent — run 'make build-all' first"
  exit 1
fi

step "cleaning up anything a previous run left behind"
cleanup

step "pulling a systemd-capable image"
if ! docker pull "$INIT_IMAGE" >>"$TRANSCRIPT" 2>&1; then
  record "5.8-systemd" "FAIL" "could not pull ${INIT_IMAGE}"
  exit 1
fi

step "starting an external PostgreSQL 18 — §9.2's 'manually provisioned' database"
docker network create "$NET" >>"$TRANSCRIPT" 2>&1
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=hangar -e POSTGRES_PASSWORD=hangar -e POSTGRES_DB=hangar \
  postgres:18-alpine >>"$TRANSCRIPT" 2>&1
for _ in $(seq 1 30); do
  if docker exec "$PG" pg_isready -U hangar >/dev/null 2>&1; then break; fi
  sleep 1
done

step "booting systemd as PID 1"
# --privileged and the cgroup mount are what systemd needs inside a
# container. Deliberately NOT how HANGAR itself runs: the privilege is the
# init system's, and the unit still drops everything its hardening block
# names.
# MSYS_NO_PATHCONV=1 is trap 20: Git Bash rewrites every container-absolute
# path on the command line, so `-v /sys/fs/cgroup:/sys/fs/cgroup` becomes a
# path under C:/Program Files/Git and the daemon answers "Access is denied"
# — which reads like a permissions problem and is a path-translation one.
MSYS_NO_PATHCONV=1 docker run -d --name "$HOST" --network "$NET" --privileged \
  --cgroupns=host \
  --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  "$INIT_IMAGE" /sbin/init >>"$TRANSCRIPT" 2>&1

READY=no
for _ in $(seq 1 60); do
  if docker exec "$HOST" systemctl is-system-running --wait 2>/dev/null | grep -qE 'running|degraded'; then
    READY=yes
    break
  fi
  sleep 1
done
if [ "$READY" != yes ]; then
  record "5.8-systemd" "FAIL" "systemd never reached a running state inside the container"
  docker exec "$HOST" systemctl status --no-pager >>"$TRANSCRIPT" 2>&1
  exit 1
fi
log "systemd version: $(docker exec "$HOST" systemctl --version | head -1)"

step "installing HANGAR the way deploy/hangar.service's header says to"
# Trap 20, the other half. MSYS_NO_PATHCONV=1 suppresses translation for
# BOTH sides of a docker cp, so the container destination survives and the
# HOST source has to be given in Windows form itself. `pwd -W` is Git Bash's
# own answer to that; on a real POSIX shell it fails and REPO_ROOT is
# already correct.
WIN_ROOT="$(cd "$REPO_ROOT" && pwd -W 2>/dev/null || echo "$REPO_ROOT")"
MSYS_NO_PATHCONV=1 docker cp "${WIN_ROOT}/bin/hangar-linux-amd64" "${HOST}:/tmp/hangar" >>"$TRANSCRIPT" 2>&1
MSYS_NO_PATHCONV=1 docker cp "${WIN_ROOT}/deploy/hangar.service" "${HOST}:/tmp/hangar.service" >>"$TRANSCRIPT" 2>&1

MASTER_KEY="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
SESSION_SECRET="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"

docker exec "$HOST" bash -lc "
set -e
install -D -m 0755 /tmp/hangar /opt/hangar/bin/hangar
install -d -m 0755 /opt/hangar
install -D -m 0644 /tmp/hangar.service /etc/systemd/system/hangar.service
useradd --system --home-dir /opt/hangar --shell /sbin/nologin hangar 2>/dev/null || true
chown -R hangar:hangar /opt/hangar
install -d -m 0750 /etc/hangar
cat > /etc/hangar/hangar.env <<EOF
HANGAR_DB_URL=postgres://hangar:hangar@${PG}:5432/hangar?sslmode=disable
HANGAR_MASTER_KEY=${MASTER_KEY}
HANGAR_SESSION_SECRET=${SESSION_SECRET}
HANGAR_SSO_CLIENT_ID=gate5-systemd
HANGAR_SSO_CLIENT_SECRET=gate5-systemd
HANGAR_PUBLIC_URL=http://localhost:8080
HANGAR_SSO_CALLBACK_URL=http://localhost:8080/auth/callback
HANGAR_ESI_STARTUP_CATALOGUE_INGEST=false
EOF
chmod 0640 /etc/hangar/hangar.env
chown root:hangar /etc/hangar/hangar.env
systemctl daemon-reload
" >>"$TRANSCRIPT" 2>&1
INSTALL_CODE=$?
if [ "$INSTALL_CODE" != 0 ]; then
  record "5.8-systemd" "FAIL" "installing the unit and its environment failed (exit ${INSTALL_CODE})"
  exit 1
fi

step "systemd-analyze verify — does the unit PARSE"
# Run before starting anything: a unit with a typo starts fine and ignores
# the directive it could not read, which is the failure mode a hardening
# block is most likely to have.
VERIFY_OUT="$(docker exec "$HOST" systemd-analyze verify /etc/systemd/system/hangar.service 2>&1)"
VERIFY_CODE=$?
echo "$VERIFY_OUT" >> "$TRANSCRIPT"
# systemd-analyze reports the missing postgresql.service dependency and the
# nologin shell as warnings on a container that has neither. Those are
# expected here and are not what this checks.
if echo "$VERIFY_OUT" | grep -qiE 'unknown (lvalue|section)|invalid|failed to parse'; then
  record "5.8-verify" "FAIL" "systemd-analyze rejected the unit: $(echo "$VERIFY_OUT" | head -3 | tr '\n' ' ')"
else
  # The exit code is NOT the signal here, and saying so in the artefact is
  # the point: systemd-analyze exits 1 on this image for "Couldn't process
  # aliases: No such file or directory" — a container with no
  # /etc/systemd/system alias tree, nothing to do with the unit. What is
  # checked is the absence of a parse complaint, because an unknown lvalue
  # is silently IGNORED at load time, which is the failure mode a hardening
  # block is most likely to have and the one a green `systemctl start` will
  # not show you.
  record "5.8-verify" "PASS" "systemd-analyze verify reported no unknown lvalue, unknown section, or parse failure. It exits ${VERIFY_CODE} on this image for an unrelated missing alias directory (see systemd-transcript.txt); the verdict is the absence of a parse complaint, not the exit code"
fi

step "systemctl start hangar"
docker exec "$HOST" systemctl start hangar >>"$TRANSCRIPT" 2>&1
START_CODE=$?

ACTIVE=no
for _ in $(seq 1 60); do
  if docker exec "$HOST" systemctl is-active --quiet hangar; then ACTIVE=yes; break; fi
  # A unit that has already failed will never become active; stop waiting.
  if docker exec "$HOST" systemctl is-failed --quiet hangar; then break; fi
  sleep 2
done

docker exec "$HOST" systemctl status hangar --no-pager -l >>"$TRANSCRIPT" 2>&1
docker exec "$HOST" journalctl -u hangar --no-pager >>"$TRANSCRIPT" 2>&1

if [ "$ACTIVE" != yes ]; then
  record "5.8-start" "FAIL" "the unit did not reach active (start exit ${START_CODE}); see systemd-transcript.txt"
  exit 1
fi
record "5.8-start" "PASS" "systemctl start hangar reached active (running), with ExecStartPre 'hangar migrate up' completing first"

step "the migration ran as part of the unit, not as a separate step"
if docker exec "$HOST" journalctl -u hangar --no-pager | grep -q "schema current"; then
  record "5.8-migrate" "PASS" "ExecStartPre migrated the external PostgreSQL 18 and verified the schema (tables, columns and indexes) in the unit's own journal"
else
  record "5.8-migrate" "FAIL" "the unit's journal carries no successful 'migrate up' — see systemd-transcript.txt"
fi

step "/healthz, from inside the unit's own network namespace"
HEALTH="$(docker exec "$HOST" bash -lc 'curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/healthz' 2>&1)"
if [ "$HEALTH" = 200 ]; then
  record "5.8-healthz" "PASS" "GET /healthz answered 200 from the systemd-managed process"
else
  record "5.8-healthz" "FAIL" "GET /healthz answered ${HEALTH}"
fi

step "systemctl stop — a clean shutdown, not a kill"
docker exec "$HOST" systemctl stop hangar >>"$TRANSCRIPT" 2>&1
sleep 2
STATE="$(docker exec "$HOST" systemctl is-active hangar 2>&1 | tr -d '\r\n')"
FAILED="$(docker exec "$HOST" systemctl is-failed hangar 2>&1 | tr -d '\r\n')"
docker exec "$HOST" journalctl -u hangar --no-pager >>"$TRANSCRIPT" 2>&1
if [ "$STATE" = "inactive" ] && [ "$FAILED" != "failed" ]; then
  record "5.8-stop" "PASS" "SIGTERM inside TimeoutStopSec: the unit is inactive (dead) and not failed"
else
  record "5.8-stop" "FAIL" "after stop the unit is is-active=${STATE} is-failed=${FAILED}"
fi

step "verdicts"
cat "$VERDICT" | tee -a "$TRANSCRIPT"
if grep -q "	FAIL	" "$VERDICT"; then
  exit 1
fi
exit 0
