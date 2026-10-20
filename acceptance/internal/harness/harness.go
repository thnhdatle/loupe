// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

// NotImplementedMessage is the failure message of a criterion test without
// a scenario.
const NotImplementedMessage = "not implemented"

// LogDir is the log directory relative to the repository root.
const LogDir = "acceptance/logs"

// Log is the content of acceptance/logs/<ID>.json.
type Log struct {
	ID       string         `json:"id"`
	Measured map[string]any `json:"measured"`
	Pass     bool           `json:"pass"`
	Details  string         `json:"details"`
}

// Run is one acceptance scenario bound to its criterion.
type Run struct {
	// ID is the criterion id, for example LPE-01.
	ID string
	// CI reports whether CI thresholds are in effect.
	CI bool
	// Thresholds are the pass.mac or pass.ci values of the criterion.
	Thresholds criteria.Thresholds
	// Criterion is the full criteria.yaml entry.
	Criterion criteria.Criterion

	tb   testing.TB
	root string
}

// Start loads criteria.yaml, selects the thresholds for the environment and
// returns the run. It fails the test when the criterion cannot be loaded.
func Start(tb testing.TB, id string) *Run {
	tb.Helper()
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatalf("harness: getwd: %v", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		tb.Fatalf("harness: %v", err)
	}
	f, err := criteria.Load(filepath.Join(root, "acceptance", criteria.FileName))
	if err != nil {
		tb.Fatalf("harness: %v", err)
	}
	c, ok := f.Find(id)
	if !ok {
		tb.Fatalf("harness: criterion %s not in criteria.yaml", id)
	}
	ci := criteria.IsCI(os.Getenv("CI"))
	th, _ := c.Select(ci)
	return &Run{ID: id, CI: ci, Thresholds: th, Criterion: c, tb: tb, root: root}
}

// Record writes acceptance/logs/<ID>.json.
func (r *Run) Record(measured map[string]any, pass bool, details string) {
	r.tb.Helper()
	if measured == nil {
		measured = map[string]any{}
	}
	dir := filepath.Join(r.root, LogDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.tb.Fatalf("harness: %v", err)
	}
	data, err := json.MarshalIndent(Log{ID: r.ID, Measured: measured, Pass: pass, Details: details}, "", "  ")
	if err != nil {
		r.tb.Fatalf("harness: marshal log: %v", err)
	}
	path := filepath.Join(dir, r.ID+".json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		r.tb.Fatalf("harness: %v", err)
	}
}

// NotImplemented records a failing log and fails the test by design.
func (r *Run) NotImplemented() {
	r.tb.Helper()
	r.Record(nil, false, NotImplementedMessage)
	r.tb.Fatalf("%s: %s; thresholds: %s", r.ID, NotImplementedMessage, r.Thresholds)
}

// Fail records a failing log with details and fails the test.
func (r *Run) Fail(measured map[string]any, format string, args ...any) {
	r.tb.Helper()
	msg := fmt.Sprintf(format, args...)
	r.Record(measured, false, msg)
	r.tb.Fatalf("%s: %s", r.ID, msg)
}

// Pass records a passing log.
func (r *Run) Pass(measured map[string]any, details string) {
	r.tb.Helper()
	r.Record(measured, true, details)
}
