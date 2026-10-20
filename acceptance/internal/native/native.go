// SPDX-License-Identifier: Apache-2.0

package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

const (
	readyTimeout = 60 * time.Second
	stopTimeout  = 60 * time.Second
	buildTimeout = 10 * time.Minute
)

var (
	buildOnce sync.Once
	buildPath string
	errBuild  error
	reapOnce  sync.Once
)

// nativeDir is where the binary and the pid files live.
func nativeDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	return filepath.Join(cache, "rayld", "loupe", "acceptance-native"), nil
}

// pidDir holds one file per running child, named after the child pid and
// containing the pid of the test process that started it. A test process that
// dies without running its cleanup (go test -timeout panic, SIGKILL) leaves its
// files behind; the next Start in any test process kills those children.
func pidDir() (string, error) {
	dir, err := nativeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pids"), nil
}

// Binary builds ./cmd/loupe (CGO_ENABLED=0) once per test process and returns
// the path of the executable.
func Binary(tb testing.TB) string {
	tb.Helper()
	buildOnce.Do(func() { buildPath, errBuild = build() })
	if errBuild != nil {
		tb.Fatalf("native: %v", errBuild)
	}
	return buildPath
}

func build() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		return "", fmt.Errorf("find root: %w", err)
	}
	dir, err := nativeDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf("loupe-%d.tmp", os.Getpid()))
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", tmp, "./cmd/loupe") //nolint:gosec // fixed arguments.
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/loupe: %w\n%s", err, out)
	}
	final := filepath.Join(dir, "loupe")
	// Rename is atomic: a concurrently running older binary keeps its inode.
	if err := os.Rename(tmp, final); err != nil {
		return "", fmt.Errorf("install binary: %w", err)
	}
	return final, nil
}

// Opts configure Start. The zero value runs `loupe serve` with capture off on
// free loopback ports and storage in a temp dir.
type Opts struct {
	// Dir is the working directory (database, log). Default t.TempDir().
	Dir string
	// Env adds or overrides LOUPE_* environment variables (for example
	// "LOUPE_CONTENT_CAPTURE": "on").
	Env map[string]string
	// Args are the arguments after the binary name. Default {"serve"}.
	Args []string
	// NoWait skips waiting for /readyz.
	NoWait bool
}

// Ports are the loopback listeners of a started process.
type Ports struct {
	GRPC, HTTP, UI, Admin int
}

// Proc is a running loupe process.
type Proc struct {
	Ports   Ports
	LogPath string // stdout and stderr
	DBPath  string
	Dir     string
	// StartedAt is the wall time taken just before the process was exec'd.
	StartedAt time.Time

	cmd  *exec.Cmd
	logf *os.File
	done chan struct{}
	err  error
}

// HTTPURL is the base URL of the OTLP/HTTP receiver.
func (p *Proc) HTTPURL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.HTTP))
}

// UIURL is the base URL of the API and dashboard listener.
func (p *Proc) UIURL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.UI))
}

// AdminURL is the base URL of the admin listener.
func (p *Proc) AdminURL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.Admin))
}

// FreePorts returns n distinct free loopback TCP ports.
func FreePorts(tb testing.TB, n int) []int {
	tb.Helper()
	ls := make([]net.Listener, 0, n)
	ports := make([]int, 0, n)
	for range n {
		l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			tb.Fatalf("native: free port: %v", err)
		}
		ls = append(ls, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port) //nolint:forcetypeassert // TCP listener.
	}
	for _, l := range ls {
		_ = l.Close()
	}
	return ports
}

// Start builds the binary, starts it and (unless NoWait) waits for /readyz. The
// process is killed at test cleanup if still running.
func Start(tb testing.TB, o Opts) *Proc {
	tb.Helper()
	bin := Binary(tb)
	pids, err := pidDir()
	if err != nil {
		tb.Fatalf("native: %v", err)
	}
	reapOnce.Do(func() { ReapOrphans(pids, bin) })
	if o.Dir == "" {
		o.Dir = tb.TempDir()
	}
	if o.Args == nil {
		o.Args = []string{"serve"}
	}
	fp := FreePorts(tb, 4)
	p := &Proc{
		Ports:   Ports{GRPC: fp[0], HTTP: fp[1], UI: fp[2], Admin: fp[3]},
		LogPath: filepath.Join(o.Dir, "loupe.log"),
		DBPath:  filepath.Join(o.Dir, "loupe.db"),
		Dir:     o.Dir,
		done:    make(chan struct{}),
	}
	env := map[string]string{
		"LOUPE_RECEIVER_GRPC_LISTEN":           net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.GRPC)),
		"LOUPE_RECEIVER_HTTP_LISTEN":           net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.HTTP)),
		"LOUPE_UI_LISTEN":                      net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.UI)),
		"LOUPE_ADMIN_LISTEN":                   net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.Admin)),
		"LOUPE_STORAGE_PATH":                   p.DBPath,
		"LOUPE_CONTENT_CAPTURE":                "off",
		"LOUPE_LOG_LEVEL":                      "debug",
		"LOUPE_UI_ALLOW_UNAUTHENTICATED":       "true",
		"LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED": "true",
	}
	for k, v := range o.Env {
		env[k] = v
	}
	f, err := os.Create(p.LogPath)
	if err != nil {
		tb.Fatalf("native: create log: %v", err)
	}
	p.logf = f
	cmd := exec.CommandContext(context.Background(), bin, o.Args...) //nolint:gosec // built by Binary.
	cmd.Dir = o.Dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = f, f
	p.StartedAt = time.Now()
	if err = cmd.Start(); err != nil {
		_ = f.Close()
		tb.Fatalf("native: start: %v", err)
	}
	p.cmd = cmd
	pidFile, err := recordPid(pids, cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = f.Close()
		tb.Fatalf("native: %v", err)
	}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	tb.Cleanup(func() {
		p.signal(syscall.SIGKILL)
		<-p.done
		_ = f.Close()
		_ = os.Remove(pidFile)
	})
	if !o.NoWait {
		if err := p.WaitReady(readyTimeout); err != nil {
			tb.Fatalf("native: %v (log %s)", err, p.LogPath)
		}
	}
	return p
}

// WaitReady polls the admin /readyz until it answers 200.
func (p *Proc) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("process exited before ready: %w", p.err)
		default:
		}
		resp, err := client.Get(p.AdminURL() + "/readyz") //nolint:noctx // short polling loop.
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("readyz not 200 in time")
}

func (p *Proc) signal(s syscall.Signal) {
	select {
	case <-p.done:
	default:
		_ = p.cmd.Process.Signal(s)
	}
}

// Stop sends SIGTERM and waits for the process to exit. It returns the exit
// error (nil on a clean exit).
func (p *Proc) Stop() error {
	p.signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return p.err
	case <-time.After(stopTimeout):
		p.signal(syscall.SIGKILL)
		<-p.done
		return fmt.Errorf("did not stop within %s: %w", stopTimeout, p.err)
	}
}

// Kill9 sends SIGKILL and waits for the process to be reaped.
func (p *Proc) Kill9() {
	p.signal(syscall.SIGKILL)
	<-p.done
}

// Pid is the operating-system process id.
func (p *Proc) Pid() int { return p.cmd.Process.Pid }

// recordPid writes dir/<child> containing this test process's pid.
func recordPid(dir string, child int) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, strconv.Itoa(child))
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return "", fmt.Errorf("write pid file: %w", err)
	}
	return path, nil
}

// ReapOrphans kills the children recorded in dir whose owning test process is
// gone and whose argv[0] is bin, and removes their pid files. Files of live
// owners are left alone.
func ReapOrphans(dir, bin string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		child, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // our own pid dir.
		if err != nil {
			continue
		}
		owner, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && alive(owner) {
			continue
		}
		// Guard against pid reuse: only kill a process still running our binary.
		if alive(child) && argv0(child) == bin {
			_ = syscall.Kill(child, syscall.SIGKILL)
		}
		_ = os.Remove(path)
	}
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// argv0 returns the first word of the process's command line, or "".
func argv0(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ps := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid)) //nolint:gosec // pid is an int.
	out, err := ps.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Exited reports whether the process has exited.
func (p *Proc) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
