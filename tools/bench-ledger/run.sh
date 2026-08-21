#!/usr/bin/env bash
# Phase 4's exit criterion, measured off Windows port forwarding.
#
# ── WHY THIS SCRIPT EXISTS (D-1) ───────────────────────────────────────────
#
# `make bench-ledger-clustered` has FAILED on this host at every commit it
# has ever been run at: p99 10.46/10.20/10.83 ms at rc1, 10.78/10.92/10.98 ms
# at rc2, 11.59-12.28 ms at rc3, against a 10 ms budget. Three runs at each of
# three commits, so it is not a regression — and it had never been
# demonstrated PASSING anywhere, which left the roadmap's Phase 4 exit
# criterion (">= 2000 ops/s/replica at p99 < 10 ms") satisfied by nothing.
#
# Phase 20.4 already established why, by measurement rather than assertion:
# the same benchmark against the same tree, with Postgres reached over the
# LINUX BRIDGE instead of Docker Desktop's Windows port forwarding, gave p99
# 6.863-7.763 ms at 9,942-10,460 ops/s — while the same tree measured through
# Windows the same afternoon gave 11.223 ms, three runs out of three.
#
# That method was performed by hand and written into a document, which is why
# it had to be performed by hand again and why no passing run was ever
# committed. This is the same method as a command.
#
# BenchmarkLedgerClusteredTransportFloor, added in the same phase, is the
# other half: it runs the same 32 workers doing nothing but the two bare
# round trips one Acquire+Settle costs, so the artefact carries the floor
# beside the measurement rather than leaving "it is the environment" as a
# claim.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WIN_ROOT="$(cd "$REPO_ROOT" && pwd -W 2>/dev/null || echo "$REPO_ROOT")"
OUT_DIR="${1:-}"
RUNS="${2:-3}"
if [ -z "$OUT_DIR" ]; then
  echo "usage: $0 <evidence-dir> [runs]" >&2
  exit 2
fi
mkdir -p "$OUT_DIR"
TRANSCRIPT="${OUT_DIR}/bench-transcript.txt"
RESULTS="${OUT_DIR}/bench-results.txt"
: > "$TRANSCRIPT"
: > "$RESULTS"

GO_IMAGE="golang:1.26-alpine"
NET=bench-ledger-net
PG=bench-ledger-pg

log() { echo "$*" | tee -a "$TRANSCRIPT"; }

# Trap 27: name every container and force-remove it, because `--rm` only
# removes one that has EXITED and `timeout` kills the CLI rather than the
# container.
cleanup() {
  docker rm -f "$PG" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  return 0
}
trap cleanup EXIT
cleanup

log "### pulling ${GO_IMAGE}"
docker pull "$GO_IMAGE" >>"$TRANSCRIPT" 2>&1 || { log "could not pull ${GO_IMAGE}"; exit 1; }

log "### PostgreSQL 18 on a docker network, reachable over the Linux bridge"
docker network create "$NET" >>"$TRANSCRIPT" 2>&1
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=hangar -e POSTGRES_PASSWORD=hangar -e POSTGRES_DB=hangar \
  postgres:18-alpine >>"$TRANSCRIPT" 2>&1
for _ in $(seq 1 30); do
  docker exec "$PG" pg_isready -U hangar >/dev/null 2>&1 && break
  sleep 1
done

log "### ${RUNS} runs, inside the container, against ${PG} over the bridge"
for run in $(seq 1 "$RUNS"); do
  log ""
  log "--- run ${run} ---"
  # gcc is needed for nothing here: the benchmark is not run under -race,
  # and CGO_ENABLED=0 keeps the alpine image from needing a toolchain.
  OUT="$(MSYS_NO_PATHCONV=1 docker run --rm --network "$NET" \
    -v "${WIN_ROOT}:/src" -w //src \
    -e CGO_ENABLED=0 \
    -e GOFLAGS=-mod=mod \
    -e HANGAR_BENCH_DB_URL="postgres://hangar:hangar@${PG}:5432/hangar?sslmode=disable" \
    "$GO_IMAGE" \
    go test -run=XXX -tags=integration -bench='BenchmarkLedgerClustered' -benchtime=2000x \
      ./internal/esi/ratelimit/... 2>&1)"
  CODE=$?
  echo "$OUT" >> "$TRANSCRIPT"
  # The database is reused across runs, so drop what the previous one wrote
  # — a benchmark whose verdict depends on whether it has been run before is
  # not evidence (Phase 22, §10).
  docker exec "$PG" psql -U hangar -d hangar -c \
    'TRUNCATE app.esi_ledger_entry, app.esi_ledger_bucket CASCADE' >>"$TRANSCRIPT" 2>&1

  {
    echo "run ${run}: exit ${CODE}"
    echo "$OUT" | grep -E "ns/op|p99 acquire/settle|is below the 2000"
  } >> "$RESULTS"
  echo "$OUT" | grep -E "ns/op|p99 acquire/settle|is below the 2000" | tee -a "$TRANSCRIPT"
done

log ""
log "### results"
cat "$RESULTS"
