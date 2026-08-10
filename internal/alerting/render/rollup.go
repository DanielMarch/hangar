package render

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Rollup renders a coalesced group as one message body: a header line,
// then one line per event, truncated to fit limit with an explicit
// remainder count.
//
// §4.4: "A coalesced roll-up that exceeds a channel's payload limit is
// truncated with an explicit remainder count, never dropped." Both halves
// of that matter. Truncating silently would let an operator believe they
// had seen all forty structures; failing the delivery would turn a
// cosmetic problem (a message too long for Slack) into a lost alert, which
// is the one outcome §4.4's delivery guarantees rule out.
//
// limit is measured in RUNES, not bytes: Slack's 3,000 and Discord's 2,000
// are character limits, and a body of 2,000 multi-byte characters is
// within Discord's limit however many bytes it occupies. Counting bytes
// would truncate perfectly legal messages (EVE structure names are
// routinely non-ASCII).
//
// A limit too small to hold even the header yields the header, truncated —
// never an empty body, which a channel would reject as a malformed
// payload.
func Rollup(header string, lines []string, limit int) string {
	if limit <= 0 {
		limit = defaultRollupLimit
	}

	var b strings.Builder
	b.WriteString(header)
	used := utf8.RuneCountInString(header)

	for i, line := range lines {
		remaining := len(lines) - i
		candidate := "\n" + line
		// Reserve room for the remainder notice that would be needed if
		// this line is the one that does not fit — otherwise appending it
		// could push the total over the limit after the fact.
		suffix := remainderNotice(remaining)
		if used+utf8.RuneCountInString(candidate)+utf8.RuneCountInString(suffix) > limit {
			b.WriteString(remainderNotice(remaining))
			return b.String()
		}
		b.WriteString(candidate)
		used += utf8.RuneCountInString(candidate)
	}

	out := b.String()
	if utf8.RuneCountInString(out) > limit {
		// Only reachable when the header alone exceeds the limit.
		return truncateRunes(out, limit)
	}
	return out
}

// defaultRollupLimit is used when a channel declares no limit of its own —
// generous enough that a real roll-up is never truncated, bounded enough
// that a pathological payload cannot produce an unbounded message.
const defaultRollupLimit = 64 * 1024

// remainderNotice is §4.4's "explicit remainder count". Phrased as a count
// of what is MISSING, so the number an operator reads is the number of
// things they have not seen.
func remainderNotice(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "\n… and 1 more"
	}
	return fmt.Sprintf("\n… and %d more", n)
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}

// Header renders a roll-up's first line: the count and the alert's
// human-readable summary. A single-event group reads naturally ("Structure
// under attack") rather than as a degenerate roll-up ("1 × ...").
func Header(summary string, count int) string {
	if count <= 1 {
		return summary
	}
	return fmt.Sprintf("%s (%d events)", summary, count)
}
