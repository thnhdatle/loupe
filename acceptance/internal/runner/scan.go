// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

// skipConstructs are not used in acceptance sources: a criterion whose
// environment is missing is reported as Skipped or Blocked by the runner,
// never skipped by the test itself.
var skipConstructs = []string{
	"t.Skip", "Skipf", "SkipNow", "testing.Short",
	"pytest.mark.skip", "pytest.skip", "xfail",
}

var testFuncPattern = regexp.MustCompile(`^func ((?:Test|Benchmark)Acceptance_[A-Za-z0-9_]*)\(`)

// scanFiles lists the sources scanned for skip constructs: the
// top-level acceptance tests and Python files and the harness. The runner and
// its tests are excluded because they must name the constructs.
func scanFiles(root string) ([]string, error) {
	var files []string
	for _, pat := range []string{
		"acceptance/*_test.go", "acceptance/*.py", "acceptance/internal/harness/*.go",
	} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pat)))
		if err != nil {
			return nil, fmt.Errorf("runner: glob %s: %w", pat, err)
		}
		files = append(files, m...)
	}
	return files, nil
}

// ScanSkips returns one problem per skip construct found.
func ScanSkips(root string) ([]string, error) {
	files, err := scanFiles(root)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, f := range files {
		p, err := scanFile(root, f)
		if err != nil {
			return nil, err
		}
		problems = append(problems, p...)
	}
	return problems, nil
}

func scanFile(root, path string) ([]string, error) {
	fh, err := os.Open(path) //nolint:gosec // path comes from a fixed glob under the root.
	if err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	defer func() { _ = fh.Close() }()
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	var problems []string
	sc := bufio.NewScanner(fh)
	for n := 1; sc.Scan(); n++ {
		for _, w := range skipConstructs {
			if strings.Contains(sc.Text(), w) {
				problems = append(problems, fmt.Sprintf("%s:%d: skip construct %q", filepath.ToSlash(rel), n, w))
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("runner: scan %s: %w", rel, err)
	}
	return problems, nil
}

// testNames collects the acceptance test function names per file content.
func testNames(root string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "acceptance", "*_test.go"))
	if err != nil {
		return nil, fmt.Errorf("runner: glob tests: %w", err)
	}
	var names []string
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // path comes from a fixed glob under the root.
		if err != nil {
			return nil, fmt.Errorf("runner: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if m := testFuncPattern.FindStringSubmatch(line); m != nil {
				names = append(names, m[1])
			}
		}
	}
	return names, nil
}

// CheckCriteria verifies each criterion has exactly one test with its id in
// the name and that this test is the one named in criteria.yaml.
func CheckCriteria(root string, f *criteria.File) ([]string, error) {
	names, err := testNames(root)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, c := range f.Criteria {
		token := "_" + strings.ReplaceAll(c.ID, "-", "") + "_"
		var matches []string
		for _, n := range names {
			if strings.Contains(n, token) {
				matches = append(matches, n)
			}
		}
		switch {
		case len(matches) != 1:
			problems = append(problems, fmt.Sprintf("%s: expected exactly one test, found %d %v", c.ID, len(matches), matches))
		case matches[0] != c.Test:
			problems = append(problems, fmt.Sprintf("%s: test is %s, criteria.yaml says %s", c.ID, matches[0], c.Test))
		}
	}
	return problems, nil
}
