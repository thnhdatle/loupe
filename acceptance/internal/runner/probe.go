// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Status is the outcome of probing one requirement.
type Status string

// Probe statuses.
const (
	StatusOK      Status = "ok"
	StatusSkipped Status = "skipped"
	StatusBlocked Status = "blocked"
)

// ProbeResult is the answer of one requirement probe.
type ProbeResult struct {
	Status Status
	// Detail is the probe result or the failing command and its error.
	Detail string
}

// Sibling records the image chosen for a sibling project.
type Sibling struct {
	Name   string
	Image  string
	Source string
	ID     string
}

// Prober checks the `requires` entries of criteria.
type Prober struct {
	Exec    Executor
	Root    string
	CI      bool
	Getenv  func(string) string
	Sibling map[string]Sibling
}

func ok(detail string) ProbeResult      { return ProbeResult{StatusOK, detail} }
func skipped(detail string) ProbeResult { return ProbeResult{StatusSkipped, detail} }
func blocked(detail string) ProbeResult { return ProbeResult{StatusBlocked, detail} }

// Check probes one requirement.
func (p *Prober) Check(ctx context.Context, req string) ProbeResult {
	switch {
	case req == "python":
		return p.tool(ctx, Command{Dir: p.Root, Name: "python3.12", Args: []string{argVersion}})
	case req == "node":
		return p.tool(ctx, Command{Dir: p.Root, Name: "node", Args: []string{argVersion}})
	case req == "playwright":
		return p.tool(ctx, Command{Dir: p.playwrightDir(), Name: "npx", Args: []string{"playwright", argVersion}})
	case req == binDocker:
		return p.tool(ctx, Command{Dir: p.Root, Name: binDocker, Args: []string{"info"}})
	case req == "mac":
		return p.mac(ctx)
	case strings.HasPrefix(req, "sibling:"):
		return p.sibling(ctx, strings.TrimPrefix(req, "sibling:"))
	default:
		return blocked("unknown requirement " + req)
	}
}

func (p *Prober) playwrightDir() string {
	dir := filepath.Join(p.Root, "tests", "node")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return p.Root
}

func (p *Prober) tool(ctx context.Context, c Command) ProbeResult {
	out, err := p.Exec.Run(ctx, c)
	if err != nil {
		return blocked(fmt.Sprintf("%s: %v: %s", c, err, firstLine(out)))
	}
	return ok(c.String() + ": " + firstLine(out))
}

func (p *Prober) mac(ctx context.Context) ProbeResult {
	if p.CI {
		return ok("CI: pass.ci thresholds apply")
	}
	c := Command{Dir: p.Root, Name: "uname", Args: []string{"-sm"}}
	out, err := p.Exec.Run(ctx, c)
	if err != nil {
		return blocked(fmt.Sprintf("%s: %v", c, err))
	}
	if got := strings.TrimSpace(out); got != "Darwin arm64" {
		return blocked(fmt.Sprintf("%s: got %q, want %q", c, got, "Darwin arm64"))
	}
	return ok("Darwin arm64")
}

// siblingCandidates lists image candidates in order: the RAYLD_<NAME>_IMAGE
// override, the released image, then the local development tag.
func (p *Prober) siblingCandidates(name string) []Sibling {
	var out []Sibling
	env := "RAYLD_" + strings.ToUpper(name) + "_IMAGE"
	if v := p.Getenv(env); v != "" {
		out = append(out, Sibling{Name: name, Image: v, Source: "from " + env})
	}
	return append(out,
		Sibling{Name: name, Image: "ghcr.io/thnhdatle/" + name + ":v0.1.0", Source: "released"},
		Sibling{Name: name, Image: "thnhdatle/" + name + ":dev", Source: "local fallback"},
	)
}

func (p *Prober) sibling(ctx context.Context, name string) ProbeResult {
	if s, found := p.Sibling[name]; found {
		return ok(s.Image)
	}
	var tried []string
	for _, s := range p.siblingCandidates(name) {
		c := Command{Dir: p.Root, Name: binDocker, Args: []string{"image", "inspect", "--format", "{{.Id}}", s.Image}}
		out, err := p.Exec.Run(ctx, c)
		if err != nil {
			tried = append(tried, s.Image+": "+firstLine(out))
			continue
		}
		s.ID = strings.TrimSpace(out)
		p.Sibling[name] = s
		return ok(s.Image)
	}
	return skipped("no image for sibling " + name + " (" + strings.Join(tried, "; ") + ")")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return line
}
