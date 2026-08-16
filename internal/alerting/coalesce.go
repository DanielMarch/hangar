package alerting

import (
	"strings"
	"time"
)

// DefaultCoalesceWindow is how long events sharing a coalescing key are
// gathered before one message goes out. §4.4 fixes the KEY ("(routing
// target, alert type)") and the behaviour ("40 events inside the window
// render as one message") but not the duration; five minutes is short
// enough that a structure under attack is still actionable and long enough
// that a fleet rolling through a constellation produces one message rather
// than forty.
const DefaultCoalesceWindow = 5 * time.Minute

// CoalesceKey is §4.4's coalescing key — (routing target, alert type) —
// plus the window bucket that turns "within the window" into an exact,
// stateless string comparison.
//
// ── FIXED WINDOW, DELIBERATELY (not sliding) ────────────────────────────
// Bucket is occurred_at truncated to the window, so every event in the
// same wall-clock window shares one key and therefore one deadline. The
// alternative — a sliding window anchored on each group's oldest unsent
// event — needs the dispatcher to look up that oldest event before it can
// decide whether the group is due, and gives every event a different
// deadline, which fragments a burst across several roll-ups exactly when
// coalescing matters most. The cost of the fixed window is the boundary
// case: 40 events straddling a bucket edge produce two messages instead of
// one. That is a visible, bounded, easily explained outcome; a fragmented
// burst is neither.
//
// This is the same fixed-window-versus-rolling choice Phase 12 hit with
// Discord's invalid-request budget, and it is called out here for the same
// reason: the two behave identically until they don't, and which one is in
// use must never be a matter of inference.
type CoalesceKey struct {
	Target    Target
	AlertType string
	Bucket    time.Time
}

// NewCoalesceKey builds the key for an event of alertType occurring at
// occurredAt and routed to target. A window of zero disables coalescing
// (String returns "", which the caller stores as a NULL coalesce_key —
// every such event then delivers on its own).
func NewCoalesceKey(target Target, alertType string, occurredAt time.Time, window time.Duration) CoalesceKey {
	if window <= 0 {
		return CoalesceKey{Target: target, AlertType: alertType}
	}
	// UTC before truncating: time.Truncate works on the absolute time, but
	// normalising the zone keeps String()'s output identical regardless of
	// the server's local zone, which matters because the result is stored
	// and compared as text.
	return CoalesceKey{
		Target:    target,
		AlertType: alertType,
		Bucket:    occurredAt.UTC().Truncate(window),
	}
}

// String renders the key as it is stored in app.alert_event.coalesce_key:
// four "|"-separated components — target kind, target ref, alert type,
// window bucket. The bucket component is EMPTY when coalescing is
// disabled; the first three are always present, because they are the
// event's routing identity and the dispatcher reads them back (via
// ParseCoalesceKey) to find the destination's mention. A disabled window
// therefore still records "who this was for", it just does not group.
//
// Escaping matters more than it looks: target refs are external ids and
// mention strings are an open vocabulary, so a component containing "|"
// must not be able to forge membership of another group. Backslash is
// escaped FIRST and pipe second, and unescaping walks the string once
// left to right — the ordering bug that lets `\|` and `\\|` collide is
// exactly the kind of escape-sequence ambiguity Phase 13 caught in the
// TeamSpeak driver, so TestCoalesceKeyRoundTripsHostileRefs pins it here.
func (k CoalesceKey) String() string {
	bucket := ""
	if !k.Bucket.IsZero() {
		bucket = k.Bucket.UTC().Format(time.RFC3339)
	}
	return strings.Join([]string{
		escapePipe(k.Target.Kind),
		escapePipe(k.Target.Ref),
		escapePipe(k.AlertType),
		bucket,
	}, "|")
}

// Coalesces reports whether this key groups events at all — false when
// the window was zero or negative, in which case each event delivers on
// its own.
func (k CoalesceKey) Coalesces() bool { return !k.Bucket.IsZero() }

// Due reports when the key's window closes — the moment its deliveries
// become eligible to be claimed and rolled up.
func (k CoalesceKey) Due(window time.Duration) time.Time {
	if k.Bucket.IsZero() || window <= 0 {
		return time.Time{}
	}
	return k.Bucket.Add(window)
}

// ── PHASE 22, DEFECT B-9: THE WARNING ARRIVED AT THE DEADLINE ────────────
//
// Two of §4.4's four thresholds stamp OccurredAt as the DEADLINE they are
// warning about: Evaluator.structureFuel uses the fuel expiry and
// expiringContracts uses date_expired. That choice is right, and its
// comment says why — a burst of structures expiring together rolls up into
// one message even though the pass that found them ran at an arbitrary
// moment. What was wrong is that the same timestamp then became the
// DELIVERY's due time, because Emit wrote Due(window) straight into
// app.alert_delivery.next_attempt_at.
//
// So a structure whose fuel runs out in seventeen hours produced a
// delivery the dispatcher could not claim for seventeen hours: an alert
// whose entire purpose is advance warning, scheduled to arrive at the
// moment it stops being actionable. Measured on a Gate 3 run at
// v1.0.0-rc1: 336 deliveries pending with attempts = 0 and
// next_attempt_at as far out as the following evening.
//
// DueBy separates the two uses. The bucket stays exactly as it was — the
// grouping is unchanged, and events that share a deadline still share a
// message — but the delivery becomes claimable at most one window from
// now. A bucket in the past (corporation.member.inactive, which passes
// `now` explicitly, and every notification-driven event) is unaffected:
// its bucket+window is already at or before now+window, so the cap never
// binds and the coalescing wait is exactly what it always was.
func (k CoalesceKey) DueBy(now time.Time, window time.Duration) time.Time {
	due := k.Due(window)
	if due.IsZero() {
		return time.Time{}
	}
	if latest := now.Add(window); due.After(latest) {
		return latest
	}
	return due
}

// ParseCoalesceKey reads back what String wrote. ok is false for anything
// that is not a well-formed four-component key — including the empty
// string, and including a key written by some future version with more
// components, neither of which is an error the dispatcher should fail on
// (it falls back to per-event delivery with no mention).
func ParseCoalesceKey(s string) (CoalesceKey, bool) {
	if s == "" {
		return CoalesceKey{}, false
	}
	parts := splitEscaped(s)
	if len(parts) != 4 {
		return CoalesceKey{}, false
	}
	key := CoalesceKey{
		Target:    Target{Kind: parts[0], Ref: parts[1]},
		AlertType: parts[2],
	}
	if parts[3] != "" {
		bucket, err := time.Parse(time.RFC3339, parts[3])
		if err != nil {
			return CoalesceKey{}, false
		}
		key.Bucket = bucket.UTC()
	}
	return key, true
}

func escapePipe(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "|", `\|`)
}

// splitEscaped splits on unescaped "|" and unescapes each component in a
// single left-to-right pass, so `\\` is consumed as one literal backslash
// before the next character is examined — the property that keeps
// `a\\|b` (backslash, separator) distinct from `a\|b` (literal pipe).
func splitEscaped(s string) []string {
	var parts []string
	var current strings.Builder
	escaped := false

	for _, r := range s {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		// Trailing lone backslash: malformed, but rendering it literally
		// is friendlier than dropping a character silently.
		current.WriteRune('\\')
	}
	parts = append(parts, current.String())
	return parts
}
