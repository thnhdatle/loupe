// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

// ComposeFile is the compose file brought up before the criteria run.
const ComposeFile = "deploy/docker-compose.yml"

// ReportPath is the repository-relative path of the generated report.
const ReportPath = "acceptance/report.md"

// ownService is the compose service of the project itself.
const ownService = "loupe"

// Runner executes the acceptance suite.
type Runner struct {
	opts   Options
	exec   Executor
	prober *Prober
	out    io.Writer
	now    func() time.Time
	blocks []string
}

// New creates a runner; getenv, exec and now are injectable for tests.
func New(opts Options, exec Executor, getenv func(string) string, out io.Writer) *Runner {
	return &Runner{
		opts: opts,
		exec: exec,
		out:  out,
		now:  time.Now,
		prober: &Prober{
			Exec: exec, Root: opts.Root, CI: opts.CI, Getenv: getenv,
			Sibling: map[string]Sibling{},
		},
	}
}

// Run executes the suite and returns the process exit code.
func (r *Runner) Run(ctx context.Context) (int, error) {
	f, err := criteria.Load(filepath.Join(r.opts.Root, "acceptance", criteria.FileName))
	if err != nil {
		return 1, err
	}
	selected, err := r.selectCriteria(f)
	if err != nil {
		return 1, err
	}
	problems, err := r.verify(f)
	if err != nil {
		return 1, err
	}
	if r.opts.CheckOnly {
		return r.finishCheck(problems), nil
	}
	if err := r.prepare(ctx); err != nil {
		return 1, err
	}
	rp := &Report{
		Project: f.Project, Version: r.opts.Version, Date: r.now().Format("2006-01-02"),
		CI: r.opts.CI, Problems: problems, Blocked: r.blocks,
		Results: r.runAll(ctx, selected),
	}
	rp.Siblings = r.siblings()
	if err := r.writeReport(rp); err != nil {
		return 1, err
	}
	if rp.HasFailure() {
		return 1, nil
	}
	return 0, nil
}

func (r *Runner) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, format, args...)
}

func (r *Runner) verify(f *criteria.File) ([]string, error) {
	problems, err := CheckCriteria(r.opts.Root, f)
	if err != nil {
		return nil, err
	}
	scan, err := ScanSkips(r.opts.Root)
	if err != nil {
		return nil, err
	}
	return append(problems, scan...), nil
}

func (r *Runner) finishCheck(problems []string) int {
	for _, p := range problems {
		r.printf("FAIL: %s\n", p)
	}
	if len(problems) > 0 {
		return 1
	}
	r.printf("ok: every criterion maps to exactly one test; no skip constructs\n")
	return 0
}

func (r *Runner) selectCriteria(f *criteria.File) ([]criteria.Criterion, error) {
	if len(r.opts.Only) == 0 {
		return append([]criteria.Criterion(nil), f.Criteria...), nil
	}
	var out []criteria.Criterion
	for _, id := range r.opts.Only {
		c, ok := f.Find(id)
		if !ok {
			return nil, fmt.Errorf("runner: unknown criterion %q in ONLY", id)
		}
		out = append(out, c)
	}
	return out, nil
}

// prepare builds from scratch and brings up compose; environment failures are
// recorded as Blocked entries, a failing Go build aborts the run.
func (r *Runner) prepare(ctx context.Context) error {
	if !r.opts.NoBuild {
		c := Command{Dir: r.opts.Root, Name: "go", Args: []string{"build", "-a", "./..."}}
		if out, err := r.exec.Run(ctx, c); err != nil {
			return fmt.Errorf("runner: %s failed: %w\n%s", c, err, out)
		}
		r.tolerate(ctx, Command{Dir: r.opts.Root, Name: "make", Args: []string{binDocker}})
	}
	if !r.opts.NoCompose {
		r.tolerate(ctx, Command{
			Dir: r.opts.Root, Name: binDocker,
			Args: []string{argCompose, "-f", ComposeFile, "up", "--wait"},
		})
	}
	return nil
}

func (r *Runner) tolerate(ctx context.Context, c Command) {
	if out, err := r.exec.Run(ctx, c); err != nil {
		r.blocks = append(r.blocks, fmt.Sprintf("%s: %v: %s", c, err, firstLine(out)))
		r.printf("blocked: %s: %v\n", c, err)
	}
}

func (r *Runner) siblings() []Sibling {
	out := make([]Sibling, 0, len(r.prober.Sibling))
	for _, s := range r.prober.Sibling {
		out = append(out, s)
	}
	return out
}

func (r *Runner) runAll(ctx context.Context, cs []criteria.Criterion) []Result {
	results := make([]Result, 0, len(cs))
	stoppedOwn := false
	for _, c := range cs {
		res := r.runOne(ctx, c, &stoppedOwn)
		r.printf("%s %s\n", c.ID, res.Outcome)
		results = append(results, res)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Criterion.ID < results[j].Criterion.ID })
	return results
}

func (r *Runner) runOne(ctx context.Context, c criteria.Criterion, stoppedOwn *bool) Result {
	th, ok := c.Select(r.opts.CI)
	if !ok {
		return Result{
			Criterion: c, Outcome: OutcomeSkipped, Requirement: "ci",
			Detail: "criteria.yaml marks this criterion ci: skip",
		}
	}
	if res, unmet := r.checkRequires(ctx, c, th); unmet {
		return res
	}
	if c.Kind == criteria.KindPerformance && !*stoppedOwn && !r.opts.NoCompose {
		r.tolerate(ctx, Command{
			Dir: r.opts.Root, Name: binDocker,
			Args: []string{argCompose, "-f", ComposeFile, "stop", ownService},
		})
		*stoppedOwn = true
	}
	return r.execute(ctx, c, th)
}

func (r *Runner) checkRequires(ctx context.Context, c criteria.Criterion, th criteria.Thresholds) (Result, bool) {
	for _, req := range c.AllRequires() {
		if p := r.prober.Check(ctx, req); p.Status != StatusOK {
			return r.unmet(c, th, req, p), true
		}
	}
	return Result{}, false
}

func (r *Runner) unmet(c criteria.Criterion, th criteria.Thresholds, req string, p ProbeResult) Result {
	o := OutcomeBlocked
	if p.Status == StatusSkipped {
		o = OutcomeSkipped
	}
	return Result{Criterion: c, Outcome: o, Threshold: th.String(), Requirement: req, Detail: p.Detail}
}

func (r *Runner) writeReport(rp *Report) error {
	path := filepath.Join(r.opts.Root, filepath.FromSlash(ReportPath))
	if err := os.WriteFile(path, []byte(rp.Markdown()), 0o600); err != nil {
		return fmt.Errorf("runner: write report: %w", err)
	}
	r.printf("report: %s\n", ReportPath)
	return nil
}
