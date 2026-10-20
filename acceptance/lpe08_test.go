//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite".

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// LPE-08 schedule.
const (
	lpe08LowDays      = 15     // days 1–15: span retention binds
	lpe08LowPerDay    = 10_000 // spans per simulated day, days 1–15
	lpe08HighPerDay   = 60_000 // spans per simulated day, days 16–30
	lpe08BaselineDays = 7      // baseline commit p99: days 1–7
	// lpe08Nudge is the wall time after which a waiting batch gets a tick.
	lpe08Nudge = 50 * time.Millisecond
)

var lpe08Start = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

const lpe08Day = 24 * time.Hour

// TestAcceptance_LPE08_Retention is the acceptance test of criterion LPE-08.
//
// Real components run in process on a fake clock: the SQLite store on a temp
// file, the writer, the ingestor (content capture on, default redaction and
// max_bytes, default prices) and the retention planner with retention.spans
// 7d, retention.content 3d, max_db_bytes 200 MiB and the other defaults
// (interval 10m, batch 5000, rollups 35d / 400d). The workload is the LPE-08
// schedule, sent as export requests of 50 spans (5 agent runs, see
// lpe08_gen_test.go) plus their 5 GenAI log events, evenly spread over each
// simulated day. writer.batch_spans is set to the slots of one request, so
// each request commits as one batch, as it would at these rates with the
// default 200 ms batch interval; the test waits for each commit before the
// next request, so the fake clock never runs ahead of the writer.
//
// The planner pass that Run starts every retention.interval runs at each
// 10-minute boundary of simulated time (Planner.Pass, the body of Run's loop,
// called from the driver so that passes happen at exact simulated instants).
// Before each pass on the hour the size of main + WAL + SHM is sampled; after
// every pass the test checks that no span is older than
// retention.spans and no content older than retention.content. At the end it
// checks every hour and day of rollups and that their span counts equal the
// spans sent. Batch commit time is the wall time of the store's WriteBatch,
// as loupe_writer_batch_duration_seconds measures it.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE08_Retention(t *testing.T) {
	run := harness.Start(t, "LPE-08")
	th := lpe08ReadThresholds(t, run)
	sim := newLPE08Sim(t, run, th)
	measured := map[string]any{}
	if err := sim.run(); err != nil {
		run.Fail(sim.measured(measured), "simulation: %v", err)
	}
	if err := sim.finalChecks(); err != nil {
		run.Fail(sim.measured(measured), "final checks: %v", err)
	}
	sim.measured(measured)
	if problems := sim.verdict(); len(problems) > 0 {
		run.Fail(measured, "%s; %s", strings.Join(problems, "; "), sim.summary())
	}
	run.Pass(measured, sim.summary())
}

// lpe08Thresholds are the pass values of the criterion.
type lpe08Thresholds struct {
	days, spanDays, contentDays, rollupDays int
	capBytes                                int64
	overshootPct, p99Ratio                  float64
	expiredSpans, expiredContent            int64
}

func lpe08ReadThresholds(t *testing.T, run *harness.Run) lpe08Thresholds {
	t.Helper()
	num := func(k string) float64 { return lpe09Threshold(t, run, k) }
	return lpe08Thresholds{
		days:           int(num("simulated_days")),
		spanDays:       int(num("retention_spans_days")),
		contentDays:    int(num("retention_content_days")),
		rollupDays:     int(num("rollup_days_present")),
		capBytes:       int64(num("max_db_mib")) << 20,
		overshootPct:   num("cap_overshoot_pct_max"),
		p99Ratio:       num("commit_p99_ratio_max"),
		expiredSpans:   int64(num("expired_spans")),
		expiredContent: int64(num("expired_content")),
	}
}

// lpe08Commits records the wall time of every WriteBatch with the simulated
// time it ran at.
type lpe08Commits struct {
	trace.SpanStore
	clk *clock.Fake

	mu   sync.Mutex
	at   []time.Time
	took []time.Duration
}

// WriteBatch implements trace.SpanStore.
func (c *lpe08Commits) WriteBatch(ctx context.Context, b *trace.Batch) (trace.BatchResult, error) {
	start := time.Now()
	res, err := c.SpanStore.WriteBatch(ctx, b)
	took := time.Since(start)
	if err != nil {
		return res, fmt.Errorf("lpe08: write batch: %w", err)
	}
	c.mu.Lock()
	c.at = append(c.at, c.clk.Now())
	c.took = append(c.took, took)
	c.mu.Unlock()
	return res, nil
}

// p99 returns the nearest-rank p99 and the count of the commits whose
// simulated day (1-based; a commit at a day's end belongs to that day) is in
// [from, to].
func (c *lpe08Commits) p99(from, to int) (time.Duration, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ds []time.Duration
	for i, at := range c.at {
		day := int((at.Sub(lpe08Start)-1)/lpe08Day) + 1
		if day >= from && day <= to {
			ds = append(ds, c.took[i])
		}
	}
	if len(ds) == 0 {
		return 0, 0
	}
	slices.Sort(ds)
	return ds[int(math.Ceil(0.99*float64(len(ds))))-1], len(ds)
}

// lpe08Sim drives the scenario.
//
//nolint:containedctx // one test run owns the context for its whole life.
type lpe08Sim struct {
	t   *testing.T
	ctx context.Context
	th  lpe08Thresholds
	cfg config.Config

	dir     string
	path    string
	clk     *clock.Fake
	store   *sqlite.Store
	commits *lpe08Commits
	w       *writer.Writer
	queue   *lpe08Queue
	ing     *ingest.Ingestor
	planner *retention.Planner
	ro      *sql.DB
	gen     *lpe08Gen

	nextPass     time.Time
	sentPerDay   []int64 // spans sent, by day (index 0 = day 1)
	hourly       int
	maxHourly    int64
	maxHourlyAt  time.Time
	maxAnyPass   int64 // largest size before any pass (10-minute samples)
	maxAfterPass int64
	overCap      []string // hourly samples above cap + overshoot
	expired      []string // daily checks that found expired rows
	maxExpSpans  int64
	maxExpCont   int64
	day15        lpe08Footprint
	rollupHours  int64
	rollupDays   int
	rollupErrs   []string
	wallStart    time.Time
	nudges       int
	expiryChecks int
}

// lpe08Footprint is the database make-up at one instant.
type lpe08Footprint struct {
	size, spans, parts, bodyBytes, rawBytes, contentSpans int64
}

func newLPE08Sim(t *testing.T, run *harness.Run, th lpe08Thresholds) *lpe08Sim {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	s := &lpe08Sim{
		t: t, ctx: ctx, th: th, dir: dir, path: filepath.Join(dir, "loupe.db"),
		clk: clock.NewFake(lpe08Start), gen: newLPE08Gen(),
		sentPerDay: make([]int64, th.days), wallStart: time.Now(),
	}
	s.cfg = config.Default()
	s.cfg.Retention.Spans = config.Duration(time.Duration(th.spanDays) * lpe08Day)
	s.cfg.Retention.Content = config.Duration(time.Duration(th.contentDays) * lpe08Day)
	s.cfg.Retention.MaxDBBytes = config.Size(th.capBytes)
	s.cfg.Content.Capture = string(content.ModeOn)
	s.cfg.Writer.BatchSpans = lpe08RequestSpans + lpe08TracesPerRequest
	if err := s.cfg.Validate(); err != nil {
		run.Fail(nil, "config: %v", err)
	}
	if err := s.open(); err != nil {
		cancel()
		run.Fail(nil, "setup: %v", err)
	}
	writerDone := make(chan error, 1)
	wctx, stopWriter := context.WithCancel(context.Background())
	go func() { writerDone <- s.w.Run(wctx) }()
	t.Cleanup(func() {
		cancel()
		stopWriter()
		if err := <-writerDone; err != nil {
			t.Errorf("writer: %v", err)
		}
		_ = s.ro.Close()
		_ = s.store.Close()
	})
	return s
}

// open wires store, writer, ingestor and planner as cmd/loupe serve does.
func (s *lpe08Sim) open() error {
	table, err := pricingfile.Load("")
	if err != nil {
		return err
	}
	calc, err := cost.NewCalculator(table)
	if err != nil {
		return err
	}
	if s.store, err = sqlite.Open(s.ctx, s.path, sqlite.WithClock(s.clk)); err != nil {
		return err
	}
	s.commits = &lpe08Commits{SpanStore: s.store, clk: s.clk}
	cfg := &s.cfg
	if s.w, err = writer.New(s.commits, s.clk,
		writer.WithBatchSpans(cfg.Writer.BatchSpans),
		writer.WithBatchInterval(cfg.Writer.BatchInterval.Std()),
		writer.WithQueueLimits(cfg.Ingest.QueueSpans, cfg.Ingest.QueueBytes.Bytes()),
		writer.WithShutdownTimeout(cfg.ShutdownTimeout.Std())); err != nil {
		return err
	}
	red, err := content.NewRedactor(cfg.Content.RedactPresets(), cfg.Content.RedactRules())
	if err != nil {
		return err
	}
	s.queue = &lpe08Queue{Writer: s.w}
	if s.ing, err = ingest.New(s.queue, calc, s.clk,
		ingest.WithContentPolicy(content.Policy{
			Mode: content.ModeOn, MaxBytes: int(cfg.Content.MaxBytes), Redactor: red,
		}),
		ingest.WithMaxAttributeBytes(int(cfg.Receiver.MaxAttributeBytes))); err != nil {
		return err
	}
	if s.planner, err = retention.New(s.store, s.w, s.clk, cfg.RetentionConfig()); err != nil {
		return err
	}
	s.nextPass = lpe08Start
	s.ro, err = sql.Open("sqlite", "file:"+s.path+"?mode=ro&_pragma=busy_timeout(5000)")
	return err
}

// run sends the 30-day schedule, running passes at every interval boundary.
func (s *lpe08Sim) run() error {
	for day := 1; day <= s.th.days; day++ {
		perDay := lpe08LowPerDay
		if day > lpe08LowDays {
			perDay = lpe08HighPerDay
		}
		requests := perDay / lpe08RequestSpans
		gap := lpe08Day / time.Duration(requests)
		dayStart := lpe08Start.Add(time.Duration(day-1) * lpe08Day)
		for k := range requests {
			windowStart := dayStart.Add(time.Duration(k) * gap)
			if err := s.passesBefore(windowStart.Add(gap)); err != nil {
				return err
			}
			if err := s.send(day, windowStart, gap); err != nil {
				return err
			}
		}
		s.t.Logf("day %d done: wall %s, %s", day, time.Since(s.wallStart).Round(time.Second), s.sizeLine())
	}
	// The pass at the end of day 30, then the final state.
	return s.passesBefore(lpe08Start.Add(time.Duration(s.th.days)*lpe08Day + time.Nanosecond))
}

// passesBefore runs the pass of every interval boundary before t.
func (s *lpe08Sim) passesBefore(t time.Time) error {
	for s.nextPass.Before(t) {
		at := s.nextPass
		s.advanceTo(at)
		if err := s.pass(at); err != nil {
			return err
		}
		s.nextPass = at.Add(s.cfg.Retention.Interval.Std())
	}
	return nil
}

// pass samples the size, runs one planner pass and runs the daily checks.
func (s *lpe08Sim) pass(at time.Time) error {
	size, err := s.dbSize()
	if err != nil {
		return err
	}
	s.maxAnyPass = max(s.maxAnyPass, size)
	if at.Sub(lpe08Start)%time.Hour == 0 {
		s.sampleHourly(at, size)
	}
	if err = s.planner.Pass(s.ctx); err != nil {
		return fmt.Errorf("retention pass at %s: %w", at.Format(time.RFC3339), err)
	}
	if size, err = s.dbSize(); err != nil {
		return err
	}
	s.maxAfterPass = max(s.maxAfterPass, size)
	if err := s.expiryCheck(at); err != nil {
		return err
	}
	since := at.Sub(lpe08Start)
	if since > 0 && since%lpe08Day == 0 {
		return s.dailyCheck(at)
	}
	return nil
}

func (s *lpe08Sim) sampleHourly(at time.Time, size int64) {
	s.hourly++
	if size > s.maxHourly {
		s.maxHourly, s.maxHourlyAt = size, at
	}
	limit := float64(s.th.capBytes) * (1 + s.th.overshootPct/100)
	if float64(size) > limit {
		s.overCap = append(s.overCap, fmt.Sprintf("%s: %d bytes", at.Format(time.RFC3339), size))
	}
}

// send ingests one request (log events first, as a separate export) and
// waits until its batch has committed.
func (s *lpe08Sim) send(day int, windowStart time.Time, window time.Duration) error {
	end := windowStart.Add(window)
	s.advanceTo(end)
	spans, logs := s.gen.request(windowStart, window)
	if _, err := s.ing.IngestLogs(s.ctx, logs); err != nil {
		return fmt.Errorf("ingest logs: %w", err)
	}
	res, err := s.ing.IngestSpans(s.ctx, spans)
	if err != nil {
		return fmt.Errorf("ingest spans: %w", err)
	}
	if res.Accepted != len(spans) {
		return fmt.Errorf("ingest spans: %+v", res)
	}
	s.sentPerDay[day-1] += int64(len(spans))
	return s.waitCommitted()
}

// advanceTo moves the fake clock forward to t (never backwards).
func (s *lpe08Sim) advanceTo(t time.Time) {
	if d := t.Sub(s.clk.Now()); d > 0 {
		s.clk.Advance(d)
	}
}

// lpe08Queue is the writer as the ingestor's queue (sync_ack off, the
// default); it keeps the commit channel of every item so the driver can wait
// for the commit before it moves the clock on.
type lpe08Queue struct {
	*writer.Writer

	mu   sync.Mutex
	acks []<-chan error
}

// Enqueue implements ingest.Queue.
func (q *lpe08Queue) Enqueue(b *trace.Batch, slots int, bytes int64, wait bool) (<-chan error, error) {
	ack, err := q.Writer.Enqueue(b, slots, bytes, true)
	if err != nil {
		return nil, fmt.Errorf("lpe08: enqueue: %w", err)
	}
	q.mu.Lock()
	q.acks = append(q.acks, ack)
	q.mu.Unlock()
	if wait {
		return ack, nil
	}
	return nil, nil //nolint:nilnil // ingest.Queue contract: no channel without wait.
}

func (q *lpe08Queue) take() []<-chan error {
	q.mu.Lock()
	defer q.mu.Unlock()
	acks := q.acks
	q.acks = nil
	return acks
}

// waitCommitted waits until every enqueued item has committed. A
// batch-interval tick left over from an earlier Advance may flush the log
// records before the spans are enqueued; the spans then wait for the next
// tick, which this provides by advancing the clock by one
// writer.batch_interval when a commit takes longer than lpe08Nudge.
func (s *lpe08Sim) waitCommitted() error {
	for _, ack := range s.queue.take() {
		for waiting := true; waiting; {
			select {
			case err := <-ack:
				if err != nil {
					return fmt.Errorf("batch not committed: %w", err)
				}
				waiting = false
			case <-time.After(lpe08Nudge):
				s.nudges++
				s.clk.Advance(s.cfg.Writer.BatchInterval.Std())
			}
		}
	}
	return nil
}

// dbSize is main + WAL + SHM as found on disk.
func (s *lpe08Sim) dbSize() (int64, error) {
	var total int64
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		fi, err := os.Stat(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return 0, fmt.Errorf("stat %s: %w", p, err)
		default:
			total += fi.Size()
		}
	}
	return total, nil
}

func (s *lpe08Sim) count(q string, args ...any) (int64, error) {
	var n sql.NullInt64
	if err := s.ro.QueryRowContext(s.ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("query %q: %w", q, err)
	}
	return n.Int64, nil
}

// expiryCheck runs after every pass: nothing past its retention may be left.
func (s *lpe08Sim) expiryCheck(at time.Time) error {
	spanCut := at.Add(-s.cfg.Retention.Spans.Std()).UnixNano()
	contentCut := at.Add(-s.cfg.Retention.Content.Std()).UnixNano()
	spans, err := s.count("SELECT count(*) FROM spans WHERE start_ns < ?", spanCut)
	if err != nil {
		return err
	}
	parts, err := s.count("SELECT count(*) FROM span_content WHERE time_ns < ?", contentCut)
	if err != nil {
		return err
	}
	s.expiryChecks++
	s.maxExpSpans, s.maxExpCont = max(s.maxExpSpans, spans), max(s.maxExpCont, parts)
	if spans > s.th.expiredSpans || parts > s.th.expiredContent {
		s.expired = append(s.expired, fmt.Sprintf("%s: %d spans, %d content rows expired",
			at.Format(time.RFC3339), spans, parts))
	}
	return nil
}

// dailyCheck logs the database make-up after the pass at a day boundary and
// keeps the one at the end of day 15.
func (s *lpe08Sim) dailyCheck(at time.Time) error {
	f, err := s.footprint()
	if err != nil {
		return err
	}
	if at.Equal(lpe08Start.Add(lpe08LowDays * lpe08Day)) {
		s.day15 = f
	}
	s.t.Logf("%s: %d spans (%d B/span on disk), %d content rows of %d spans, raw %d B, stored %d B per content span",
		at.Format(time.DateOnly), f.spans, perSpan(f.size, f.spans), f.parts, f.contentSpans,
		perSpan(f.rawBytes, f.contentSpans), perSpan(f.bodyBytes, f.contentSpans))
	return nil
}

// footprint measures the database make-up.
func (s *lpe08Sim) footprint() (lpe08Footprint, error) {
	var f lpe08Footprint
	var err error
	if f.size, err = s.dbSize(); err != nil {
		return f, err
	}
	for _, q := range []struct {
		dst *int64
		sql string
	}{
		{&f.spans, "SELECT count(*) FROM spans"},
		{&f.parts, "SELECT count(*) FROM span_content"},
		{&f.bodyBytes, "SELECT sum(length(body)) FROM span_content"},
		{&f.rawBytes, "SELECT sum(bytes) FROM span_content"},
		{&f.contentSpans, "SELECT count(*) FROM (SELECT DISTINCT trace_id, span_id FROM span_content)"},
	} {
		if *q.dst, err = s.count(q.sql); err != nil {
			return f, err
		}
	}
	return f, nil
}

// finalChecks verifies rollups: every simulated hour and day has rows, and
// their span counts equal the spans sent that day.
func (s *lpe08Sim) finalChecks() error {
	var err error
	end := lpe08Start.Add(time.Duration(s.th.days) * lpe08Day).UnixNano()
	q := "SELECT count(DISTINCT bucket_ns) FROM %s WHERE bucket_ns >= ? AND bucket_ns < ?"
	if s.rollupHours, err = s.count(fmt.Sprintf(q, "rollup_hour"), lpe08Start.UnixNano(), end); err != nil {
		return err
	}
	if s.rollupDays, err = s.countDays(); err != nil {
		return err
	}
	for d := range s.th.days {
		from := lpe08Start.Add(time.Duration(d) * lpe08Day).UnixNano()
		to := from + int64(lpe08Day)
		for _, table := range []string{"rollup_hour", "rollup_day"} {
			n, err := s.count("SELECT sum(span_count) FROM "+table+" WHERE bucket_ns >= ? AND bucket_ns < ?", from, to)
			if err != nil {
				return err
			}
			if n != s.sentPerDay[d] {
				s.rollupErrs = append(s.rollupErrs, fmt.Sprintf("day %d %s span_count %d, sent %d",
					d+1, table, n, s.sentPerDay[d]))
			}
		}
	}
	return nil
}

// countDays returns the days that have both daily rows and 24 hourly buckets.
func (s *lpe08Sim) countDays() (int, error) {
	present := 0
	for d := range s.th.days {
		from := lpe08Start.Add(time.Duration(d) * lpe08Day).UnixNano()
		to := from + int64(lpe08Day)
		hours, err := s.count("SELECT count(DISTINCT bucket_ns) FROM rollup_hour WHERE bucket_ns >= ? AND bucket_ns < ?",
			from, to)
		if err != nil {
			return 0, err
		}
		days, err := s.count("SELECT count(*) FROM rollup_day WHERE bucket_ns = ?", from)
		if err != nil {
			return 0, err
		}
		if hours == 24 && days > 0 {
			present++
		}
	}
	return present, nil
}

func (s *lpe08Sim) sizeLine() string {
	size, _ := s.dbSize()
	st := s.planner.Stats()
	return fmt.Sprintf("db %.1f MiB, cap deleted content %d spans %d, cap unmet %d",
		mib(size), st.CapDeleted[retention.ClassContent], st.CapDeleted[retention.ClassSpans], st.CapUnmet)
}

func mib(n int64) float64 { return math.Round(float64(n)/(1<<20)*100) / 100 }

func (s *lpe08Sim) ratios() (base, cleanup time.Duration, nBase, nClean int, ratio float64) {
	base, nBase = s.commits.p99(1, lpe08BaselineDays)
	cleanup, nClean = s.commits.p99(lpe08BaselineDays+1, s.th.days)
	if base > 0 {
		ratio = float64(cleanup) / float64(base)
	}
	return base, cleanup, nBase, nClean, ratio
}

// measured fills m with the measured values.
func (s *lpe08Sim) measured(m map[string]any) map[string]any {
	base, cleanup, nBase, nClean, ratio := s.ratios()
	st := s.planner.Stats()
	f := s.day15
	m["simulated_days"] = s.th.days
	m["retention_spans_days"] = s.th.spanDays
	m["retention_content_days"] = s.th.contentDays
	m["max_db_mib"] = mib(s.th.capBytes)
	m["max_hourly_db_mib"] = mib(s.maxHourly)
	m["max_hourly_db_at"] = s.maxHourlyAt.Format(time.RFC3339)
	m["max_db_mib_any_pass_sample"] = mib(s.maxAnyPass)
	m["max_db_mib_after_pass"] = mib(s.maxAfterPass)
	m["cap_overshoot_pct_max"] = math.Round((float64(s.maxHourly)/float64(s.th.capBytes)-1)*1e4) / 100
	m["hourly_samples"] = s.hourly
	m["rollup_days_present"] = s.rollupDays
	m["rollup_hours_present"] = s.rollupHours
	m["expired_spans"] = s.maxExpSpans
	m["expired_content"] = s.maxExpCont
	m["expiry_checks"] = s.expiryChecks
	m["commit_p99_baseline_ms"] = ms(base)
	m["commit_p99_cleanup_ms"] = ms(cleanup)
	m["commits_baseline"] = nBase
	m["commits_cleanup"] = nClean
	m["commit_p99_ratio_max"] = math.Round(ratio*1000) / 1000
	m["spans_sent"] = int64(s.gen.spans)
	m["spans_per_request"] = lpe08RequestSpans
	m["content_raw_bytes_per_span_sent"] = perSpan(s.gen.rawBytes, int64(s.gen.spans))
	m["day15_db_mib"] = mib(f.size)
	m["day15_spans"] = f.spans
	m["day15_db_bytes_per_span"] = perSpan(f.size, f.spans)
	m["day15_content_rows"] = f.parts
	m["day15_content_raw_bytes_per_content_span"] = perSpan(f.rawBytes, f.contentSpans)
	m["day15_content_stored_bytes_per_content_span"] = perSpan(f.bodyBytes, f.contentSpans)
	m["retention_deleted"] = st.Deleted
	m["cap_deleted"] = st.CapDeleted
	m["cap_exceeded_passes"] = st.CapExceeded
	m["cap_unmet_passes"] = st.CapUnmet
	m["checkpoint_busy"] = st.CheckpointBusy
	m["passes"] = st.Passes
	m["writer_tick_nudges"] = s.nudges
	m["wall_seconds"] = math.Round(time.Since(s.wallStart).Seconds())
	return m
}

func ms(d time.Duration) float64 { return math.Round(float64(d)/float64(time.Millisecond)*1000) / 1000 }

func perSpan(total, n int64) int64 {
	if n == 0 {
		return 0
	}
	return total / n
}

// verdict lists every threshold the run missed.
func (s *lpe08Sim) verdict() []string {
	var out []string
	_, _, _, _, ratio := s.ratios()
	if len(s.overCap) > 0 {
		out = append(out, fmt.Sprintf("%d hourly samples above cap + %v%%, first %s",
			len(s.overCap), s.th.overshootPct, s.overCap[0]))
	}
	if s.hourly < s.th.days*24 {
		out = append(out, fmt.Sprintf("%d hourly samples, want %d", s.hourly, s.th.days*24))
	}
	if s.rollupDays < s.th.rollupDays || s.rollupHours < int64(s.th.days*24) {
		out = append(out, fmt.Sprintf("rollups present for %d days / %d hours, want %d / %d",
			s.rollupDays, s.rollupHours, s.th.rollupDays, s.th.days*24))
	}
	if len(s.rollupErrs) > 0 {
		out = append(out, fmt.Sprintf("%d rollup count mismatches, first %s", len(s.rollupErrs), s.rollupErrs[0]))
	}
	if len(s.expired) > 0 {
		out = append(out, fmt.Sprintf("%d post-pass checks found expired rows, first %s", len(s.expired), s.expired[0]))
	}
	if ratio > s.th.p99Ratio || ratio == 0 {
		out = append(out, fmt.Sprintf("commit p99 ratio %.3f, max %v", ratio, s.th.p99Ratio))
	}
	return out
}

func (s *lpe08Sim) summary() string {
	base, cleanup, nBase, nClean, ratio := s.ratios()
	return fmt.Sprintf("max hourly size %.2f MiB at %s (cap %.0f MiB + %v%%); %d hourly samples; "+
		"rollups %d days / %d hours; expired spans %d content %d (max over the checks after every pass); "+
		"commit p99 baseline %s (n=%d) cleanup %s (n=%d) ratio %.3f (max %v); day-15 footprint %d B/span",
		mib(s.maxHourly), s.maxHourlyAt.Format(time.RFC3339), mib(s.th.capBytes), s.th.overshootPct, s.hourly,
		s.rollupDays, s.rollupHours, s.maxExpSpans, s.maxExpCont,
		base, nBase, cleanup, nClean, ratio, s.th.p99Ratio, perSpan(s.day15.size, s.day15.spans))
}
