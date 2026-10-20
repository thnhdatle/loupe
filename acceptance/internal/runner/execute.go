// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
)

// acceptanceTimeout replaces go test's 10 min default: LPE-08 runs about 6 min
// on the reference Mac and the LPE-02 benchmark runs 3 × 10 min.
const acceptanceTimeout = "3h"

// testCommand builds the go test invocation of one criterion.
func testCommand(root string, c criteria.Criterion, ci bool) Command {
	args := []string{"test", "-tags", "acceptance", "-count=1", "-timeout", acceptanceTimeout}
	if c.IsBenchmark() {
		args = append(args, "-run", "^$", "-bench", "^"+c.Test+"$", "-benchtime", "1x")
	} else {
		args = append(args, "-run", "^"+c.Test+"$", "-json")
	}
	args = append(args, "./acceptance/...")
	env := append(os.Environ(), "CI=")
	if ci {
		env = append(env, "CI=1")
	}
	return Command{Dir: root, Env: env, Name: "go", Args: args}
}

func removeStale(root, id string) error {
	for _, p := range []string{
		filepath.Join(root, harness.LogDir, id+".json"),
		filepath.Join(root, harness.LogDir, id+".txt"),
	} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("runner: remove stale log: %w", err)
		}
	}
	return nil
}

func readLog(root, id string) (*harness.Log, error) {
	data, err := os.ReadFile(filepath.Join(root, harness.LogDir, id+".json")) //nolint:gosec // fixed log dir.
	if err != nil {
		return nil, fmt.Errorf("runner: read log: %w", err)
	}
	var l harness.Log
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("runner: parse log: %w", err)
	}
	return &l, nil
}

// execute runs one criterion test and classifies the outcome.
func (r *Runner) execute(ctx context.Context, c criteria.Criterion, th criteria.Thresholds) Result {
	res := Result{Criterion: c, Threshold: th.String()}
	if err := removeStale(r.opts.Root, c.ID); err != nil {
		res.Outcome, res.Detail = OutcomeFail, err.Error()
		return res
	}
	cmd := testCommand(r.opts.Root, c, r.opts.CI)
	out, runErr := r.exec.Run(ctx, cmd)
	if err := os.MkdirAll(filepath.Join(r.opts.Root, harness.LogDir), 0o750); err == nil {
		txt := filepath.Join(r.opts.Root, filepath.FromSlash(LogPath(c.ID)))
		_ = os.WriteFile(txt, []byte(cmd.String()+"\n"+out), 0o600)
	}
	return classify(res, r.opts.Root, runErr)
}

func classify(res Result, root string, runErr error) Result {
	l, err := readLog(root, res.Criterion.ID)
	if err != nil {
		res.Outcome = OutcomeFail
		res.Detail = fmt.Sprintf("no usable %s.json (test error: %v; log error: %v)", res.Criterion.ID, runErr, err)
		return res
	}
	res.Measured = criteria.FormatMap(l.Measured)
	res.Detail = l.Details
	switch {
	case runErr != nil:
		res.Outcome = OutcomeFail
		res.Detail = fmt.Sprintf("test exited with error: %v; %s", runErr, l.Details)
	case !l.Pass:
		res.Outcome = OutcomeFail
	default:
		res.Outcome = OutcomePass
	}
	return res
}
