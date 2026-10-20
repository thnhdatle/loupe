// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"fmt"
	"sort"
	"strings"
)

// Report is the data behind acceptance/report.md.
type Report struct {
	Project  string
	Version  string
	Date     string
	CI       bool
	Results  []Result
	Blocked  []string
	Problems []string
	Siblings []Sibling
}

// HasFailure reports whether the run must exit non-zero.
func (rp *Report) HasFailure() bool {
	if len(rp.Problems) > 0 {
		return true
	}
	for _, r := range rp.Results {
		if r.Outcome == OutcomeFail {
			return true
		}
	}
	return false
}

// Markdown renders the report in the format described in acceptance/README.md.
func (rp *Report) Markdown() string {
	var b strings.Builder
	env := "mac"
	if rp.CI {
		env = "ci"
	}
	fmt.Fprintf(&b, "# Acceptance report — %s %s — %s\n", rp.Project, rp.Version, rp.Date)
	fmt.Fprintf(&b, "Environment: %s\n\n", env)
	b.WriteString("| ID | Result | Measured | Threshold |\n|---|---|---|---|\n")
	for _, r := range rp.Results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.Criterion.ID, r.Outcome,
			cell(r.Measured), cell(r.Threshold))
	}
	section(&b, "Not met", rp.notMet())
	section(&b, "Skipped (environment)", rp.lines(OutcomeSkipped, "probe"))
	section(&b, "Blocked (environment)", append(rp.lines(OutcomeBlocked, "command"), rp.Blocked...))
	section(&b, "Sibling images used", rp.siblingLines())
	return b.String()
}

func cell(s string) string {
	if s == "" {
		return "-"
	}
	return strings.ReplaceAll(s, "|", "/")
}

func section(b *strings.Builder, title string, lines []string) {
	fmt.Fprintf(b, "\n## %s\n", title)
	if len(lines) == 0 {
		b.WriteString("- none\n")
		return
	}
	for _, l := range lines {
		fmt.Fprintf(b, "- %s\n", l)
	}
}

func (rp *Report) notMet() []string {
	var out []string
	for _, r := range rp.Results {
		if r.Outcome == OutcomeFail {
			out = append(out, fmt.Sprintf("%s: %s; attempts: 1", r.Criterion.ID, oneLine(r.Detail)))
		}
	}
	for _, p := range rp.Problems {
		out = append(out, "check: "+p)
	}
	return out
}

func (rp *Report) lines(o Outcome, label string) []string {
	var out []string
	for _, r := range rp.Results {
		if r.Outcome == o {
			out = append(out, fmt.Sprintf("%s: requires %s — %s: %s",
				r.Criterion.ID, r.Requirement, label, oneLine(r.Detail)))
		}
	}
	return out
}

func (rp *Report) siblingLines() []string {
	sib := append([]Sibling(nil), rp.Siblings...)
	sort.Slice(sib, func(i, j int) bool { return sib[i].Name < sib[j].Name })
	var out []string
	for _, s := range sib {
		switch s.Source {
		case "released":
			out = append(out, fmt.Sprintf("%s: %s (released)", s.Name, s.Image))
		default:
			out = append(out, fmt.Sprintf("%s: %s @ %s (%s)", s.Name, s.Image, shortID(s.ID), s.Source))
		}
	}
	return out
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
