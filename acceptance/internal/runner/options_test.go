// SPDX-License-Identifier: Apache-2.0

package runner_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/runner"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		want    runner.Options
		wantErr bool
	}{
		{"defaults", nil, nil, runner.Options{Version: "dev"}, false},
		{
			"flags",
			[]string{"--no-build", "--no-compose", "--check-only", "--version", "v0.1.0"},
			nil,
			runner.Options{NoBuild: true, NoCompose: true, CheckOnly: true, Version: "v0.1.0"},
			false,
		},
		{
			"env", nil,
			map[string]string{"ONLY": "LPE-01, LPE-04", "CI": "1"},
			runner.Options{CI: true, Only: []string{"LPE-01", "LPE-04"}, Version: "dev"},
			false,
		},
		{
			"args override",
			[]string{"ONLY=LPE-05", "CI=0"},
			map[string]string{"CI": "1"},
			runner.Options{Only: []string{"LPE-05"}, Version: "dev"},
			false,
		},
		{"unknown arg", []string{"FOO=1"}, nil, runner.Options{}, true},
		{"bare arg", []string{"x"}, nil, runner.Options{}, true},
		{"bad flag", []string{"--nope"}, nil, runner.Options{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := runner.ParseArgs(tc.args, env(tc.env), io.Discard)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRepo_RealAcceptanceTree_Checks(t *testing.T) {
	t.Parallel()
	root, err := criteria.FindRoot(".")
	require.NoError(t, err)
	f, err := criteria.Load(filepath.Join(root, "acceptance", criteria.FileName))
	require.NoError(t, err)
	problems, err := runner.CheckCriteria(root, f)
	require.NoError(t, err)
	assert.Empty(t, problems)
	problems, err = runner.ScanSkips(root)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestOSExecutor_Run(t *testing.T) {
	t.Parallel()
	ex := runner.OSExecutor{}
	out, err := ex.Run(t.Context(), runner.Command{Dir: os.TempDir(), Name: "go", Args: []string{"version"}})
	require.NoError(t, err)
	assert.Contains(t, out, "go version")
	bad := runner.Command{Name: "go", Args: []string{"nope-cmd"}, Env: os.Environ()}
	_, err = ex.Run(t.Context(), bad)
	assert.Error(t, err)
}

func TestOSExecutor_RunCancelKillsProcessGroup(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runner.OSExecutor{}.Run(ctx, runner.Command{
		Name: "sh",
		Args: []string{"-c", `sleep 30 & echo $! > "$0"; wait`, pidFile},
	})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 10*time.Second, "Run must return once the group is killed")
	data, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, 5*time.Second, 50*time.Millisecond, "grandchild %d still running", pid)
}
