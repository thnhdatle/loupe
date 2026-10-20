// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

// Options configures one runner invocation.
type Options struct {
	// Root is the repository root; commands run there.
	Root string
	// NoBuild skips the fresh build and `make docker`.
	NoBuild bool
	// NoCompose skips the compose bring-up.
	NoCompose bool
	// CheckOnly runs only the criteria and source checks.
	CheckOnly bool
	// CI selects pass.ci thresholds and CI-mode semantics.
	CI bool
	// Only restricts the run to the listed criterion ids.
	Only []string
	// Version is the release shown in the report heading.
	Version string
}

// ParseArgs parses flags and KEY=VALUE arguments (ONLY=, CI=) with the
// environment lookup getenv providing defaults.
func ParseArgs(args []string, getenv func(string) string, errOut io.Writer) (Options, error) {
	fs := flag.NewFlagSet("runner", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o Options
	fs.BoolVar(&o.NoBuild, "no-build", false, "skip the fresh build and make docker")
	fs.BoolVar(&o.NoCompose, "no-compose", false, "skip docker compose bring-up")
	fs.BoolVar(&o.CheckOnly, "check-only", false, "only check criteria.yaml against the tests and sources")
	fs.StringVar(&o.Version, "version", "dev", "release shown in the report heading")
	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("runner: %w", err)
	}
	only := getenv("ONLY")
	ci := getenv("CI")
	for _, a := range fs.Args() {
		k, v, ok := strings.Cut(a, "=")
		switch {
		case ok && k == "ONLY":
			only = v
		case ok && k == "CI":
			ci = v
		default:
			return Options{}, fmt.Errorf("runner: unknown argument %q", a)
		}
	}
	o.CI = criteria.IsCI(ci)
	o.Only = splitIDs(only)
	return o, nil
}

func splitIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
