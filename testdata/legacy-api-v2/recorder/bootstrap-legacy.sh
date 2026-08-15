#!/bin/sh
# bootstrap-legacy.sh — put the four eveseat repositories at the exact commits
# testdata/legacy-api-v2/README.md pins, and apply the recorder's patches.
#
# ── WHY THIS SCRIPT EXISTS (PHASE 20.10, DEFECT B58) ─────────────────────────
# The README said the recording was "reproducible rather than asserted", and it
# was not quite. Reproducing it needed two things the repository did not carry:
# the four clones (fine — they are pinned by SHA and cloning them is the point)
# and a one-line patch to eveseat/web that lived ONLY inside one of those
# untracked clones. A fresh checkout followed the documented steps and got 464
# of 472 migrations instead of 471, with seven cascading failures whose message
# named `users.id` and `refresh_tokens.user_id` rather than the CAST that
# actually caused them.
#
# The pins are unchanged and still verified here: a clone that does not land on
# the recorded SHA is a hard failure, not a warning, because the whole value of
# the corpus is that it came from THAT code.
#
# Usage, from testdata/legacy-api-v2/recorder:
#     sh bootstrap-legacy.sh
# Then build the image and run migrate.php and record.php — see README.md.
set -eu

cd "$(dirname "$0")"

# Repository -> pinned commit. Keep in step with README.md's table; the check
# below is what makes the two agree rather than merely both existing.
pin_api=fe523ffed5e298ea913242998a2b2274ff8a65e5
pin_eveapi=ba25892e810ff1893d5462ad6e779c3b2cc57555
pin_services=b2db97fd75f03c68cf587dd05da5beba2718f360
pin_web=cd11287006bddbf00c48cf13fd1fa704b0ea25d6

fetch() {
    name=$1
    pin=$2
    dir="seat-$name"

    if [ ! -d "$dir/.git" ]; then
        echo "cloning eveseat/$name"
        git clone --quiet "https://github.com/eveseat/$name.git" "$dir"
    fi
    git -C "$dir" checkout --quiet "$pin"

    actual=$(git -C "$dir" rev-parse HEAD)
    if [ "$actual" != "$pin" ]; then
        echo "FATAL: $dir is at $actual, not the pinned $pin" >&2
        exit 1
    fi
    echo "$dir @ $pin"
}

fetch api "$pin_api"
fetch eveapi "$pin_eveapi"
fetch services "$pin_services"
fetch web "$pin_web"

# Patches are applied against seat-web's git tree so that "already applied" is
# detectable rather than guessed at: `apply --reverse --check` succeeds exactly
# when the patch is already in place, which makes this script safe to re-run.
for patch in patches/*.patch; do
    [ -e "$patch" ] || continue
    if git -C seat-web apply --reverse --check "../$patch" >/dev/null 2>&1; then
        echo "$(basename "$patch"): already applied"
    else
        git -C seat-web apply "../$patch"
        echo "$(basename "$patch"): applied"
    fi
done

echo "legacy sources ready"
