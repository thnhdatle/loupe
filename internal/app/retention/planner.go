// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// DefaultVacuumPages is the number of free pages one incremental-vacuum job
// releases (8 MiB with 4 KiB pages), so that a vacuum after a large delete
// does not hold the writer for long.
const DefaultVacuumPages = 2048

// ErrInvalidArgument reports a nil dependency or an invalid option given to New.
var ErrInvalidArgument = errors.New("retention: invalid argument")

// FreeDiskFunc returns the bytes available to Loupe on the file system that
// holds the database.
type FreeDiskFunc func(ctx context.Context) (int64, error)

type options struct {
	logger      *slog.Logger
	freeDisk    FreeDiskFunc
	vacuumPages int
}

// Option configures a Planner.
type Option func(*options)

// WithLogger sets the logger (default: discard).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithFreeDisk sets the free-disk probe used by the startup check of Run
// (default: none, the check is skipped).
func WithFreeDisk(f FreeDiskFunc) Option { return func(o *options) { o.freeDisk = f } }

// WithVacuumPages sets the free pages released per incremental-vacuum job
// (default DefaultVacuumPages).
func WithVacuumPages(n int) Option { return func(o *options) { o.vacuumPages = n } }

// Planner deletes data past its retention and enforces the disk cap
// by submitting bounded jobs to the single writer.
type Planner struct {
	store Store
	jobs  trace.JobRunner
	clk   clock.Clock
	cfg   Config
	opts  options
	log   *slog.Logger

	passMu sync.Mutex // serialises passes started by Run and Pass
	stats  counters
}

// New validates cfg and returns a Planner. It starts nothing; call Run.
func New(store Store, jobs trace.JobRunner, clk clock.Clock, cfg Config, opts ...Option) (*Planner, error) {
	if store == nil || jobs == nil || clk == nil {
		return nil, fmt.Errorf("%w: store, job runner and clock are required", ErrInvalidArgument)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	o := options{logger: slog.New(slog.DiscardHandler), vacuumPages: DefaultVacuumPages}
	for _, opt := range opts {
		opt(&o)
	}
	if o.logger == nil || o.vacuumPages < 1 {
		return nil, fmt.Errorf("%w: logger must be non-nil and vacuum pages at least 1", ErrInvalidArgument)
	}
	return &Planner{
		store: store, jobs: jobs, clk: clk, cfg: cfg, opts: o,
		log: o.logger.With("component", "retention"),
	}, nil
}

// Stats returns a snapshot of the counters.
func (p *Planner) Stats() Stats { return p.stats.snapshot() }

// Run checks free disk, runs a pass at once and then one every
// retention.interval on the planner's clock, until ctx ends or the writer
// shuts down; both return nil. Pass errors are logged and retried at the next
// interval.
func (p *Planner) Run(ctx context.Context) error {
	p.checkFreeDisk(ctx)
	tick := p.clk.NewTicker(p.cfg.Interval)
	defer tick.Stop()
	for {
		if err := p.Pass(ctx); err != nil {
			if isStop(ctx, err) {
				p.log.Debug("retention stopped", "status", "shutdown")
				return nil
			}
			p.log.Error("retention pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C():
		}
	}
}

// Pass runs one retention pass now: every class in Classes order, an
// incremental vacuum and checkpoint, then the disk cap. Concurrent calls run
// one after the other. It returns ctx's error or one wrapping
// trace.ErrShuttingDown as soon as either happens, and otherwise the joined
// errors of the steps that failed (later steps still run).
func (p *Planner) Pass(ctx context.Context) error {
	p.passMu.Lock()
	defer p.passMu.Unlock()
	err := p.pass(ctx)
	p.stats.passes.Add(1)
	if err != nil && !isStop(ctx, err) {
		p.stats.passErrors.Add(1)
	}
	return err
}

func (p *Planner) pass(ctx context.Context) error {
	now := p.clk.Now()
	var errs []error
	for _, c := range Classes() {
		n, err := p.purgeAll(ctx, c, now.Add(-p.age(c)))
		if n > 0 {
			p.stats.deleted[classIndex(c)].Add(n)
			p.log.Debug("retention deleted rows", "operation", jobName(c), "count", n)
		}
		if err != nil {
			if isStop(ctx, err) {
				return err
			}
			errs = append(errs, err)
		}
	}
	if _, err := p.compact(ctx); err != nil {
		if isStop(ctx, err) {
			return err
		}
		errs = append(errs, err)
	}
	if p.cfg.MaxDBBytes > 0 {
		if err := p.enforceCap(ctx, now); err != nil {
			errs = append(errs, err)
		}
	} else if _, err := p.measure(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// age returns how old rows of c must be to go; sessions use now as cutoff.
func (p *Planner) age(c Class) time.Duration {
	switch c {
	case ClassContent:
		return p.cfg.Content
	case ClassOrphanContent:
		return p.cfg.OrphanContent
	case ClassSpans:
		return p.cfg.Spans
	case ClassRollupsHourly:
		return p.cfg.RollupsHourly
	case ClassRollupsDaily:
		return p.cfg.RollupsDaily
	case ClassSessions:
		return 0 // every session without traces goes
	}
	return 0
}

func jobName(c Class) string { return "retention_" + string(c) }

// purgeAll submits one Purge job of retention.batch rows after another until a
// job deletes fewer rows, so ingest batches run between the jobs.
func (p *Planner) purgeAll(ctx context.Context, c Class, cutoff time.Time) (int64, error) {
	var total int64
	for {
		n, err := p.purgeOnce(ctx, jobName(c), c, cutoff)
		total += n
		if err != nil || n < int64(p.cfg.Batch) {
			return total, err
		}
	}
}

// purgeOnce runs one Purge of at most retention.batch rows as a writer job.
func (p *Planner) purgeOnce(ctx context.Context, name string, c Class, cutoff time.Time) (int64, error) {
	var deleted int64
	err := p.submit(ctx, name, func(jctx context.Context) error {
		n, err := p.store.Purge(jctx, c, cutoff, p.cfg.Batch)
		if err != nil {
			return fmt.Errorf("purge %s: %w", c, err)
		}
		deleted = int64(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// submit runs fn as a writer job. Values fn writes may be read only after a
// nil return: then fn has finished and Submit's channel receive orders its
// writes before the read. After a non-nil return fn may still be running.
func (p *Planner) submit(ctx context.Context, name string, fn func(context.Context) error) error {
	if err := p.jobs.Submit(ctx, trace.Job{Name: name, Run: fn}); err != nil {
		return fmt.Errorf("retention: %s: %w", name, err)
	}
	return nil
}

// compact releases free pages in bounded vacuum jobs, then truncates the WAL.
// busy reports a checkpoint that could not complete because of readers.
func (p *Planner) compact(ctx context.Context) (busy bool, err error) {
	prev := -1
	for {
		var remaining int
		err = p.submit(ctx, "retention_vacuum", func(jctx context.Context) error {
			r, verr := p.store.IncrementalVacuum(jctx, p.opts.vacuumPages)
			if verr != nil {
				return fmt.Errorf("incremental vacuum: %w", verr)
			}
			remaining = r
			return nil
		})
		if err != nil {
			return false, err
		}
		// Stop when the free list is empty, or when it stopped shrinking
		// because concurrent batches keep freeing a few pages.
		if remaining == 0 || (prev >= 0 && remaining >= prev) {
			break
		}
		prev = remaining
	}
	// The job writes its own variable, not the named result: after an error
	// return it may still be running.
	var walBusy bool
	err = p.submit(ctx, "retention_checkpoint", func(jctx context.Context) error {
		b, cerr := p.store.Checkpoint(jctx)
		if cerr != nil {
			return fmt.Errorf("wal checkpoint: %w", cerr)
		}
		walBusy = b
		return nil
	})
	if err != nil {
		return false, err
	}
	busy = walBusy
	if busy {
		p.stats.checkpointBusy.Add(1)
		p.log.Debug("wal checkpoint busy; WAL left for the next pass", "operation", "retention_checkpoint")
	}
	return busy, nil
}

// checkFreeDisk warns when the file system has less than twice the cap free.
func (p *Planner) checkFreeDisk(ctx context.Context) {
	if p.cfg.MaxDBBytes == 0 || p.opts.freeDisk == nil {
		return
	}
	free, err := p.opts.freeDisk(ctx)
	switch {
	case err != nil:
		p.log.Warn("free disk check failed", "error", err)
	case free/2 < p.cfg.MaxDBBytes:
		p.log.Warn("free disk is below twice retention.max_db_bytes", "bytes", free)
	}
}

func isStop(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, trace.ErrShuttingDown)
}
