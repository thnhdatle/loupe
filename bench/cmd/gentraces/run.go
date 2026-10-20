// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite" for the final count.

	"github.com/thnhdatle/loupe/bench/gen"
	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const (
	requestSpans = 512 // target spans per ingest request; traces are never split
	retryWait    = 5 * time.Millisecond
)

type options struct {
	spans    int
	days     int
	end      time.Time
	seed     uint64
	db       string
	capture  string
	manifest string
	force    bool
}

// fixedClock reports a fixed time while timers stay real.
type fixedClock struct {
	clock.System
	at time.Time
}

func (c fixedClock) Now() time.Time { return c.at }

func parseFlags(args []string, errw io.Writer) (options, error) {
	var o options
	var end string
	fs := flag.NewFlagSet("gentraces", flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.IntVar(&o.spans, "spans", 2_000_000, "total spans")
	fs.IntVar(&o.days, "days", 30, "days covered by the dataset")
	fs.StringVar(&end, "end", "2026-10-01T00:00:00Z", "dataset anchor (RFC 3339); the range is [end-days, end)")
	fs.Uint64Var(&o.seed, "seed", 1, "random seed")
	fs.StringVar(&o.db, "db", "", "SQLite database to create (required)")
	fs.StringVar(&o.capture, "capture", "on", "content capture mode: on or off")
	fs.StringVar(&o.manifest, "manifest", "", "write the dataset manifest as JSON to this path")
	fs.BoolVar(&o.force, "force", false, "delete an existing database (and -wal, -shm)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.db == "" {
		return o, errors.New("--db is required")
	}
	t, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return o, fmt.Errorf("--end: %w", err)
	}
	o.end = t.UTC()
	return o, nil
}

func run(ctx context.Context, args []string, errw io.Writer) error {
	o, err := parseFlags(args, errw)
	if err != nil {
		return err
	}
	ds, err := gen.NewDataset(gen.DatasetConfig{Spans: o.spans, Days: o.days, End: o.end, Seed: o.seed})
	if err != nil {
		return err
	}
	if o.manifest != "" {
		if err := writeManifest(o.manifest, ds.Manifest()); err != nil {
			return err
		}
	}
	if err := prepareDB(o); err != nil {
		return err
	}
	start := time.Now()
	if err := build(ctx, o, ds, errw); err != nil {
		return err
	}
	if err := verifyCount(ctx, o.db, int64(o.spans)); err != nil {
		return err
	}
	return summary(errw, o, ds, time.Since(start))
}

func writeManifest(path string, m gen.Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	return nil
}

func prepareDB(o options) error {
	if _, err := os.Stat(o.db); err == nil {
		if !o.force {
			return fmt.Errorf("%s exists (use --force to replace it)", o.db)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(o.db + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// build feeds the whole dataset through the production write path and drains
// the writer before it returns.
func build(ctx context.Context, o options, ds *gen.Dataset, errw io.Writer) (err error) {
	cfg := config.Default()
	clk := fixedClock{at: o.end}
	table, err := pricingfile.Load("")
	if err != nil {
		return fmt.Errorf("load pricing: %w", err)
	}
	calc, err := cost.NewCalculator(table)
	if err != nil {
		return fmt.Errorf("build cost calculator: %w", err)
	}
	mode, err := content.ParseMode(o.capture)
	if err != nil {
		return fmt.Errorf("--capture: %w", err)
	}
	red, err := content.NewRedactor(cfg.Content.RedactPresets(), cfg.Content.RedactRules())
	if err != nil {
		return fmt.Errorf("content redaction: %w", err)
	}
	store, err := sqlite.Open(ctx, o.db, sqlite.WithClock(clk), sqlite.WithContentMaxBytes(int(cfg.Content.MaxBytes)))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close store: %w", cerr)
		}
	}()
	w, err := writer.New(store, clk,
		writer.WithBatchSpans(cfg.Writer.BatchSpans),
		writer.WithBatchInterval(cfg.Writer.BatchInterval.Std()),
		writer.WithQueueLimits(cfg.Ingest.QueueSpans, cfg.Ingest.QueueBytes.Bytes()),
		writer.WithShutdownTimeout(10*time.Minute))
	if err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	ing, err := ingest.New(w, calc, clk,
		ingest.WithContentPolicy(content.Policy{Mode: mode, MaxBytes: int(cfg.Content.MaxBytes), Redactor: red}),
		ingest.WithMaxAttributeBytes(int(cfg.Receiver.MaxAttributeBytes)),
		ingest.WithSyncAck(true))
	if err != nil {
		return fmt.Errorf("ingestor: %w", err)
	}

	// The writer runs on its own context so that it drains after the feeders end.
	wctx, wcancel := context.WithCancel(context.WithoutCancel(ctx))
	wdone := make(chan error, 1)
	go func() { wdone <- w.Run(wctx) }()
	feedErr := feed(ctx, ing, ds, errw)
	wcancel()
	if werr := <-wdone; werr != nil && feedErr == nil {
		feedErr = fmt.Errorf("writer drain: %w", werr)
	}
	return feedErr
}

// progress prints a line to w every 5% of the spans.
type progress struct {
	w       io.Writer
	total   int64
	step    int64
	started time.Time
	done    atomic.Int64
	mu      sync.Mutex
	next    int64
}

func newProgress(w io.Writer, total int64) *progress {
	step := max(total/20, 1)
	return &progress{w: w, total: total, step: step, started: time.Now(), next: step}
}

// add records n more spans as stored.
func (p *progress) add(n int64) {
	done := p.done.Add(n)
	p.mu.Lock()
	defer p.mu.Unlock()
	if done < p.next {
		return
	}
	for p.next <= done {
		p.next += p.step
	}
	_, _ = fmt.Fprintf(p.w, "gentraces: %d/%d spans (%d%%) %.0f spans/s\n",
		done, p.total, done*100/p.total, float64(done)/time.Since(p.started).Seconds())
}

type job struct{ lo, hi int }

// feed splits the traces into requests of about requestSpans spans and sends
// them from several workers. Request boundaries do not change trace content.
func feed(ctx context.Context, ing *ingest.Ingestor, ds *gen.Dataset, errw io.Writer) error {
	jobs := make(chan job)
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fail := func(err error) { errOnce.Do(func() { firstErr = err; cancel() }) }
	prog := newProgress(errw, int64(ds.NumSpans()))
	workers := min(max(runtime.GOMAXPROCS(0), 2), 8)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				var raws []trace.RawSpan
				for i := j.lo; i < j.hi; i++ {
					raws = append(raws, ds.Trace(i).Spans...)
				}
				if err := send(fctx, ing, raws); err != nil {
					fail(err)
					return
				}
				prog.add(int64(len(raws)))
			}
		}()
	}
	dispatch(fctx, ds, jobs)
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// dispatch sends consecutive trace ranges of about requestSpans spans.
func dispatch(ctx context.Context, ds *gen.Dataset, jobs chan<- job) {
	for lo := 0; lo < ds.NumTraces(); {
		hi, n := lo, 0
		for hi < ds.NumTraces() && n < requestSpans {
			n += ds.SpanCount(hi)
			hi++
		}
		select {
		case jobs <- job{lo, hi}:
		case <-ctx.Done():
			return
		}
		lo = hi
	}
}

// send ingests one request and retries it while the queue is full, so that no
// span is ever rejected or lost.
func send(ctx context.Context, ing *ingest.Ingestor, raws []trace.RawSpan) error {
	for {
		res, err := ing.IngestSpans(ctx, raws)
		switch {
		case errors.Is(err, trace.ErrOverloaded):
			select {
			case <-time.After(retryWait):
			case <-ctx.Done():
				return ctx.Err()
			}
		case err != nil:
			return fmt.Errorf("ingest: %w", err)
		case res.Accepted != len(raws) || res.Rejected != 0 || res.Dropped != 0:
			return fmt.Errorf("ingest: %d spans, result %+v", len(raws), res)
		default:
			return nil
		}
	}
}

func verifyCount(ctx context.Context, path string, want int64) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	defer func() { _ = db.Close() }()
	var got int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM spans").Scan(&got); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if got != want {
		return fmt.Errorf("verify: %d spans stored, want %d", got, want)
	}
	return nil
}

func summary(errw io.Writer, o options, ds *gen.Dataset, el time.Duration) error {
	var bytes int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(o.db + suffix); err == nil {
			bytes += fi.Size()
		}
	}
	m := ds.Manifest()
	_, err := fmt.Fprintf(errw, "gentraces: done spans=%d traces=%d sessions=%d elapsed=%s spans/s=%.0f db_bytes=%d\n",
		m.Spans, m.Traces, m.Sessions, el.Round(time.Millisecond), float64(m.Spans)/el.Seconds(), bytes)
	return err
}
