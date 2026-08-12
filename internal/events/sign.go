package events

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the header carrying the signature. Named with the
// vendor prefix because a receiver behind a proxy must be able to tell
// HANGAR's signature apart from anything else on the request.
const SignatureHeader = "X-Hangar-Signature"

// The header's value is `t=<unix>,v1=<hex>`:
//
//	X-Hangar-Signature: t=1770000000,v1=6f1c...9ab
//
// v1 is HMAC-SHA256(secret, "<t>.<body>") hex-encoded lower-case.
//
// ── WHY THE TIMESTAMP IS INSIDE THE SIGNED STRING ────────────────────────
// Signing the body alone would let anyone who captured one delivery replay
// it forever, and a receiver checking a SEPARATE unsigned timestamp header
// gains nothing, because an attacker rewrites that header freely. Binding
// the two — signing `timestamp ‖ '.' ‖ body` — means changing the timestamp
// invalidates the signature, so the replay window is enforceable. This is
// the same construction Stripe and GitHub use, chosen so an integrator can
// port a verifier they have already written.
//
// The `v1=` label exists so the scheme can be replaced without breaking
// every receiver at once: a future v2 is an ADDITIONAL element in the same
// header, and a receiver checks whichever version it understands. Verify
// below ignores unknown elements for exactly that reason.
const (
	signatureVersion = "v1"
	timestampKey     = "t"
)

// DefaultReplayWindow bounds how far a delivery's timestamp may be from
// the receiver's clock. Five minutes is generous enough to survive
// ordinary NTP skew and a slow queue, tight enough that a captured
// delivery is not indefinitely replayable.
//
// It is symmetric — a timestamp in the FUTURE is rejected past the same
// bound. Accepting arbitrary future timestamps would hand an attacker who
// obtains one signed payload a delivery that stays valid for as long as
// they chose when they captured it, which is the replay the window exists
// to stop.
const DefaultReplayWindow = 5 * time.Minute

// SigningPayload is the exact byte string that gets HMAC'd. Exported
// because deploy/verify-webhook-signature.sh and every third-party
// verifier must agree with it byte for byte, and because the one place
// integrators get this wrong is guessing at the separator.
func SigningPayload(ts time.Time, body []byte) []byte {
	prefix := strconv.FormatInt(ts.Unix(), 10) + "."
	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	out = append(out, body...)
	return out
}

// Sign returns the full header value for body at time ts.
func Sign(secret, body []byte, ts time.Time) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(SigningPayload(ts, body))
	return fmt.Sprintf("%s=%d,%s=%s", timestampKey, ts.Unix(), signatureVersion, hex.EncodeToString(mac.Sum(nil)))
}

// Signature verification failures. Distinguished from each other for the
// SERVER's logs and tests only — a receiver should never report which one
// it was to the sender, since "bad signature" and "stale timestamp" are
// different amounts of information to hand an attacker.
var (
	ErrMalformedSignature = errors.New("events: malformed signature header")
	ErrStaleSignature     = errors.New("events: signature timestamp outside the replay window")
	ErrBadSignature       = errors.New("events: signature does not match")
)

// Verify checks a received header against body.
//
// CONSTANT TIME (SRS §4.9: "Signature verification must be constant-time").
// The comparison is subtle.ConstantTimeCompare over the decoded bytes, not
// == over the hex strings: a byte-wise early-exit comparison leaks, through
// timing, how many leading bytes of a guess were right, which turns forging
// a 32-byte tag from 2^256 work into 256×32 measurements. Comparing decoded
// bytes rather than hex text also means an upper-case hex signature from a
// well-meaning receiver library verifies identically instead of being a
// mystery rejection.
func Verify(secret, body []byte, header string, now time.Time, window time.Duration) error {
	if window <= 0 {
		window = DefaultReplayWindow
	}

	ts, provided, err := ParseSignatureHeader(header)
	if err != nil {
		return err
	}

	// Window check BEFORE the HMAC: a stale delivery is rejected without
	// spending the hash, and — more importantly — the answer for a stale
	// timestamp must not depend on whether the signature was also right.
	drift := now.Sub(ts)
	if drift < 0 {
		drift = -drift
	}
	if drift > window {
		return fmt.Errorf("%w: %s off (window %s)", ErrStaleSignature, drift.Truncate(time.Second), window)
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(SigningPayload(ts, body))
	if subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return ErrBadSignature
	}
	return nil
}

// ParseSignatureHeader splits `t=<unix>,v1=<hex>` into its parts.
//
// Unknown elements are IGNORED rather than rejected, so adding a future
// `v2=` alongside `v1=` does not break receivers written against v1 — see
// the note on signatureVersion. A header with no v1 element at all is
// malformed, because then there is nothing this version can check.
func ParseSignatureHeader(header string) (time.Time, []byte, error) {
	var (
		ts      time.Time
		sawTS   bool
		sig     []byte
		sawSig  bool
		invalid = func(reason string) (time.Time, []byte, error) {
			return time.Time{}, nil, fmt.Errorf("%w: %s", ErrMalformedSignature, reason)
		}
	)

	for _, element := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(element), "=")
		if !ok {
			continue
		}
		switch key {
		case timestampKey:
			seconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return invalid("timestamp is not an integer")
			}
			ts, sawTS = time.Unix(seconds, 0).UTC(), true
		case signatureVersion:
			decoded, err := hex.DecodeString(value)
			if err != nil {
				return invalid("v1 is not hex")
			}
			if len(decoded) != sha256.Size {
				return invalid(fmt.Sprintf("v1 is %d bytes, want %d", len(decoded), sha256.Size))
			}
			sig, sawSig = decoded, true
		}
	}

	switch {
	case !sawTS && !sawSig:
		return invalid("no t= or v1= element")
	case !sawTS:
		return invalid("no t= element")
	case !sawSig:
		return invalid("no v1= element")
	}
	return ts, sig, nil
}
