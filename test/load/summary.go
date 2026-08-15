package load

// summary.go writes the SUMMARY.md every gate evidence directory carries.
//
// One writer, shared by every runner, because 04_RELEASE_GATES.md §0 rule 1
// makes the artefact the gate ("we ran it and it looked fine is a fail") and
// a reviewer comparing seven gates should not also have to compare seven
// report formats. The verdict line is mechanical: it is PASS only when every
// condition passed, and each condition prints the measurement that decided
// it beside its own verdict, so a FAIL says which condition and with what
// number rather than only that something went wrong.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Summary is one gate's report.
type Summary struct {
	// Gate is the number, e.g. "1". Name is §N's title.
	Gate string
	Name string
	// Version is the release the evidence belongs to, e.g. "v1.0.0-rc1".
	Version string
	// Ran is the wall-clock window the gate covered.
	StartedAt  time.Time
	FinishedAt time.Time
	// Headline is the one-sentence statement of what was measured, in the
	// units the gate is specified in.
	Headline string
	// Conditions are the numbered pass conditions of the gate's own section.
	Conditions []ConditionResult
	// Environment is recorded per §0 rule 3 — replica count, host, Postgres
	// version, anything a reader needs to know the run was real.
	Environment map[string]string
	// Artefacts are the files beside the summary, with what each one is.
	Artefacts map[string]string
	// Notes is free prose appended verbatim: caveats, deviations, and
	// anything the numbers do not say on their own.
	Notes string
}

// Passed reports the mechanical verdict: every condition passed, and there
// was at least one condition. A gate with no conditions evaluated has not
// passed — it has not run.
func (s Summary) Passed() bool {
	if len(s.Conditions) == 0 {
		return false
	}
	for _, c := range s.Conditions {
		if !c.Passed {
			return false
		}
	}
	return true
}

// Verdict is "PASS" or "FAIL".
func (s Summary) Verdict() string {
	if s.Passed() {
		return "PASS"
	}
	return "FAIL"
}

// WriteSummary renders the report to <dir>/SUMMARY.md.
func WriteSummary(dir string, s Summary) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("load: creating summary directory: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Gate %s — %s\n\n", s.Gate, s.Name)
	fmt.Fprintf(&b, "**Verdict: %s**\n\n", s.Verdict())
	if s.Headline != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Headline)
	}

	fmt.Fprintf(&b, "| | |\n| :-- | :-- |\n")
	fmt.Fprintf(&b, "| Release | `%s` |\n", s.Version)
	fmt.Fprintf(&b, "| Started | %s |\n", s.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| Finished | %s |\n", s.FinishedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| Duration | %s |\n", s.FinishedAt.Sub(s.StartedAt).Round(time.Second))
	for _, k := range sortedKeys(s.Environment) {
		fmt.Fprintf(&b, "| %s | %s |\n", k, s.Environment[k])
	}

	b.WriteString("\n## Pass conditions\n\n")
	if len(s.Conditions) == 0 {
		b.WriteString("**No condition was evaluated.** A gate that measured nothing has not passed;\n" +
			"it has not run. See 04_RELEASE_GATES.md §0 rule 1.\n")
	} else {
		b.WriteString("| # | Condition | Verdict | Measurement |\n| :-- | :-- | :-- | :-- |\n")
		for _, c := range s.Conditions {
			verdict := "FAIL"
			if c.Passed {
				verdict = "pass"
			}
			fmt.Fprintf(&b, "| %s | %s | **%s** | %s |\n",
				c.ID, escapePipes(c.Description), verdict, escapePipes(c.Measurement))
		}
	}

	if len(s.Artefacts) > 0 {
		b.WriteString("\n## Artefacts\n\n")
		b.WriteString("| File | Contents |\n| :-- | :-- |\n")
		for _, k := range sortedKeys(s.Artefacts) {
			fmt.Fprintf(&b, "| `%s` | %s |\n", k, escapePipes(s.Artefacts[k]))
		}
	}

	if s.Notes != "" {
		b.WriteString("\n## Notes\n\n")
		b.WriteString(strings.TrimSpace(s.Notes))
		b.WriteString("\n")
	}

	path := filepath.Join(dir, "SUMMARY.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("load: writing %s: %w", path, err)
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// escapePipes keeps a measurement containing a pipe from breaking the
// markdown table it is printed in — divergence measurements quote
// |local − server| often enough for this to matter.
func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
