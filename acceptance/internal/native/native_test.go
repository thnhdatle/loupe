// SPDX-License-Identifier: Apache-2.0

package native_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/acceptance/internal/native"
)

// startSleep runs `sleep 60` with argv[0] set to its full path, as native.Start
// does for the loupe binary.
func startSleep(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	bin, err := exec.LookPath("sleep")
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), bin, "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd, bin
}

// deadPid returns the pid of a process that has already exited.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

func writePid(t *testing.T, dir string, child, owner int) string {
	t.Helper()
	path := filepath.Join(dir, strconv.Itoa(child))
	require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(owner)), 0o600))
	return path
}

func TestReapOrphans_KillsChildOfDeadOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd, bin := startSleep(t)
	path := writePid(t, dir, cmd.Process.Pid, deadPid(t))

	native.ReapOrphans(dir, bin)

	err := cmd.Wait()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.Equal(t, syscall.SIGKILL, ws.Signal())
	assert.NoFileExists(t, path)
}

func TestReapOrphans_KeepsChildOfLiveOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd, bin := startSleep(t)
	path := writePid(t, dir, cmd.Process.Pid, os.Getpid())

	native.ReapOrphans(dir, bin)

	assert.FileExists(t, path)
	assert.NoError(t, cmd.Process.Signal(syscall.Signal(0)), "child must still run")
}

func TestReapOrphans_SkipsOtherBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd, _ := startSleep(t)
	path := writePid(t, dir, cmd.Process.Pid, deadPid(t))

	native.ReapOrphans(dir, "/nonexistent/loupe")

	assert.NoFileExists(t, path, "stale file is removed")
	time.Sleep(100 * time.Millisecond)
	assert.NoError(t, cmd.Process.Signal(syscall.Signal(0)), "a reused pid running another binary is not killed")
}
