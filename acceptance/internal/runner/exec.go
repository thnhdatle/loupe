// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// waitDelay bounds how long Run waits for output pipes after the process group
// was killed.
const waitDelay = 5 * time.Second

// Names reused across the runner commands.
const (
	binDocker  = "docker"
	argCompose = "compose"
	argVersion = "--version"
)

// Command is one external command invocation.
type Command struct {
	Dir  string
	Env  []string
	Name string
	Args []string
}

// String renders the command line for reports.
func (c Command) String() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

// Executor runs external commands and returns their combined output.
type Executor interface {
	Run(ctx context.Context, c Command) (string, error)
}

// OSExecutor runs commands on the host.
type OSExecutor struct{}

// Run executes the command and returns stdout and stderr combined. The command
// runs in its own process group; cancelling ctx kills the whole group, so the
// test binary that `go test` starts and the loupe processes it runs do not
// outlive the runner.
func (OSExecutor) Run(ctx context.Context, c Command) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) //nolint:gosec // commands are fixed by the runner.
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	out, err := cmd.CombinedOutput()
	return string(out), err
}
