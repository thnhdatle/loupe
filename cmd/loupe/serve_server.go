// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/thnhdatle/loupe/internal/adapter/adminhttp"
	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
	"github.com/thnhdatle/loupe/internal/platform/metrics"
)

// server is the assembled Loupe process: the store, the writer, the use cases and the listeners.
type server struct {
	cfg *config.Config
	log *slog.Logger
	clk clock.Clock

	store     *sqlite.Store
	writer    *writer.Writer
	planner   *retention.Planner
	admin     *admin.Service
	metrics   *metrics.Metrics
	limiter   *ingest.InflightLimiter
	otlp      *otlp.Server
	uiServer  *http.Server
	admServer *http.Server

	grpcLn, httpLn, uiLn, adminLn net.Listener

	ready atomic.Bool
}

// newServer opens the store, builds the use cases and binds the listeners.
func newServer(ctx context.Context, cfg *config.Config, log *slog.Logger, bi metrics.BuildInfo) (*server, error) {
	s := &server{cfg: cfg, log: log, clk: clock.System{}}
	for _, w := range cfg.Warnings() {
		log.Warn("configuration", "component", "config", "error", w)
	}
	apiTokens, recvTokens, err := s.loadTokens()
	if err != nil {
		return nil, err
	}
	table, err := pricingfile.Load(cfg.Pricing.File)
	if err != nil {
		return nil, fmt.Errorf("load pricing: %w", err)
	}
	calc, err := cost.NewCalculator(table)
	if err != nil {
		return nil, fmt.Errorf("build cost calculator: %w", err)
	}
	s.store, err = sqlite.Open(ctx, cfg.Storage.Path, sqlite.WithClock(s.clk),
		sqlite.WithBackupBeforeMigrate(cfg.Storage.BackupBeforeMigrate),
		sqlite.WithContentMaxBytes(int(cfg.Content.MaxBytes)))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := s.build(calc, apiTokens, recvTokens, bi); err != nil {
		_ = s.store.Close()
		return nil, err
	}
	if err := s.listen(ctx); err != nil {
		s.closeListeners()
		_ = s.store.Close()
		return nil, err
	}
	return s, nil
}

func (s *server) loadTokens() (api, recv []string, err error) {
	created, err := ensureAPIToken(s.cfg)
	if err != nil {
		return nil, nil, err
	}
	if created {
		s.log.Info("generated api token", "component", "config", "path", s.cfg.API.TokenFile)
	}
	if api, err = readTokens(s.cfg.API.TokenFile); err != nil {
		return nil, nil, err
	}
	if recv, err = readTokens(s.cfg.Receiver.TokensFile); err != nil {
		return nil, nil, err
	}
	return api, recv, nil
}

func (s *server) build(calc *cost.Calculator, apiTokens, recvTokens []string, bi metrics.BuildInfo) error {
	cfg := s.cfg
	var err error
	if s.limiter, err = ingest.NewInflightLimiter(cfg.Receiver.MaxInflightBytes.Bytes()); err != nil {
		return fmt.Errorf("inflight limiter: %w", err)
	}
	s.metrics, err = metrics.New(metrics.Sources{
		Writer:        func() writer.Stats { return s.writer.Stats() },
		Retention:     func() retention.Stats { return s.planner.Stats() },
		InflightBytes: s.limiter.InUse,
	}, bi)
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	if s.writer, err = writer.New(observedStore{SpanStore: s.store, m: s.metrics}, s.clk,
		writer.WithBatchSpans(cfg.Writer.BatchSpans),
		writer.WithBatchInterval(cfg.Writer.BatchInterval.Std()),
		writer.WithQueueLimits(cfg.Ingest.QueueSpans, cfg.Ingest.QueueBytes.Bytes()),
		writer.WithShutdownTimeout(cfg.ShutdownTimeout.Std()),
		writer.WithLogger(s.log)); err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	ing, err := s.newIngestor(calc)
	if err != nil {
		return err
	}
	if s.otlp, err = s.newOTLP(ing, recvTokens); err != nil {
		return err
	}
	if s.planner, err = retention.New(s.store, s.writer, s.clk, cfg.RetentionConfig(),
		retention.WithFreeDisk(s.store.FreeDisk), retention.WithLogger(s.log)); err != nil {
		return fmt.Errorf("retention: %w", err)
	}
	s.admin = admin.New(s.store.Admin(), s.store, s.writer, s.clk)
	return s.buildHTTP(apiTokens)
}

func (s *server) newIngestor(calc *cost.Calculator) (*ingest.Ingestor, error) {
	cfg := s.cfg
	mode, err := content.ParseMode(cfg.Content.Capture)
	if err != nil {
		return nil, fmt.Errorf("content mode: %w", err)
	}
	red, err := content.NewRedactor(cfg.Content.RedactPresets(), cfg.Content.RedactRules())
	if err != nil {
		return nil, fmt.Errorf("content redaction: %w", err)
	}
	overflow, err := ingest.ParseOverflow(cfg.Ingest.Overflow)
	if err != nil {
		return nil, fmt.Errorf("ingest overflow: %w", err)
	}
	ing, err := ingest.New(metrics.WrapQueue(s.writer, s.metrics), calc, s.clk,
		ingest.WithContentPolicy(content.Policy{Mode: mode, MaxBytes: int(cfg.Content.MaxBytes), Redactor: red}),
		ingest.WithMaxAttributeBytes(int(cfg.Receiver.MaxAttributeBytes)),
		ingest.WithOverflow(overflow),
		ingest.WithSyncAck(cfg.Ingest.SyncAck),
		ingest.WithUnpricedHook(s.metrics.AddUnpriced),
		ingest.WithLogger(s.log))
	if err != nil {
		return nil, fmt.Errorf("ingestor: %w", err)
	}
	return ing, nil
}

func (s *server) newOTLP(ing *ingest.Ingestor, tokens []string) (*otlp.Server, error) {
	cfg := s.cfg
	oc := otlp.DefaultConfig()
	oc.MaxRequestBytes = cfg.Receiver.MaxRequestBytes.Bytes()
	oc.MaxSpansPerRequest = cfg.Receiver.MaxSpansPerRequest
	oc.Tokens = tokens
	oc.ShutdownTimeout = cfg.ShutdownTimeout.Std()
	oc.Logger = s.log
	var err error
	if oc.GRPCTLS, err = loadTLS(cfg.Receiver.GRPC.TLS); err != nil {
		return nil, fmt.Errorf("receiver.grpc.tls: %w", err)
	}
	if oc.HTTPTLS, err = loadTLS(cfg.Receiver.HTTP.TLS); err != nil {
		return nil, fmt.Errorf("receiver.http.tls: %w", err)
	}
	srv, err := otlp.NewServer(oc, ing, s.limiter)
	if err != nil {
		return nil, fmt.Errorf("otlp receivers: %w", err)
	}
	return srv, nil
}

func (s *server) buildHTTP(apiTokens []string) error {
	cfg := s.cfg
	observe := func(endpoint string, _ int, d time.Duration) { s.metrics.ObserveQuery(endpoint, d) }
	q := queries.New(queries.Readers{
		Traces: s.store, Sessions: s.store, Rollups: s.store, Stats: s.store,
	}, s.clk, queries.WithGauges(gauges{w: s.writer, l: s.limiter}))
	dash, err := s.newDashboard(q, apiTokens)
	if err != nil {
		return err
	}
	api, err := httpapi.NewHandler(q, s.admin, httpapi.Options{
		Tokens:               apiTokens,
		Exporter:             s.store,
		AllowUnauthenticated: len(apiTokens) == 0,
		SessionAuth:          dash.SessionAuth,
		Version:              httpapi.VersionInfo{Version: version, Commit: commit, BuildDate: buildDate},
		Clock:                s.clk, Logger: s.log, AccessLog: cfg.Log.AccessLog, Observe: observe,
	})
	if err != nil {
		return fmt.Errorf("api handler: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", api)
	if len(apiTokens) > 0 || isLoopback(cfg.UI.Listen) {
		adm, aerr := adminhttp.NewHandler(s.admin, adminhttp.Options{
			Tokens:               apiTokens,
			AllowUnauthenticated: len(apiTokens) == 0,
			StorageDir:           filepath.Dir(s.store.Path()),
			Calculator:           s.calculator,
			Clock:                s.clk, Logger: s.log, AccessLog: cfg.Log.AccessLog, Observe: observe,
		})
		if aerr != nil {
			return fmt.Errorf("admin api handler: %w", aerr)
		}
		mux.Handle("/v1/admin/", adm)
	} else {
		s.log.Warn("admin api disabled: it needs api.token_file on a non-loopback ui.listen", "component", "config")
	}
	mux.Handle("/", dash)
	tlsCfg, err := loadTLS(cfg.UI.TLS)
	if err != nil {
		return fmt.Errorf("ui.tls: %w", err)
	}
	s.uiServer = httpmw.NewServer(cfg.UI.Listen, mux, tlsCfg)
	s.admServer = httpmw.NewServer(cfg.Admin.Listen, s.adminMux(), nil)
	return nil
}

func (s *server) calculator() (*cost.Calculator, error) {
	table, err := pricingfile.Load(s.cfg.Pricing.File)
	if err != nil {
		return nil, fmt.Errorf("load pricing: %w", err)
	}
	c, err := cost.NewCalculator(table)
	if err != nil {
		return nil, fmt.Errorf("build cost calculator: %w", err)
	}
	return c, nil
}

func (s *server) listen(ctx context.Context) error {
	var err error
	lc := net.ListenConfig{}
	bind := func(dst *net.Listener, addr string) {
		if err != nil {
			return
		}
		*dst, err = lc.Listen(ctx, "tcp", addr)
		if err != nil {
			err = fmt.Errorf("listen %s: %w", addr, err)
		}
	}
	bind(&s.grpcLn, s.cfg.Receiver.GRPC.Listen)
	bind(&s.httpLn, s.cfg.Receiver.HTTP.Listen)
	bind(&s.uiLn, s.cfg.UI.Listen)
	bind(&s.adminLn, s.cfg.Admin.Listen)
	return err
}

func (s *server) closeListeners() {
	for _, ln := range []net.Listener{s.grpcLn, s.httpLn, s.uiLn, s.adminLn} {
		if ln != nil {
			_ = ln.Close()
		}
	}
}

func (s *server) close() error {
	s.closeListeners()
	if err := s.store.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

type gauges struct {
	w *writer.Writer
	l *ingest.InflightLimiter
}

func (g gauges) QueueDepth() (spans, bytes int64) {
	st := g.w.Stats()
	return int64(st.QueuedSlots), st.QueuedBytes
}

func (g gauges) InflightBytes() int64 { return g.l.InUse() }

var errNotReady = errors.New("not ready")
