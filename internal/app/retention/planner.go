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

const DefaultVacuumPages = 2048

var ErrInvalidArgument = errors.New("retention: invalid argument")

type FreeDiskFunc func(ctx context.Context) (int64, error)

type options struct {
	logger      *slog.Logger
	freeDisk    FreeDiskFunc
	vacuumPages int
}

type Option func(*options)

func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

func WithFreeDisk(f FreeDiskFunc) Option { return func(o *options) { o.freeDisk = f } }

func WithVacuumPages(n int) Option { return func(o *options) { o.vacuumPages = n } }

// Planner deletes data past its retention and enforces the disk cap.
type Planner struct {
	store Store
	jobs  trace.JobRunner
	clk   clock.Clock
	cfg   Config
	opts  options
	log   *slog.Logger

	passMu sync.Mutex
	stats  counters
}

// New validates cfg and returns a Planner.
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

func (p *Planner) Stats() Stats { return p.stats.snapshot() }

// Run runs a retention pass at start and then at every interval until ctx ends.
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

// Pass runs one retention pass now.
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
		return 0
	}
	return 0
}

func jobName(c Class) string { return "retention_" + string(c) }

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

func (p *Planner) submit(ctx context.Context, name string, fn func(context.Context) error) error {
	if err := p.jobs.Submit(ctx, trace.Job{Name: name, Run: fn}); err != nil {
		return fmt.Errorf("retention: %s: %w", name, err)
	}
	return nil
}

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
		if remaining == 0 || (prev >= 0 && remaining >= prev) {
			break
		}
		prev = remaining
	}
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
