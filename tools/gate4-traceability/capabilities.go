package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Capability is one row of SRS Appendix A's 58-capability parity matrix.
type Capability struct {
	ID     int
	Domain string
	Name   string
}

// bandRow matches one row of Appendix A: a band range, a domain, and a
// semicolon-separated member list.
//
//	| **1–14** | **Character** | Assets (…); Blueprints; … |
//
// The dash is an EN DASH (U+2013) in the document, not a hyphen — matching a
// hyphen here silently finds nothing, which would make this parser report
// zero capabilities and the gate pass vacuously. Hence the explicit check on
// the total below.
var bandRow = regexp.MustCompile(`^\|\s*\*\*(\d+)(?:[–-](\d+))?\*\*\s*\|\s*\*\*([^|]+?)\*\*\s*\|\s*(.+?)\s*\|\s*$`)

// ParseCapabilities reads Appendix A out of the SRS.
//
// ── WHY PARSE THE SPEC RATHER THAN RESTATE IT ────────────────────────────
// A traceability matrix transcribed by hand agrees with itself by
// construction: the author reads the spec, writes the rows, and the rows
// match the spec because they came from it. Reading Appendix A directly
// means the matrix cannot drift from the specification without the parse
// failing, and it makes Gate 4.8 — "the capability matrix's band ranges
// agree with their enumerated member counts" — a thing this program CHECKS
// rather than a thing somebody confirms.
func ParseCapabilities(srsPath string) ([]Capability, error) {
	file, err := os.Open(srsPath)
	if err != nil {
		return nil, fmt.Errorf("opening the SRS: %w", err)
	}
	defer func() { _ = file.Close() }()

	var capabilities []Capability
	inAppendixA := false

	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "## Appendix A") {
			inAppendixA = true
			continue
		}
		if inAppendixA && strings.HasPrefix(line, "## Appendix B") {
			break
		}
		if !inAppendixA {
			continue
		}

		match := bandRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		first, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("parsing band start %q: %w", match[1], err)
		}
		last := first
		if match[2] != "" {
			if last, err = strconv.Atoi(match[2]); err != nil {
				return nil, fmt.Errorf("parsing band end %q: %w", match[2], err)
			}
		}
		domain := strings.TrimSpace(strings.ReplaceAll(match[3], "(cont.)", ""))

		members := splitMembers(match[4])
		// ── GATE 4.8, CHECKED HERE ───────────────────────────────────────
		// "the capability matrix's band ranges agree with their enumerated
		// member counts (the SRS's own numbering note)". A band that says
		// 1–14 and lists 13 members is a specification defect that blocks
		// the gate, so it is an error rather than a silent truncation.
		if want := last - first + 1; len(members) != want {
			return nil, fmt.Errorf(
				"GATE 4.8 FAILURE: band %d–%d (%s) declares %d capabilities but enumerates %d: %v",
				first, last, domain, want, len(members), members)
		}
		for i, name := range members {
			capabilities = append(capabilities, Capability{ID: first + i, Domain: domain, Name: name})
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("reading the SRS: %w", err)
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("appendix A yielded no capabilities — the parse is wrong, not the document")
	}
	return capabilities, nil
}

// splitMembers breaks one band's member list on semicolons, stripping the
// bold markers Appendix A uses to emphasise capabilities added late.
// Semicolons inside parentheses are NOT separators — "Contracts (headers,
// items, bids)" is one capability — but Appendix A uses commas inside its
// parentheses, so a plain split is correct and the depth counter below
// exists to keep it correct if that ever changes.
func splitMembers(row string) []string {
	var members []string
	var current strings.Builder
	depth := 0
	for _, r := range row {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ';':
			if depth == 0 {
				members = append(members, cleanMember(current.String()))
				current.Reset()
				continue
			}
		}
		current.WriteRune(r)
	}
	if name := cleanMember(current.String()); name != "" {
		members = append(members, name)
	}
	return members
}

func cleanMember(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "**", ""))
}
