// SPDX-License-Identifier: Apache-2.0

package runner_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/acceptance/internal/runner"
)

var errFake = errors.New("fake failure")

// fakeExec answers commands by prefix match and records every call.
type fakeExec struct {
	mu    sync.Mutex
	rules map[string]fakeAnswer
	calls []string
	onRun func(c runner.Command)
}

type fakeAnswer struct {
	out string
	err error
}

func (f *fakeExec) Run(_ context.Context, c runner.Command) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := c.String()
	f.calls = append(f.calls, line)
	if f.onRun != nil {
		f.onRun(c)
	}
	for prefix, a := range f.rules {
		if strings.HasPrefix(line, prefix) {
			return a.out, a.err
		}
	}
	return "", nil
}

func (f *fakeExec) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const criteriaYAML = `project: demo
machine: m
criteria:
  - id: DMO-01
    title: a
    test: TestAcceptance_DMO01_A
    kind: functional
    real_deps: []
    requires: [python]
    pass:
      mac: { n: 1 }
      ci: { n: 1 }
  - id: DMO-02
    title: b
    test: BenchmarkAcceptance_DMO02_B
    kind: performance
    real_deps: [svc]
    requires: [mac]
    pass:
      mac: { n: 2 }
      ci: skip
  - id: DMO-03
    title: c
    test: TestAcceptance_DMO03_C
    kind: functional
    requires: ["sibling:fakellm"]
    pass:
      mac: { n: 3 }
      ci: { n: 3 }
`

// newRepo creates a repository layout with a criteria file and test sources.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "acceptance/criteria.yaml", criteriaYAML)
	write(t, root, "acceptance/a_test.go", "package a\nfunc TestAcceptance_DMO01_A(t *T) {}\n")
	write(t, root, "acceptance/b_test.go", "package a\nfunc BenchmarkAcceptance_DMO02_B(b *B) {}\n")
	write(t, root, "acceptance/c_test.go", "package a\nfunc TestAcceptance_DMO03_C(t *T) {}\n")
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

func newRunner(root string, opts runner.Options, ex runner.Executor) (*runner.Runner, *bytes.Buffer) {
	opts.Root = root
	var out bytes.Buffer
	return runner.New(opts, ex, env(nil), &out), &out
}
