// Package teamspeak is HANGAR's TeamSpeak 3 provisioning driver
// (01_ARCHITECTURE.md §9.4, Phase 13): TS3 WebQuery over HTTP with an
// x-api-key header, mapping client_unique_identifier via single-use
// challenge tokens.
package teamspeak

import "strings"

// escapeMap is TS3's ServerQuery escape table (used verbatim by
// WebQuery's underlying command values, per §9.4: "TS3's query
// escaping... applies to values even over WebQuery"). This is TS3's own
// protocol-level escaping, unrelated to and applied BEFORE any HTTP/URL
// encoding the transport layer does.
var escapeMap = map[rune]string{
	'\\': `\\`,
	'/':  `\/`,
	' ':  `\s`,
	'|':  `\p`,
	'\a': `\a`,
	'\b': `\b`,
	'\f': `\f`,
	'\n': `\n`,
	'\r': `\r`,
	'\t': `\t`,
	'\v': `\v`,
}

// unescapeMap is escapeMap inverted, keyed by the character following a
// backslash in wire format.
var unescapeMap = map[rune]rune{
	'\\': '\\',
	'/':  '/',
	's':  ' ',
	'p':  '|',
	'a':  '\a',
	'b':  '\b',
	'f':  '\f',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'v':  '\v',
}

// Escape converts a raw value into TS3 query wire format: a single
// left-to-right pass, each rune substituted independently — deliberately
// NOT a sequence of strings.ReplaceAll calls, which would be ambiguous
// here (e.g. running the space rule after the backslash rule would match
// the backslash the backslash rule just inserted, corrupting the
// output). One pass over the INPUT, one substitution decision per input
// rune, is inherently unambiguous.
func Escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if esc, ok := escapeMap[r]; ok {
			b.WriteString(esc)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Unescape reverses Escape: a single left-to-right pass over the WIRE
// string, consuming one or two input runes per output rune. Symmetrical
// reasoning to Escape — scanning the escaped output for escape sequences
// via repeated ReplaceAll would be ambiguous for the same reason (a
// decoded space produces a literal space that could coincidentally sit
// next to characters from an adjacent, distinct escape sequence).
// An unrecognised escape letter after a backslash (never expected from a
// real TS3 response, but WebQuery output is still external input) passes
// both characters through literally rather than dropping the backslash
// silently.
func Unescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && i+1 < len(runes) {
			if decoded, ok := unescapeMap[runes[i+1]]; ok {
				b.WriteRune(decoded)
				i++
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}
