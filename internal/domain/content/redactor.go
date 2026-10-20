// SPDX-License-Identifier: Apache-2.0

package content

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Rule is a custom redaction rule. Replacement may use regexp expansions such as $1.
type Rule struct {
	Name        string
	Pattern     string
	Replacement string
}

// matcher validates a regexp match at s[start:end] and may shrink it; it
// returns the final end offset and whether the match is to be replaced.
type matcher func(s string, start, end int) (int, bool)

type compiledRule struct {
	re     *regexp.Regexp
	repl   string
	accept matcher // nil: plain regexp replacement with expansion
	// need is a cheap necessary condition for a replacement in s (for example
	// "contains @" for e-mail addresses); when it is false the regexp, whose
	// unanchored scan dominates ingest CPU, is not run. nil: always run.
	need func(s string) bool
}

// Redactor applies compiled redaction rules in order. The zero value and a nil
// pointer are valid and leave input unchanged. It is safe for concurrent use.
type Redactor struct {
	rules []compiledRule
}

// NewRedactor compiles the named presets (in order) and then the custom rules.
// It reports every configuration problem at once, joined, each wrapping ErrInvalidRule.
func NewRedactor(presets []string, custom []Rule) (*Redactor, error) {
	var (
		errs  []error
		rules []compiledRule
	)
	for _, name := range presets {
		rs, ok := presetRules(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: unknown preset %q", ErrInvalidRule, name))
			continue
		}
		rules = append(rules, rs...)
	}
	for i, c := range custom {
		switch {
		case c.Name == "":
			errs = append(errs, fmt.Errorf("%w: rule %d has an empty name", ErrInvalidRule, i))
			continue
		case c.Pattern == "":
			errs = append(errs, fmt.Errorf("%w: rule %q has an empty pattern", ErrInvalidRule, c.Name))
			continue
		}
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: rule %q: %w", ErrInvalidRule, c.Name, err))
			continue
		}
		rules = append(rules, compiledRule{re: re, repl: c.Replacement})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &Redactor{rules: rules}, nil
}

// Redact returns s with every rule applied in order.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	for i := range r.rules {
		s = r.rules[i].apply(s)
	}
	return s
}

// RedactBytes is Redact for byte slices. When nothing changes it returns b itself.
func (r *Redactor) RedactBytes(b []byte) []byte {
	if r == nil || len(r.rules) == 0 || len(b) == 0 {
		return b
	}
	s := string(b)
	if out := r.Redact(s); out != s {
		return []byte(out)
	}
	return b
}

func (c *compiledRule) apply(s string) string {
	if c.need != nil && !c.need(s) {
		return s
	}
	if c.accept == nil {
		if !c.re.MatchString(s) {
			return s
		}
		return c.re.ReplaceAllString(s, c.repl)
	}
	locs := c.re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var (
		sb   strings.Builder
		last int
	)
	for _, loc := range locs {
		if loc[0] < last {
			continue
		}
		end, ok := c.accept(s, loc[0], loc[1])
		if !ok {
			continue
		}
		sb.WriteString(s[last:loc[0]])
		sb.WriteString(c.repl)
		last = end
	}
	if last == 0 && sb.Len() == 0 {
		return s
	}
	sb.WriteString(s[last:])
	return sb.String()
}
