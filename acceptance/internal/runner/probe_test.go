// SPDX-License-Identifier: Apache-2.0

package runner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thnhdatle/loupe/acceptance/internal/runner"
)

func newProber(ex runner.Executor, ci bool, e map[string]string) *runner.Prober {
	return &runner.Prober{Exec: ex, Root: ".", CI: ci, Getenv: env(e), Sibling: map[string]runner.Sibling{}}
}

var (
	bad  = fakeAnswer{err: errFake}
	sOK  = runner.StatusOK
	sBlk = runner.StatusBlocked
)

func one(prefix string, a fakeAnswer) map[string]fakeAnswer {
	return map[string]fakeAnswer{prefix: a}
}

func TestProber_Check(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		req   string
		exec  map[string]fakeAnswer
		ci    bool
		want  runner.Status
		match string
	}{
		{"python ok", "python", one("python3.12", fakeAnswer{out: "Python 3.12.4"}), false, sOK, "3.12"},
		{"python missing", "python", one("python3.12", bad), false, sBlk, "python3.12 --version"},
		{"node missing", "node", one("node", bad), false, sBlk, "node --version"},
		{"playwright ok", "playwright", nil, false, sOK, "playwright"},
		{"docker missing", "docker", one("docker info", bad), false, sBlk, "docker info"},
		{"mac ok", "mac", one("uname", fakeAnswer{out: "Darwin arm64\n"}), false, sOK, ""},
		{"mac linux", "mac", one("uname", fakeAnswer{out: "Linux x86_64\n"}), false, sBlk, "Linux"},
		{"mac uname error", "mac", one("uname", bad), false, sBlk, "uname"},
		{"mac on ci", "mac", nil, true, sOK, "CI"},
		{"unknown", "bogus", nil, false, sBlk, "bogus"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProber(&fakeExec{rules: tc.exec}, tc.ci, nil)
			got := p.Check(context.Background(), tc.req)
			assert.Equal(t, tc.want, got.Status, got.Detail)
			assert.Contains(t, got.Detail, tc.match)
		})
	}
}

func TestProber_Sibling_FallbackOrder(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{rules: map[string]fakeAnswer{
		"docker image inspect --format {{.Id}} custom":                {err: errFake},
		"docker image inspect --format {{.Id}} ghcr.io":               {err: errFake},
		"docker image inspect --format {{.Id}} thnhdatle/fakellm:dev": {out: "sha256:abc\n"},
	}}
	p := newProber(ex, false, map[string]string{"RAYLD_FAKELLM_IMAGE": "custom/img:1"})
	got := p.Check(context.Background(), "sibling:fakellm")
	assert.Equal(t, runner.StatusOK, got.Status)
	assert.Equal(t, "thnhdatle/fakellm:dev", p.Sibling["fakellm"].Image)
	assert.Equal(t, "sha256:abc", p.Sibling["fakellm"].ID)
	assert.Len(t, ex.calls, 3)
	assert.Equal(t, runner.StatusOK, p.Check(context.Background(), "sibling:fakellm").Status)
	assert.Len(t, ex.calls, 3)

	none := newProber(&fakeExec{rules: map[string]fakeAnswer{"docker": {err: errFake}}}, false, nil)
	assert.Equal(t, runner.StatusSkipped, none.Check(context.Background(), "sibling:fakellm").Status)
}
