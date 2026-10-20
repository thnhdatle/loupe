// SPDX-License-Identifier: Apache-2.0

package criteria

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the criteria file name relative to the acceptance directory.
const FileName = "criteria.yaml"

// ErrNoRoot is returned when no go.mod is found above the start directory.
var ErrNoRoot = errors.New("criteria: repository root not found")

var idPattern = regexp.MustCompile(`^[A-Z]+-[0-9]{2}$`)

// Thresholds maps a threshold name to its numeric (or boolean) limit.
type Thresholds map[string]any

// String renders the thresholds as sorted "key=value" pairs.
func (t Thresholds) String() string {
	return formatMap(t)
}

// Pass holds the Mac and CI thresholds of one criterion.
type Pass struct {
	Mac    Thresholds
	CI     Thresholds
	CISkip bool
}

// UnmarshalYAML decodes a pass block; `ci: skip` sets CISkip.
func (p *Pass) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Mac Thresholds `yaml:"mac"`
		CI  yaml.Node  `yaml:"ci"`
	}
	if err := n.Decode(&raw); err != nil {
		return fmt.Errorf("criteria: decode pass: %w", err)
	}
	p.Mac = raw.Mac
	if raw.CI.Kind == yaml.ScalarNode {
		if raw.CI.Value != "skip" {
			return fmt.Errorf("criteria: ci must be a mapping or skip, got %q", raw.CI.Value)
		}
		p.CISkip = true
		return nil
	}
	if raw.CI.Kind == 0 {
		return nil
	}
	if err := raw.CI.Decode(&p.CI); err != nil {
		return fmt.Errorf("criteria: decode pass.ci: %w", err)
	}
	return nil
}

// KindPerformance marks criteria whose thresholds are calibrated for the
// reference machine; the other kinds are functional and release.
const KindPerformance = "performance"

// Criterion is one acceptance criterion.
type Criterion struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Test     string   `yaml:"test"`
	Kind     string   `yaml:"kind"`
	RealDeps []string `yaml:"real_deps"`
	Requires []string `yaml:"requires"`
	Pass     Pass     `yaml:"pass"`
}

// File is the parsed criteria.yaml.
type File struct {
	Project  string      `yaml:"project"`
	Machine  string      `yaml:"machine"`
	Criteria []Criterion `yaml:"criteria"`
}

// IsBenchmark reports whether the criterion test is a Go benchmark.
func (c Criterion) IsBenchmark() bool {
	return strings.HasPrefix(c.Test, "Benchmark")
}

// Select returns the thresholds for the environment; ok is false when the
// criterion is marked `ci: skip` and ci is true.
func (c Criterion) Select(ci bool) (Thresholds, bool) {
	if !ci {
		return c.Pass.Mac, true
	}
	if c.Pass.CISkip {
		return nil, false
	}
	return c.Pass.CI, true
}

// AllRequires returns Requires plus the implicit `mac` of every performance
// criterion: its thresholds are calibrated for the reference machine.
func (c Criterion) AllRequires() []string {
	out := append([]string(nil), c.Requires...)
	if c.Kind == KindPerformance && !contains(out, "mac") {
		out = append(out, "mac")
	}
	return out
}

// Find returns the criterion with the given id.
func (f *File) Find(id string) (Criterion, bool) {
	for _, c := range f.Criteria {
		if c.ID == id {
			return c, true
		}
	}
	return Criterion{}, false
}

// Validate checks structural invariants of the file.
func (f *File) Validate() error {
	if f.Project == "" || len(f.Criteria) == 0 {
		return errors.New("criteria: project and criteria are required")
	}
	seen := map[string]bool{}
	for _, c := range f.Criteria {
		if !idPattern.MatchString(c.ID) {
			return fmt.Errorf("criteria: bad id %q", c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("criteria: duplicate id %s", c.ID)
		}
		seen[c.ID] = true
		if c.Test == "" || c.Title == "" || c.Kind == "" {
			return fmt.Errorf("criteria: %s needs title, test and kind", c.ID)
		}
	}
	return nil
}

// Parse decodes and validates criteria.yaml content.
func Parse(data []byte) (*File, error) {
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("criteria: parse: %w", err)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Load reads and parses the criteria file at path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the repository root.
	if err != nil {
		return nil, fmt.Errorf("criteria: read: %w", err)
	}
	return Parse(data)
}

// FindRoot walks up from start until a directory containing go.mod is found.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("criteria: abs path: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoRoot
		}
		dir = parent
	}
}

// IsCI interprets the value of the CI environment variable.
func IsCI(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// FormatMap renders any string-keyed map as sorted "key=value" pairs.
func FormatMap(m map[string]any) string {
	return formatMap(m)
}

func formatMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
