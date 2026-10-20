// SPDX-License-Identifier: Apache-2.0

package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/runner"
)

func readReport(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "acceptance", "report.md"))
	require.NoError(t, err)
	return string(data)
}

func TestRunner_Run_CheckOnly(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	r, out := newRunner(root, runner.Options{CheckOnly: true}, &fakeExec{})
	code, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "ok:")

	write(t, root, "acceptance/d_test.go", "package a\nfunc TestAcceptance_DMO01_Dup(t *T) { t.Skip() }\n")
	r, out = newRunner(root, runner.Options{CheckOnly: true}, &fakeExec{})
	code, err = r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Contains(t, out.String(), "DMO-01: expected exactly one test")
	assert.Contains(t, out.String(), "skip construct")
}

func TestRunner_Run_ClassifiesRows(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	ex := &fakeExec{rules: map[string]fakeAnswer{
		"python3.12":           {out: "Python 3.12.1"},
		"docker image inspect": {err: errFake},
	}}
	ex.onRun = func(c runner.Command) {
		if c.Name == "go" && c.Args[0] == "test" {
			write(t, root, "acceptance/logs/DMO-01.json",
				`{"id":"DMO-01","measured":{"n":1},"pass":true,"details":"fine"}`)
		}
	}
	r, _ := newRunner(root, runner.Options{NoBuild: true, NoCompose: true, Version: "v9.9.9"}, ex)
	code, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	rep := readReport(t, root)
	assert.Contains(t, rep, "# Acceptance report — demo v9.9.9 —")
	assert.Contains(t, rep, "Environment: mac")
	assert.Contains(t, rep, "| DMO-01 | PASS | n=1 | n=1 |")
	assert.Contains(t, rep, "| DMO-02 | Blocked (environment) |")
	assert.Contains(t, rep, "| DMO-03 | Skipped (environment) |")
	assert.Contains(t, rep, "DMO-03: requires sibling:fakellm")
	assert.True(t, ex.called(
		"go test -tags acceptance -count=1 -timeout 3h -run ^TestAcceptance_DMO01_A$ -json ./acceptance/..."))
	assert.False(t, ex.called("go test -tags acceptance -count=1 -timeout 3h -run ^$ -bench"))
}

func TestRunner_Run_FailAndBlocked(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	ex := &fakeExec{rules: map[string]fakeAnswer{
		"python3.12":     {err: errFake},
		"docker compose": {err: errFake},
	}}
	r, _ := newRunner(root, runner.Options{NoBuild: true, CI: true, Only: []string{"DMO-01", "DMO-02"}}, ex)
	code, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	rep := readReport(t, root)
	assert.Contains(t, rep, "Environment: ci")
	assert.Contains(t, rep, "| DMO-01 | Blocked (environment) |")
	assert.Contains(t, rep, "| DMO-02 | Skipped (environment) |")
	assert.Contains(t, rep, "ci: skip")
	assert.Contains(t, rep, "docker compose -f deploy/docker-compose.yml up --wait")
	assert.NotContains(t, rep, "DMO-03")
}

func TestRunner_Run_FailWhenNoLog(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	ex := &fakeExec{rules: map[string]fakeAnswer{"go test": {err: errFake}}}
	r, _ := newRunner(root, runner.Options{NoBuild: true, NoCompose: true, Only: []string{"DMO-01"}}, ex)
	code, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	rep := readReport(t, root)
	assert.Contains(t, rep, "| DMO-01 | FAIL |")
	assert.Contains(t, rep, "no usable DMO-01.json")
	_, statErr := os.Stat(filepath.Join(root, "acceptance", "logs", "DMO-01.txt"))
	assert.NoError(t, statErr)
}

func TestRunner_Run_Benchmark(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	ex := &fakeExec{rules: map[string]fakeAnswer{
		"python3.12":     {out: "Python 3.12"},
		"uname":          {out: "Darwin arm64\n"},
		"docker compose": {},
	}}
	ex.onRun = func(c runner.Command) {
		if c.Name == "go" && c.Args[0] == "test" {
			write(t, root, "acceptance/logs/DMO-01.json", `{"id":"DMO-01","measured":{},"pass":true,"details":""}`)
			write(t, root, "acceptance/logs/DMO-02.json", `{"id":"DMO-02","measured":{},"pass":false,"details":"slow"}`)
		}
	}
	opts := runner.Options{NoBuild: true, Only: []string{"DMO-02", "DMO-01"}}
	r, _ := newRunner(root, opts, ex)
	code, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.True(t, ex.called("go test -tags acceptance -count=1 -timeout 3h -run ^$ -bench ^BenchmarkAcceptance_DMO02_B$ "+
		"-benchtime 1x"))
	assert.True(t, ex.called("docker compose -f deploy/docker-compose.yml stop loupe"))
	assert.Contains(t, readReport(t, root), "| DMO-02 | FAIL |")
}

func TestRunner_Run_BuildFailureAborts(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	ex := &fakeExec{rules: map[string]fakeAnswer{"go build": {err: errFake}}}
	r, _ := newRunner(root, runner.Options{NoCompose: true}, ex)
	code, err := r.Run(context.Background())
	require.Error(t, err)
	assert.Equal(t, 1, code)
}

func TestRunner_Run_Errors(t *testing.T) {
	t.Parallel()
	_, err := newRunnerNoRepo(t).Run(context.Background())
	require.Error(t, err)
	root := newRepo(t)
	r, _ := newRunner(root, runner.Options{Only: []string{"NOPE-01"}}, &fakeExec{})
	_, err = r.Run(context.Background())
	require.Error(t, err)
}

func newRunnerNoRepo(t *testing.T) *runner.Runner {
	t.Helper()
	r, _ := newRunner(t.TempDir(), runner.Options{}, &fakeExec{})
	return r
}

func TestReport_Markdown_Sections(t *testing.T) {
	t.Parallel()
	rp := &runner.Report{
		Project: "demo", Version: "v", Date: "d",
		Results: []runner.Result{{
			Criterion: criteria.Criterion{ID: "DMO-01"}, Outcome: runner.OutcomeFail, Detail: "a\nb",
			Measured: "x|y",
		}},
		Blocked:  []string{"compose: boom"},
		Problems: []string{"p"},
		Siblings: []runner.Sibling{
			{Name: "fakellm", Image: "ghcr.io/thnhdatle/fakellm:v0.1.0", Source: "released"},
			{Name: "other", Image: "thnhdatle/other:dev", Source: "local fallback", ID: "sha256:0123456789abcdef"},
		},
	}
	md := rp.Markdown()
	assert.True(t, rp.HasFailure())
	assert.Contains(t, md, "| ID | Result | Measured | Threshold |\n|---|---|---|---|\n| DMO-01 | FAIL | x/y | - |\n")
	assert.Contains(t, md, "- DMO-01: a b; attempts: 1\n")
	assert.NotContains(t, md, "logs/")
	assert.Contains(t, md, "- check: p")
	assert.Contains(t, md, "- compose: boom")
	assert.Contains(t, md, "- fakellm: ghcr.io/thnhdatle/fakellm:v0.1.0 (released)")
	assert.Contains(t, md, "- other: thnhdatle/other:dev @ 0123456789ab (local fallback)")
	assert.False(t, (&runner.Report{}).HasFailure())
}
