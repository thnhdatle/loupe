// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// Defaults.
const (
	DefaultBatchSpans      = 2000
	DefaultBatchInterval   = 200 * time.Millisecond
	DefaultQueueSpans      = 100_000
	DefaultQueueBytes      = 256 << 20
	DefaultShutdownTimeout = 30 * time.Second
	// DefaultWriteTimeout bounds one WriteBatch call.
	DefaultWriteTimeout = 30 * time.Second
	// DefaultRetryAttempts is the number of WriteBatch attempts per batch before
	// it is dropped; with the default backoff the waits between attempts total at most 1.5 s.
	DefaultRetryAttempts = 5
	DefaultRetryBase     = 100 * time.Millisecond
	DefaultRetryMax      = 2 * time.Second
	// DefaultJobQueue is how many submitted jobs may wait for the writer.
	DefaultJobQueue = 16
)

var (
	// ErrInvalidConfig reports an invalid option value.
	ErrInvalidConfig = errors.New("writer: invalid configuration")
	// ErrInvalidItem reports an Enqueue call without a batch or with no slots.
	ErrInvalidItem = errors.New("writer: invalid queue item")
	// ErrInvalidJob reports a job without a name or a Run function.
	ErrInvalidJob = errors.New("writer: invalid job")
	// ErrAlreadyRunning reports a second call to Run.
	ErrAlreadyRunning = errors.New("writer: already running")
	// ErrDrainIncomplete reports that the shutdown timeout passed before every
	// queued item was written; the abandoned spans are counted in Stats.
	ErrDrainIncomplete = errors.New("writer: shutdown drain incomplete")
)

type config struct {
	batchSpans      int
	batchInterval   time.Duration
	queueSpans      int
	queueBytes      int64
	shutdownTimeout time.Duration
	writeTimeout    time.Duration
	retryAttempts   int
	retryBase       time.Duration
	retryMax        time.Duration
	jobQueue        int
	logger          *slog.Logger
	rnd             *rand.Rand
}

// Option configures a Writer.
type Option func(*config)

// WithBatchSpans sets writer.batch_spans: a batch is flushed once this many
// slots are queued. Items are never split, so one item larger than n forms a
// batch of its own.
func WithBatchSpans(n int) Option { return func(c *config) { c.batchSpans = n } }

// WithBatchInterval sets writer.batch_interval: whatever is queued is flushed
// at this period.
func WithBatchInterval(d time.Duration) Option { return func(c *config) { c.batchInterval = d } }

// WithQueueLimits sets ingest.queue_spans and ingest.queue_bytes, the bounds on
// slots and estimated bytes held by queued and in-flight items.
func WithQueueLimits(spans int, bytes int64) Option {
	return func(c *config) { c.queueSpans, c.queueBytes = spans, bytes }
}

// WithShutdownTimeout bounds the drain after Run's context ends (shutdown_timeout).
func WithShutdownTimeout(d time.Duration) Option { return func(c *config) { c.shutdownTimeout = d } }

// WithWriteTimeout bounds each WriteBatch call.
func WithWriteTimeout(d time.Duration) Option { return func(c *config) { c.writeTimeout = d } }

// WithRetry sets the number of WriteBatch attempts per batch and the backoff
// between them: base doubled per retry, capped at maxDelay, with equal jitter
// (a uniform delay in [d/2, d]).
func WithRetry(attempts int, base, maxDelay time.Duration) Option {
	return func(c *config) { c.retryAttempts, c.retryBase, c.retryMax = attempts, base, maxDelay }
}

// WithJobQueue sets how many submitted jobs may wait; Submit blocks beyond that.
func WithJobQueue(n int) Option { return func(c *config) { c.jobQueue = n } }

// WithLogger sets the logger; the default discards.
func WithLogger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }

// WithRand sets the source of backoff jitter; it is used only by the writer
// goroutine. The default is a fixed-seed PCG, which is fine for jitter.
func WithRand(r *rand.Rand) Option { return func(c *config) { c.rnd = r } }

func (c *config) validate() error {
	var errs []error
	check := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidConfig, msg))
		}
	}
	check(c.batchSpans > 0, "batch_spans must be positive")
	check(c.batchInterval > 0, "batch_interval must be positive")
	check(c.queueSpans > 0, "queue_spans must be positive")
	check(c.queueBytes > 0, "queue_bytes must be positive")
	check(c.shutdownTimeout > 0, "shutdown_timeout must be positive")
	check(c.writeTimeout > 0, "write timeout must be positive")
	check(c.retryAttempts > 0, "retry attempts must be positive")
	check(c.retryBase > 0 && c.retryMax >= c.retryBase, "retry backoff must satisfy 0 < base <= max")
	check(c.jobQueue > 0, "job queue must be positive")
	check(c.logger != nil, "logger must not be nil")
	check(c.rnd != nil, "rand must not be nil")
	return errors.Join(errs...)
}

// Writer is the single store writer. Enqueue, AddCount, Submit and Stats are
// safe for concurrent use; Run must be called once.
type Writer struct {
	store trace.SpanStore
	clk   clock.Clock
	cfg   config
	log   *slog.Logger

	wake    chan struct{}    // capacity 1: a full batch is queued
	jobs    chan *jobRequest // bounded by cfg.jobQueue
	closing chan struct{}    // closed when shutdown starts
	stopped chan struct{}    // closed when Run returns
	started atomic.Bool

	mu           sync.Mutex // guards the fields below
	pending      []*entry   // FIFO of items not yet taken into a batch
	pendingSlots int
	usedSlots    int // slots held by pending items and the batch being written
	usedBytes    int64
	counts       map[countKey]int64
	closed       bool

	stats counters
}

// New returns a Writer over store. It does not start goroutines; call Run.
func New(store trace.SpanStore, clk clock.Clock, opts ...Option) (*Writer, error) {
	if store == nil || clk == nil {
		return nil, fmt.Errorf("%w: store and clock are required", ErrInvalidConfig)
	}
	cfg := config{
		batchSpans:      DefaultBatchSpans,
		batchInterval:   DefaultBatchInterval,
		queueSpans:      DefaultQueueSpans,
		queueBytes:      DefaultQueueBytes,
		shutdownTimeout: DefaultShutdownTimeout,
		writeTimeout:    DefaultWriteTimeout,
		retryAttempts:   DefaultRetryAttempts,
		retryBase:       DefaultRetryBase,
		retryMax:        DefaultRetryMax,
		jobQueue:        DefaultJobQueue,
		logger:          slog.New(slog.DiscardHandler),
		rnd:             rand.New(rand.NewPCG(0x5eed, 0x10e)), //nolint:gosec // G404: non-crypto jitter
	}
	for _, o := range opts {
		o(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Writer{
		store:   store,
		clk:     clk,
		cfg:     cfg,
		log:     cfg.logger.With("component", "writer"),
		wake:    make(chan struct{}, 1),
		jobs:    make(chan *jobRequest, cfg.jobQueue),
		closing: make(chan struct{}),
		stopped: make(chan struct{}),
		counts:  make(map[countKey]int64),
	}, nil
}

var _ trace.JobRunner = (*Writer)(nil)
