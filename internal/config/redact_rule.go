// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RedactRule is one content.redact entry: a preset name ("email") or a custom
// rule {name, pattern, replacement}.
type RedactRule struct {
	Preset      string
	Name        string
	Pattern     string
	Replacement string
}

type redactRuleYAML struct {
	Name        string `yaml:"name"`
	Pattern     string `yaml:"pattern"`
	Replacement string `yaml:"replacement"`
}

// UnmarshalYAML implements yaml.Unmarshaler; unknown keys in a custom rule are errors.
func (r *RedactRule) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind { //nolint:exhaustive // other node kinds fall to the default error.
	case yaml.ScalarNode:
		*r = RedactRule{Preset: n.Value}
		return nil
	case yaml.MappingNode:
		var raw redactRuleYAML
		for i := 0; i+1 < len(n.Content); i += 2 {
			switch n.Content[i].Value {
			case "name", "pattern", "replacement":
			default:
				return fmt.Errorf("line %d: unknown key %q in content.redact rule", n.Content[i].Line, n.Content[i].Value)
			}
		}
		if err := n.Decode(&raw); err != nil {
			return fmt.Errorf("content.redact rule: %w", err)
		}
		*r = RedactRule{Name: raw.Name, Pattern: raw.Pattern, Replacement: raw.Replacement}
		return nil
	default:
		return fmt.Errorf("line %d: content.redact entries must be a preset name or a rule mapping", n.Line)
	}
}

// MarshalYAML implements yaml.Marshaler.
func (r RedactRule) MarshalYAML() (any, error) {
	if r.Preset != "" {
		return r.Preset, nil
	}
	return redactRuleYAML{Name: r.Name, Pattern: r.Pattern, Replacement: r.Replacement}, nil
}
