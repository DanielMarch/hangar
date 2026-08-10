package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Template renders one alert payload into a single human-readable line.
// ok=false means "this template cannot render this payload" — a missing or
// unexpected field, not an error — and the chain moves on to the next
// candidate. A Template must never panic and must never return an error:
// Principle 14's operational form is that a payload HANGAR does not
// understand still gets delivered, so there is no failure to report.
type Template func(payload map[string]any) (line string, ok bool)

// templates is the per-alert-type registry — the first link in the chain
// Render walks. It is deliberately small: a template exists only where the
// payload's fields are known well enough to say something clearer than the
// generic key/value listing already does, and every one of them falls
// through rather than guessing when a field it wants is absent.
//
// Adding a template is always optional. The generic renderer (generic.go,
// built during Phase 9 and already exercised by
// internal/sync/handlers/phase9_exit_integration_test.go) is the chain's
// last resort and handles every type, known or not, forever.
var templates = map[string]Template{
	"StructureUnderAttack": func(p map[string]any) (string, bool) {
		name := firstString(p, "structureShowInfoData", "structureName", "structureTypeName")
		system := firstString(p, "solarsystemID", "solarSystemID")
		attacker := firstString(p, "corpName", "allianceName", "charID")
		if name == "" && system == "" {
			return "", false
		}
		return joinNonEmpty(" — ",
			labelled("structure", name),
			labelled("system", system),
			labelled("attacker", attacker),
		), true
	},
	"StructureFuelAlert": func(p map[string]any) (string, bool) {
		name := firstString(p, "structureShowInfoData", "structureName")
		listed := firstString(p, "listOfTypesAndQty")
		if name == "" {
			return "", false
		}
		return joinNonEmpty(" — ", labelled("structure", name), labelled("fuel", listed)), true
	},
	"corporation.starbase.fuel_low": func(p map[string]any) (string, bool) {
		name := firstString(p, "starbase_name", "starbase_id")
		hours := firstString(p, "hours_remaining")
		if name == "" {
			return "", false
		}
		return joinNonEmpty(" — ", labelled("starbase", name), labelled("hours left", hours)), true
	},
	"corporation.structure.fuel_low": func(p map[string]any) (string, bool) {
		name := firstString(p, "structure_name", "structure_id")
		expires := firstString(p, "fuel_expires")
		if name == "" {
			return "", false
		}
		return joinNonEmpty(" — ", labelled("structure", name), labelled("fuel expires", expires)), true
	},
}

// Line renders one event as a single line, walking the chain:
//
//  1. the per-type template, if one is registered and it can render this
//     payload;
//  2. the generic key/value fallback (generic.go), flattened onto one line.
//
// It is what a coalesced roll-up lists one of per event. Render (below) is
// the multi-line form for a message that carries exactly one event.
func Line(alertType string, payload json.RawMessage) string {
	if tmpl, ok := templates[alertType]; ok {
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err == nil && decoded != nil {
			if line, ok := tmpl(decoded); ok {
				return line
			}
		}
	}
	// Fallback: the generic renderer's multi-line listing, folded onto one
	// line so it can sit in a roll-up list. The full listing is still what
	// Render returns for a single-event message.
	flattened := strings.ReplaceAll(strings.TrimSpace(Generic(payload)), "\n", "; ")
	return strings.Join(strings.Fields(flattened), " ")
}

// Render produces the full body for a message carrying a single event: the
// per-type line when a template matched, otherwise the generic key/value
// listing in its original multi-line form (which is far more readable than
// the folded one-liner and is what the operator wants when there is only
// one thing to read).
//
// This is the wiring §4.4 asks for — "unrecognised payloads render as
// generic key/value pairs" — with generic.go as the last resort of the
// template chain, exactly as Phase 9 anticipated. generic.go itself is
// untouched by this phase.
func Render(alertType string, payload json.RawMessage) string {
	if tmpl, ok := templates[alertType]; ok {
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err == nil && decoded != nil {
			if line, ok := tmpl(decoded); ok {
				return line
			}
		}
	}
	return Generic(payload)
}

// HasTemplate reports whether a per-type template is registered. Used by
// tests to prove the fallback path is the one being exercised, rather than
// a template silently doing the work.
func HasTemplate(alertType string) bool {
	_, ok := templates[alertType]
	return ok
}

// firstString returns the first of keys present in p, rendered as a
// string. CCP notification payloads are YAML-derived, so a value may be a
// string, a number, or a nested list/map depending on the type — all are
// rendered rather than rejected.
func firstString(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k]; ok {
			if s := compactValue(v); s != "" && s != "(null)" {
				return s
			}
		}
	}
	return ""
}

// compactValue renders a decoded JSON value inline, recursing into lists
// and maps so every LEAF goes through scalarString.
//
// The recursion is not decoration. encoding/json decodes every number as
// float64, and fmt's %v prints a large float64 in scientific notation:
// `structureShowInfoData: [showinfo 35832 1021975179626]` would render as
// `1.021975179626e+12`, turning a structure id an operator could paste
// into the game client into something they cannot. Every EVE structure id
// is around 1e12, so this is the normal case, not an edge one — it showed
// up the first time a real fixture went through a template.
//
// generic.go does not have this problem (its renderValue already recurses
// to scalars) and is untouched; the bug was in reaching for scalarString
// directly on a container.
func compactValue(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, compactValue(item))
		}
		return "[" + strings.Join(parts, " ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+compactValue(t[k]))
		}
		return "{" + strings.Join(parts, " ") + "}"
	default:
		return scalarString(v)
	}
}

func labelled(label, value string) string {
	if value == "" {
		return ""
	}
	return fmt.Sprintf("%s %s", label, value)
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
