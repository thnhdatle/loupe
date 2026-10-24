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

const (
	DefaultBatchSpans      = 2000
	DefaultBatchInterval   = 200 * time.Millisecond
	DefaultQueueSpans      = 100_000
	DefaultQueueBytes      = 256 << 20
	DefaultShutdownTimeout = 30 * time.Second
	DefaultWriteTimeout    = 30 * time.Second
	DefaultRetryAttempts   = 5
	DefaultRetryBase       = 100 * time.Millisecond
	DefaultRetryMax        = 2 * time.Second
	DefaultJobQueue        = 16
)

var (
	ErrInvalidConfig   = errors.New("writer: invalid configuration")
	ErrInvalidItem     = errors.New("writer: invalid queue item")
	ErrInvalidJob      = errors.New("writer: invalid job")
	ErrAlreadyRunning  = errors.New("writer: already running")
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

type Option func(*config)

func WithBatchSpans(n int) Option { return func(c *config) { c.batchSpans = n } }

func WithBatchInterval(d time.Duration) Option { return func(c *config) { c.batchInterval = d } }

func WithQueueLimits(spans int, bytes int64) Option {
	return func(c *config) { c.queueSpans, c.queueBytes = spans, bytes }
}

func WithShutdownTimeout(d time.Duration) Option { return func(c *config) { c.shutdownTimeout = d } }

func WithWriteTimeout(d time.Duration) Option { return func(c *config) { c.writeTimeout = d } }

func WithRetry(attempts int, base, maxDelay time.Duration) Option {
	return func(c *config) { c.retryAttempts, c.retryBase, c.retryMax = attempts, base, maxDelay }
}

func WithJobQueue(n int) Option { return func(c *config) { c.jobQueue = n } }

func WithLogger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }

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

// Writer is the single store writer.
type Writer struct {
	store trace.SpanStore
	clk   clock.Clock
	cfg   config
	log   *slog.Logger

	wake    chan struct{}
	jobs    chan *jobRequest
	closing chan struct{}
	stopped chan struct{}
	started atomic.Bool

	mu           sync.Mutex
	pending      []*entry
	pendingSlots int
	usedSlots    int
	usedBytes    int64
	counts       map[countKey]int64
	closed       bool

	stats counters
}

// New returns a Writer over store.
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
