// SPDX-License-Identifier: Apache-2.0

package criteria_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

const sample = `
project: demo
machine: m
criteria:
  - id: DMO-01
    title: t
    test: TestAcceptance_DMO01_X
    kind: performance
    real_deps: []
    requires: []
    pass:
      mac: { a: 1, b: 2 }
      ci: skip
  - id: DMO-02
    title: t
    test: BenchmarkAcceptance_DMO02_Y
    kind: functional
    pass:
      mac: { a: 1 }
      ci: { a: 2 }
`

func TestParse_Sample_SelectsThresholds(t *testing.T) {
	t.Parallel()
	f, err := criteria.Parse([]byte(sample))
	require.NoError(t, err)
	c1, ok := f.Find("DMO-01")
	require.True(t, ok)
	th, ok := c1.Select(false)
	assert.True(t, ok)
	assert.Equal(t, "a=1, b=2", th.String())
	_, ok = c1.Select(true)
	assert.False(t, ok)
	assert.Equal(t, []string{"mac"}, c1.AllRequires())
	c2, _ := f.Find("DMO-02")
	th, ok = c2.Select(true)
	assert.True(t, ok)
	assert.Equal(t, "a=2", th.String())
	assert.True(t, c2.IsBenchmark())
	assert.False(t, c1.IsBenchmark())
	_, ok = f.Find("NOPE-01")
	assert.False(t, ok)
}

func TestParse_Invalid_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"bad yaml", "project: [x"},
		{"bad id", "project: p\ncriteria:\n  - {id: x, title: t, test: T, kind: k}\n"},
		{"missing fields", "project: p\ncriteria:\n  - {id: AA-01}\n"},
		{"duplicate", "project: p\ncriteria:\n  - {id: AA-01, title: t, test: T, kind: k}\n" +
			"  - {id: AA-01, title: t, test: T, kind: k}\n"},
		{"bad ci", "project: p\ncriteria:\n  - {id: AA-01, title: t, test: T, kind: k, pass: {mac: {a: 1}, ci: nope}}\n"},
		{"bad pass", "project: p\ncriteria:\n  - {id: AA-01, title: t, test: T, kind: k, pass: [1]}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := criteria.Parse([]byte(tc.in))
			assert.Error(t, err)
		})
	}
}

func TestLoad_RealFile_Valid(t *testing.T) {
	t.Parallel()
	f, err := criteria.Load(filepath.Join("..", "..", criteria.FileName))
	require.NoError(t, err)
	assert.Len(t, f.Criteria, 12)
	_, err = criteria.Load("does-not-exist.yaml")
	assert.Error(t, err)
}

func TestFindRoot_Nested_FindsGoMod(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600))
	nested := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	got, err := criteria.FindRoot(nested)
	require.NoError(t, err)
	assert.Equal(t, root, got)
	_, err = criteria.FindRoot(filepath.VolumeName(root) + string(filepath.Separator))
	assert.ErrorIs(t, err, criteria.ErrNoRoot)
}

func TestIsCI_Values(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{"": false, "0": false, "false": false, "1": true, "true": true} {
		assert.Equal(t, want, criteria.IsCI(v), v)
	}
}

func TestFormatMap_Sorted(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a=1, b=x", criteria.FormatMap(map[string]any{"b": "x", "a": 1}))
}
