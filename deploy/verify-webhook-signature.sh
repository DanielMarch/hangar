#!/bin/sh
#
# verify-webhook-signature.sh — the reference implementation of HANGAR's
# outbound webhook signature check (SRS §4.9).
#
# This script ships with the release for one reason: the single most common
# webhook support case is a receiver that has implemented verification
# ALMOST right and cannot tell why signatures fail. Rather than describing
# the construction in prose and hoping, HANGAR ships something an integrator
# can run against a real captured delivery, that either says "valid" or says
# exactly which step disagreed.
#
# Requires: POSIX sh and openssl. Nothing else — deliberately, so it runs on
# a receiver's host without installing anything.
#
# ─────────────────────────────────────────────────────────────────────────
# THE CONSTRUCTION, IN FULL
#
# HANGAR sends:
#
#     POST /your/endpoint
#     Content-Type: application/json
#     X-Hangar-Signature: t=1770000000,v1=6f1c...9ab
#     X-Hangar-Delivery:  0194f0d2-...        (stable across retries)
#     X-Hangar-Attempt:   1
#
#     {"event_id":"...","event_type":"...", ... }
#
# The signature is:
#
#     v1 = HMAC-SHA256(secret, "<t>" + "." + "<raw request body>")
#
# hex-encoded, lower case. `t` is the Unix timestamp in seconds, and it is
# INSIDE the signed string — that is what makes the replay window
# enforceable. A verifier that signs only the body, and reads `t` from the
# header without covering it, gains nothing from the window: an attacker
# replaying a captured delivery simply rewrites `t`.
#
# ─────────────────────────────────────────────────────────────────────────
# MORE THAN ONE v1= DURING A SECRET ROTATION
#
# When the endpoint's owner rotates the signing secret, HANGAR signs every
# delivery with BOTH the new secret and the superseded one for a grace
# window, and the header then carries two elements:
#
#     X-Hangar-Signature: t=1770000000,v1=<new>,v1=<previous>
#
# ACCEPT IF ANY ELEMENT MATCHES. That overlap is what lets you update your
# stored secret at your leisure instead of during a coordinated cutover, and
# it is why deliveries queued before the rotation are not lost. A verifier
# that reads only the first v1= will reject perfectly valid deliveries for
# the whole window if it happens to hold the other secret — this script used
# to do exactly that, and it is fixed here.
#
# The window is bounded: once it passes, only the current secret is signed
# with, and a receiver still holding the old one starts failing. That is the
# point of a rotation.
#
# ─────────────────────────────────────────────────────────────────────────
# THE FOUR WAYS RECEIVERS GET THIS WRONG
#
# 1. Signing a RE-SERIALISED body. The signature covers the exact bytes on
#    the wire. If your framework parses JSON into a map and you re-encode it
#    to verify, key order, number formatting and whitespace can all change,
#    and the HMAC will not match — intermittently, depending on the payload,
#    which is the worst way for this to fail. Capture the raw body BEFORE
#    parsing. In Express use `express.raw()`; in Flask `request.get_data()`;
#    in Go read `req.Body` once into a []byte and parse from that.
#
# 2. Forgetting the "." separator, or using the header value verbatim as the
#    signed string. It is `<t>.<body>`, one ASCII full stop, no spaces.
#
# 3. Comparing with `==`. A byte-wise early-exit comparison leaks, through
#    timing, how many leading bytes of a forged tag were correct. Use
#    hmac.compare_digest (Python), crypto.timingSafeEqual (Node),
#    subtle.ConstantTimeCompare (Go), hash_equals (PHP), Rack::Utils
#    .secure_compare (Ruby). See the note on this script's own comparison
#    below.
#
# 4. Skipping the replay window. A valid signature on a delivery from three
#    weeks ago is still a valid signature. Reject anything further than a
#    few minutes from your own clock, in EITHER direction — a timestamp far
#    in the future is an attacker choosing their own expiry.
#
# ─────────────────────────────────────────────────────────────────────────
# USAGE
#
#   HANGAR_WEBHOOK_SECRET=<hex> \
#     ./verify-webhook-signature.sh \
#       --signature "$HTTP_X_HANGAR_SIGNATURE" \
#       --body-file /path/to/raw-body.json
#
# Options:
#   --signature <value>    the X-Hangar-Signature header value (required)
#   --body-file <path>     file holding the RAW request body (required;
#                          use - for stdin)
#   --secret-file <path>   read the hex secret from a file instead of the
#                          HANGAR_WEBHOOK_SECRET environment variable
#   --window <seconds>     replay window, default 300
#   --now <unix>           override "now", for testing against a captured
#                          delivery whose timestamp has since aged out
#
# The secret is taken from the environment or a file, never from an
# argument: process arguments are world-readable in `ps` on most systems and
# land in shell history.
#
# Exit status: 0 valid; 1 invalid; 2 usage error.

set -eu

PROG=$(basename "$0")

die_usage() {
    echo "$PROG: $1" >&2
    echo "run '$PROG --help' for usage" >&2
    exit 2
}

fail() {
    echo "SIGNATURE INVALID: $1" >&2
    exit 1
}

SIGNATURE=''
BODY_FILE=''
SECRET_FILE=''
WINDOW=300
NOW=''

while [ $# -gt 0 ]; do
    case "$1" in
        --signature)   [ $# -ge 2 ] || die_usage "--signature needs a value";   SIGNATURE=$2;   shift 2 ;;
        --body-file)   [ $# -ge 2 ] || die_usage "--body-file needs a value";   BODY_FILE=$2;   shift 2 ;;
        --secret-file) [ $# -ge 2 ] || die_usage "--secret-file needs a value"; SECRET_FILE=$2; shift 2 ;;
        --window)      [ $# -ge 2 ] || die_usage "--window needs a value";      WINDOW=$2;      shift 2 ;;
        --now)         [ $# -ge 2 ] || die_usage "--now needs a value";         NOW=$2;         shift 2 ;;
        -h|--help)     sed -n '2,110p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *)             die_usage "unknown argument '$1'" ;;
    esac
done

[ -n "$SIGNATURE" ] || die_usage "--signature is required"
[ -n "$BODY_FILE" ] || die_usage "--body-file is required"

command -v openssl >/dev/null 2>&1 || die_usage "openssl is required but not on PATH"

# ── the secret ───────────────────────────────────────────────────────────
if [ -n "$SECRET_FILE" ]; then
    [ -r "$SECRET_FILE" ] || die_usage "cannot read secret file '$SECRET_FILE'"
    SECRET=$(tr -d ' \t\n\r' < "$SECRET_FILE")
else
    SECRET=${HANGAR_WEBHOOK_SECRET:-}
    [ -n "$SECRET" ] || die_usage "set HANGAR_WEBHOOK_SECRET (hex) or pass --secret-file"
fi

# HANGAR presents an endpoint's secret as lower-case hex. Reject anything
# else loudly rather than silently HMAC-ing the literal characters of a
# base64 string, which would produce a wrong-but-plausible tag and send the
# integrator hunting in the wrong place.
if ! printf '%s' "$SECRET" | grep -Eq '^[0-9a-fA-F]+$'; then
    die_usage "the secret must be hex (as HANGAR displays it), got something else"
fi
if [ $(( ${#SECRET} % 2 )) -ne 0 ]; then
    die_usage "hex secret has an odd number of characters"
fi

# ── parse t= and v1= out of the header ───────────────────────────────────
# Unknown elements are ignored on purpose: a future v2= must be able to
# appear alongside v1= without breaking receivers written against v1.
extract_first() {
    printf '%s' "$SIGNATURE" | tr ',' '
' | sed -n "s/^[[:space:]]*$1=//p" | head -n 1
}

# EVERY matching element, one per line — see the rotation note at the top of
# this file for why there can be more than one v1=.
extract_all() {
    printf '%s' "$SIGNATURE" | tr ',' '
' | sed -n "s/^[[:space:]]*$1=//p"
}

TS=$(extract_first 't')
PROVIDED_ALL=$(extract_all 'v1')

[ -n "$TS" ]           || fail "header has no t= element (got: $SIGNATURE)"
[ -n "$PROVIDED_ALL" ] || fail "header has no v1= element (got: $SIGNATURE)"

printf '%s' "$TS" | grep -Eq '^-?[0-9]+$' || fail "t= is not an integer: $TS"
for one in $PROVIDED_ALL; do
    printf '%s' "$one" | grep -Eq '^[0-9a-fA-F]{64}$' || fail "v1= is not 64 hex characters: $one"
done

# ── replay window ────────────────────────────────────────────────────────
[ -n "$NOW" ] || NOW=$(date -u +%s)
printf '%s' "$NOW" | grep -Eq '^-?[0-9]+$' || die_usage "--now must be a Unix timestamp"

DRIFT=$(( NOW - TS ))
[ "$DRIFT" -ge 0 ] || DRIFT=$(( -DRIFT ))
if [ "$DRIFT" -gt "$WINDOW" ]; then
    fail "timestamp is ${DRIFT}s from now, outside the ${WINDOW}s replay window (t=$TS, now=$NOW)"
fi

# ── recompute ────────────────────────────────────────────────────────────
# The signed string is "<t>.<raw body>". Built by streaming the body rather
# than substituting it into a shell variable: a body containing NUL bytes,
# or simply a large one, must not be mangled or truncated on the way to the
# HMAC. `printf` for the prefix and `cat` for the body means openssl sees
# exactly the bytes that were on the wire.
if [ "$BODY_FILE" = "-" ]; then
    BODY_SOURCE=$(mktemp)
    trap 'rm -f "$BODY_SOURCE"' EXIT INT TERM
    cat > "$BODY_SOURCE"
else
    [ -r "$BODY_FILE" ] || die_usage "cannot read body file '$BODY_FILE'"
    BODY_SOURCE=$BODY_FILE
fi

hmac_hex() {
    # $1 = hex key. Reads the message on stdin, prints lower-case hex.
    openssl dgst -sha256 -mac HMAC -macopt "hexkey:$1" -r 2>/dev/null \
        | cut -d' ' -f1 \
        | tr 'A-Z' 'a-z'
}

EXPECTED=$( { printf '%s.' "$TS"; cat "$BODY_SOURCE"; } | hmac_hex "$SECRET" )

[ -n "$EXPECTED" ] || fail "openssl produced no digest — check that this openssl supports '-mac HMAC'"

# ── compare ──────────────────────────────────────────────────────────────
# A shell has no constant-time string comparison, and `[ "$a" = "$b" ]` is
# certainly not one. Rather than pretend, this uses the standard double-HMAC
# trick: both values are first HMAC'd under a fresh random key, and the
# BLINDED digests are compared. An attacker cannot predict the blinding key,
# so learning how many leading bytes of the blinded values matched tells
# them nothing about the real tag, and a variable-time compare becomes
# harmless.
#
# Your receiver should not need this — use your language's constant-time
# primitive (listed at the top of this file). It is here because a reference
# script that demonstrated the insecure comparison would be teaching the
# wrong thing to the exact audience least able to spot it.
BLIND=$(openssl rand -hex 32)
EXPECTED_BLINDED=$(printf '%s' "$EXPECTED" | hmac_hex "$BLIND")

# Every element is compared and the loop deliberately does not stop at the
# first match, so the work done does not depend on WHICH secret signed the
# delivery — during a rotation overlap that would otherwise leak, through
# timing, whether the sender had rotated yet.
MATCHED=0
for one in $PROVIDED_ALL; do
    if [ "$EXPECTED_BLINDED" = "$(printf '%s' "$(printf '%s' "$one" | tr 'A-Z' 'a-z')" | hmac_hex "$BLIND")" ]; then
        MATCHED=1
    fi
done

if [ "$MATCHED" -eq 1 ]; then
    echo "SIGNATURE VALID"
    echo "  timestamp : $TS ($(( DRIFT ))s from now, window ${WINDOW}s)"
    echo "  digest    : $EXPECTED"
    exit 0
fi

echo "SIGNATURE INVALID: computed digest does not match any v1= element" >&2
echo "  expected (computed here) : $EXPECTED" >&2
echo "  provided (in the header) : $(echo "$PROVIDED_ALL" | tr '
' ' ')" >&2
echo >&2
echo "Most likely causes, in order:" >&2
echo "  1. the body file is not the RAW bytes received (re-serialised JSON?)" >&2
echo "  2. the secret does not belong to this endpoint" >&2
echo "  3. the secret was rotated and its grace window has since passed" >&2
echo "  4. the body was altered in transit by a proxy that rewrites JSON" >&2
exit 1
