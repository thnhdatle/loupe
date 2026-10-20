// SPDX-License-Identifier: Apache-2.0

package harness_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
)

// fakeTB records fatal calls instead of stopping the goroutine.
type fakeTB struct {
	testing.TB
	fatal string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fatal = format
	panic(f)
}

func catch(fn func()) (caught bool) {
	defer func() {
		if r := recover(); r != nil {
			caught = true
		}
	}()
	fn()
	return false
}

func logPath(t *testing.T, id string) string {
	t.Helper()
	return filepath.Join("..", "..", "logs", id+".json")
}

func TestStart_RealCriteria_SelectsByCI(t *testing.T) {
	t.Setenv("CI", "")
	r := harness.Start(t, "LPE-04")
	assert.False(t, r.CI)
	assert.EqualValues(t, 1, r.Thresholds["p95_s_max"])
	t.Setenv("CI", "1")
	r = harness.Start(t, "LPE-04")
	assert.True(t, r.CI)
	assert.EqualValues(t, 3, r.Thresholds["p95_s_max"])
}

func TestRun_NotImplemented_WritesFailingLog(t *testing.T) {
	t.Setenv("CI", "")
	const id = "LPE-09"
	path := logPath(t, id)
	t.Cleanup(func() { _ = os.Remove(path) })
	tb := &fakeTB{TB: t}
	r := harness.Start(tb, id)
	assert.True(t, catch(r.NotImplemented))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var l harness.Log
	require.NoError(t, json.Unmarshal(data, &l))
	assert.Equal(t, id, l.ID)
	assert.False(t, l.Pass)
	assert.Equal(t, harness.NotImplementedMessage, l.Details)
}

func TestRun_PassAndFail_WriteLogs(t *testing.T) {
	t.Setenv("CI", "")
	const id = "LPE-10"
	path := logPath(t, id)
	t.Cleanup(func() { _ = os.Remove(path) })
	tb := &fakeTB{TB: t}
	r := harness.Start(tb, id)
	r.Pass(map[string]any{"markers_found": 0}, "ok")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"pass": true`)
	assert.True(t, catch(func() { r.Fail(nil, "bad %d", 1) }))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"pass": false`)
	assert.Contains(t, string(data), "bad 1")
}

func TestStart_UnknownCriterion_Fatal(t *testing.T) {
	t.Parallel()
	tb := &fakeTB{TB: t}
	assert.True(t, catch(func() { harness.Start(tb, "ZZZ-99") }))
}
