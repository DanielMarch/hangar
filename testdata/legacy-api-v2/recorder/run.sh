#!/bin/sh
# run.sh — entrypoint inside the hangar-corpus-recorder image.
# $1 is the script under src/ to run (migrate.php, record.php).
set -eu
if [ ! -d /app/vendor ] || [ ! -f /app/.deps-ready ]; then
    composer install --no-interaction --no-progress --no-security-blocking >/dev/null 2>&1 \
        || composer update --no-interaction --no-progress --no-security-blocking 2>&1 | tail -3
    touch /app/.deps-ready
fi
exec php "/app/src/$1"
