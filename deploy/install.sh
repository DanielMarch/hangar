#!/bin/sh
# Project HANGAR — turnkey installer (SRS §9.1, Gate 5).
#
# Gate 5 requires a blank environment to reach a running installation in
# three commands with NO source compilation:
#
#   1) curl -fsSLO https://raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml
#   2) curl -fsSL  https://raw.githubusercontent.com/hangar-project/hangar/main/deploy/install.sh | sh
#   3) docker compose up -d
#
# This script only ever writes .env in the current directory; it never talks
# to Docker itself and never generates a fallback key inside the binary — see
# internal/config/validate.go's fail-fast contract, which this script exists
# to satisfy honestly instead of working around.
set -eu

ENV_FILE="${HANGAR_ENV_FILE:-.env}"

# The template this script fills in. Overridable (PHASE 21) for two reasons,
# both real:
#
#   1. A fork, a mirror, or an air-gapped installation has the file somewhere
#      else, and hard-coding one host makes the installer unusable there.
#   2. Gate 5 has to be RUN. §5.1's procedure is three commands against
#      raw.githubusercontent.com, and until the repository is published those
#      URLs 404 — including this one, which is the second fetch hiding inside
#      command 2. Without an override the gate cannot be executed even in a
#      substituted environment, which is how it stayed unrun.
#
# The default is unchanged, so the documented three-command deployment is
# exactly what it was.
EXAMPLE_URL="${HANGAR_ENV_EXAMPLE_URL:-https://raw.githubusercontent.com/hangar-project/hangar/main/.env.example}"

if [ -f "$ENV_FILE" ]; then
  echo "install.sh: $ENV_FILE already exists — leaving it untouched." >&2
  echo "install.sh: delete it first if you want a fresh installation." >&2
  exit 0
fi

random_key() {
  # 32 raw bytes, base64-encoded — the format internal/config/validate.go
  # requires for HANGAR_MASTER_KEY and HANGAR_SESSION_SECRET.
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32
  else
    head -c 32 /dev/urandom | base64
  fi
}

echo "install.sh: fetching .env.example..." >&2
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$EXAMPLE_URL" -o "$ENV_FILE.tmp"
elif command -v wget >/dev/null 2>&1; then
  wget -q "$EXAMPLE_URL" -O "$ENV_FILE.tmp"
else
  echo "install.sh: need curl or wget" >&2
  exit 1
fi

MASTER_KEY="$(random_key)"
SESSION_SECRET="$(random_key)"
POSTGRES_PASSWORD="$(random_key)"

# Fill in the generated secrets; leave the two SSO values for the operator —
# per SRS §9.1, that is the ONLY input a novice administrator should have to
# supply. Everything else defaults.
sed \
  -e "s|^HANGAR_MASTER_KEY=.*|HANGAR_MASTER_KEY=${MASTER_KEY}|" \
  -e "s|^HANGAR_SESSION_SECRET=.*|HANGAR_SESSION_SECRET=${SESSION_SECRET}|" \
  -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${POSTGRES_PASSWORD}|" \
  "$ENV_FILE.tmp" > "$ENV_FILE"
rm -f "$ENV_FILE.tmp"

echo "" >&2
echo "install.sh: wrote $ENV_FILE with generated HANGAR_MASTER_KEY, HANGAR_SESSION_SECRET" >&2
echo "            and POSTGRES_PASSWORD." >&2
echo "" >&2
echo "Before running 'docker compose up -d', register an application at" >&2
echo "https://developers.eveonline.com/applications and set in $ENV_FILE:" >&2
echo "  HANGAR_SSO_CLIENT_ID=..." >&2
echo "  HANGAR_SSO_CLIENT_SECRET=..." >&2
echo "The callback URL must be exactly \${HANGAR_PUBLIC_URL}/auth/callback." >&2
