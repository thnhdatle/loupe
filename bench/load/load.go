// SPDX-License-Identifier: Apache-2.0

package load

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/bench/gen"
)

// Overflow modes; they only change how partial-success rejections are classified.
const (
	ModeReject = "reject"
	ModeDrop   = "drop"
)

// Config configures one run.
type Config struct {
	HTTPURL  string // base URL of the OTLP/HTTP receiver, e.g. http://127.0.0.1:4318
	GRPCAddr string // host:port of the OTLP/gRPC receiver
	// Rate is the target arrival rate in spans per second.
	Rate     float64
	Duration time.Duration
	Batch    int // spans per request (default 512)
	Seed     uint64
	RunID    uint32
	// Mode is ModeReject (default) or ModeDrop.
	Mode string
	// Token is an optional bearer token.
	Token string
	// GRPCFraction is the share of requests sent over gRPC, chosen deterministically.
	GRPCFraction float64
	// MaxInflight caps concurrent requests (default 1024); further scheduled
	// requests are counted as skipped_backlog.
	MaxInflight int
	// RequestTimeout bounds one request (default 30s).
	RequestTimeout time.Duration
	// DrainTimeout bounds the wait for in-flight requests after Duration (default 30s).
	DrainTimeout time.Duration
	// Progress receives a one-line progress report every ProgressEvery (default 10s).
	Progress      io.Writer
	ProgressEvery time.Duration
}

// Summary is the result of a run; it is the JSON written by the CLI.
type Summary struct {
	Mode            string  `json:"mode"`
	TargetRate      float64 `json:"target_rate_spans_per_s"`
	DurationSeconds float64 `json:"duration_seconds"`
	Batch           int     `json:"batch"`

	// Requests counts requests launched; RequestsHTTP and RequestsGRPC split them.
	Requests     int64 `json:"requests"`
	RequestsHTTP int64 `json:"requests_http"`
	RequestsGRPC int64 `json:"requests_grpc"`
	// LaunchedSpans is the number of unique spans in launched requests.
	LaunchedSpans int64 `json:"launched_spans"`
	// SkippedBacklog and SkippedBacklogSpans are scheduled requests never sent
	// because MaxInflight was reached.
	SkippedBacklog      int64 `json:"skipped_backlog"`
	SkippedBacklogSpans int64 `json:"skipped_backlog_spans"`

	// SentSpans counts spans of requests that got any response.
	SentSpans     int64 `json:"sent_spans"`
	AcceptedSpans int64 `json:"accepted_spans"`
	RejectedSpans int64 `json:"rejected_spans"`
	DroppedSpans  int64 `json:"dropped_spans"`
	// OtherErrorSpans are spans of responses that were neither success nor overload.
	OtherErrorSpans int64            `json:"other_error_spans"`
	OtherErrors     map[string]int64 `json:"other_errors"`

	TransportErrors     int64 `json:"transport_errors"`
	TransportErrorSpans int64 `json:"transport_error_spans"`
	// TransportErrorSeqs are the request sequence numbers (gen.PayloadSeq) of
	// requests without a response, in completion order, at most
	// maxTransportSeqs; the server may still have stored their spans, so a
	// caller that compares sent with stored can attribute them.
	TransportErrorSeqs []uint32 `json:"transport_error_seqs,omitempty"`

	GRPCRejections                 int64 `json:"grpc_rejections"`
	GRPCRejectionsWithoutRetryInfo int64 `json:"grpc_rejections_without_retryinfo"`
	HTTP429WithoutRetryAfter       int64 `json:"http_429_without_retry_after"`

	LatencyP50ms float64 `json:"latency_p50_ms"`
	LatencyP95ms float64 `json:"latency_p95_ms"`
	LatencyP99ms float64 `json:"latency_p99_ms"`

	// AchievedRate is LaunchedSpans per second of scheduled run time.
	AchievedRate float64 `json:"achieved_send_rate_spans_per_s"`
	// Unfinished counts requests still in flight when the drain timeout expired.
	Unfinished int64 `json:"unfinished_requests"`
}

type runner struct {
	cfg  Config
	gen  *gen.Payload
	hc   *http.Client
	tr   *http.Transport
	gc   *grpc.ClientConn
	gcli coltrace.TraceServiceClient
	url  string

	launched, reqHTTP, reqGRPC, launchedSpans   atomic.Int64
	skipped, skippedSpans                       atomic.Int64
	sent, accepted, rejected, dropped           atomic.Int64
	otherSpans, transport, transportSpans       atomic.Int64
	grpcRej, grpcRejNoInfo, http429NoRetryAfter atomic.Int64
	done                                        atomic.Int64

	mu        sync.Mutex
	other     map[string]int64
	latency   []float64
	transSeqs []uint32
}

// maxTransportSeqs caps Summary.TransportErrorSeqs.
const maxTransportSeqs = 100_000

// Run drives the load until Duration elapses or ctx is cancelled, waits for
// in-flight requests, and returns the summary. Requests are never retried.
func Run(ctx context.Context, cfg Config) (Summary, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return Summary{}, err
	}
	r := &runner{cfg: cfg, gen: gen.NewPayload(cfg.Seed, cfg.RunID), other: map[string]int64{}}
	r.gen.SpansPerRequest = cfg.Batch
	if cfg.HTTPURL != "" {
		r.url = strings.TrimRight(cfg.HTTPURL, "/") + "/v1/traces"
		r.tr = &http.Transport{
			MaxIdleConns: cfg.MaxInflight, MaxIdleConnsPerHost: cfg.MaxInflight,
			IdleConnTimeout: 90 * time.Second,
		}
		r.hc = &http.Client{Transport: r.tr}
		defer r.tr.CloseIdleConnections()
	}
	if cfg.GRPCAddr != "" {
		r.gc, err = grpc.NewClient(cfg.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return Summary{}, fmt.Errorf("load: dial grpc: %w", err)
		}
		defer func() { _ = r.gc.Close() }()
		r.gcli = coltrace.NewTraceServiceClient(r.gc)
	}
	return r.run(ctx), nil
}

func normalize(c Config) (Config, error) {
	if c.Batch <= 0 {
		c.Batch = gen.PayloadSpans
	}
	if c.MaxInflight <= 0 {
		c.MaxInflight = 1024
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 30 * time.Second
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = 30 * time.Second
	}
	if c.ProgressEvery <= 0 {
		c.ProgressEvery = 10 * time.Second
	}
	switch c.Mode {
	case "":
		c.Mode = ModeReject
	case ModeReject, ModeDrop:
	default:
		return c, fmt.Errorf("load: unknown mode %q", c.Mode)
	}
	return c, validate(c)
}

func validate(c Config) error {
	switch {
	case c.Rate <= 0:
		return errors.New("load: rate must be positive")
	case c.Duration <= 0:
		return errors.New("load: duration must be positive")
	case c.GRPCFraction < 0 || c.GRPCFraction > 1:
		return errors.New("load: grpc fraction must be in [0,1]")
	case c.GRPCFraction < 1 && c.HTTPURL == "":
		return errors.New("load: http url is required unless grpc fraction is 1")
	case c.GRPCFraction > 0 && c.GRPCAddr == "":
		return errors.New("load: grpc address is required when grpc fraction > 0")
	}
	return nil
}

// useGRPC reports whether request i (0-based) goes over gRPC: with fraction f,
// exactly floor((i+1)*f) - floor(i*f) of each request, e.g. every 10th for 0.1.
func useGRPC(i int64, f float64) bool {
	return math.Floor(float64(i+1)*f+1e-9) > math.Floor(float64(i)*f+1e-9)
}

func (r *runner) run(ctx context.Context) Summary {
	cfg := r.cfg
	period := time.Duration(float64(cfg.Batch) / cfg.Rate * float64(time.Second))
	if period <= 0 {
		period = time.Nanosecond
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.MaxInflight)
	reqCtx := context.WithoutCancel(ctx)
	start := time.Now()

	stopProgress := make(chan struct{})
	var pwg sync.WaitGroup
	if cfg.Progress != nil {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			r.progressLoop(stopProgress, start)
		}()
	}

	timer := time.NewTimer(0)
	defer timer.Stop()
	<-timer.C
schedule:
	for i := int64(0); ; i++ {
		at := time.Duration(i) * period
		if at >= cfg.Duration {
			break
		}
		if wait := at - time.Since(start); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				break schedule
			case <-timer.C:
			}
		} else if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		default:
			r.skipped.Add(1)
			r.skippedSpans.Add(int64(cfg.Batch))
			continue
		}
		wg.Add(1)
		viaGRPC := useGRPC(i, cfg.GRPCFraction)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.send(reqCtx, uint32(i), viaGRPC) //nolint:gosec // sequence numbers wrap by design.
		}()
	}
	elapsed := time.Since(start)
	if elapsed > cfg.Duration {
		elapsed = cfg.Duration
	}

	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(cfg.DrainTimeout):
	}
	close(stopProgress)
	pwg.Wait()
	s := r.summary(elapsed)
	s.Unfinished = r.launched.Load() - r.done.Load()
	return s
}

func (r *runner) progressLoop(stop <-chan struct{}, start time.Time) {
	t := time.NewTicker(r.cfg.ProgressEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			r.progress(r.cfg.Progress, time.Since(start))
		}
	}
}

func (r *runner) progress(w io.Writer, el time.Duration) {
	_, _ = fmt.Fprintf(w,
		"loadgen t=%s requests=%d launched_spans=%d sent=%d accepted=%d rejected=%d dropped=%d "+
			"transport_err=%d skipped=%d\n",
		el.Truncate(time.Second), r.launched.Load(), r.launchedSpans.Load(), r.sent.Load(), r.accepted.Load(),
		r.rejected.Load(), r.dropped.Load(), r.transport.Load(), r.skipped.Load())
}

func (r *runner) send(ctx context.Context, seq uint32, viaGRPC bool) {
	n := int64(r.cfg.Batch)
	r.launched.Add(1)
	r.launchedSpans.Add(n)
	defer r.done.Add(1)
	req := r.gen.Request(seq, time.Now())
	ctx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	t0 := time.Now()
	var responded bool
	if viaGRPC {
		r.reqGRPC.Add(1)
		responded = r.sendGRPC(ctx, req, n)
	} else {
		r.reqHTTP.Add(1)
		responded = r.sendHTTP(ctx, req, n)
	}
	if responded {
		d := float64(time.Since(t0)) / float64(time.Millisecond)
		r.mu.Lock()
		r.latency = append(r.latency, d)
		r.mu.Unlock()
	} else {
		r.transport.Add(1)
		r.transportSpans.Add(n)
		r.mu.Lock()
		if len(r.transSeqs) < maxTransportSeqs {
			r.transSeqs = append(r.transSeqs, seq)
		}
		r.mu.Unlock()
	}
}

// success classifies a successful response with its partial-success rejection count.
func (r *runner) success(n, rejected int64) {
	r.sent.Add(n)
	rejected = max(0, min(rejected, n))
	r.accepted.Add(n - rejected)
	if r.cfg.Mode == ModeDrop {
		r.dropped.Add(rejected)
	} else {
		r.rejected.Add(rejected)
	}
}

func (r *runner) otherError(key string, n int64) {
	r.sent.Add(n)
	r.otherSpans.Add(n)
	r.mu.Lock()
	r.other[key]++
	r.mu.Unlock()
}

// sendHTTP returns false when no response was received.
func (r *runner) sendHTTP(ctx context.Context, msg *coltrace.ExportTraceServiceRequest, n int64) bool {
	body, err := proto.Marshal(msg)
	if err != nil {
		return false
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	hr.Header.Set("Content-Type", "application/x-protobuf")
	if r.cfg.Token != "" {
		hr.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	}
	resp, err := r.hc.Do(hr)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var out coltrace.ExportTraceServiceResponse
		if err := proto.Unmarshal(data, &out); err != nil {
			r.otherError("http:200 undecodable body", n)
			return true
		}
		r.success(n, out.GetPartialSuccess().GetRejectedSpans())
	case http.StatusTooManyRequests:
		r.sent.Add(n)
		r.rejected.Add(n)
		if resp.Header.Get("Retry-After") == "" {
			r.http429NoRetryAfter.Add(1)
		}
	default:
		r.otherError("http:"+resp.Status, n)
	}
	return true
}

// sendGRPC returns false when no response was received. UNAVAILABLE,
// DEADLINE_EXCEEDED and CANCELED count as transport errors.
func (r *runner) sendGRPC(ctx context.Context, msg *coltrace.ExportTraceServiceRequest, n int64) bool {
	if r.cfg.Token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+r.cfg.Token)
	}
	out, err := r.gcli.Export(ctx, msg)
	if err == nil {
		r.success(n, out.GetPartialSuccess().GetRejectedSpans())
		return true
	}
	st, _ := status.FromError(err)
	switch st.Code() { //nolint:exhaustive // every other code is an other error.
	case codes.ResourceExhausted:
		r.sent.Add(n)
		r.rejected.Add(n)
		r.grpcRej.Add(1)
		if !slices.ContainsFunc(st.Details(), func(d any) bool { _, ok := d.(*errdetails.RetryInfo); return ok }) {
			r.grpcRejNoInfo.Add(1)
		}
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return false
	default:
		r.otherError("grpc:"+st.Code().String(), n)
	}
	return true
}

func (r *runner) summary(elapsed time.Duration) Summary {
	r.mu.Lock()
	lat := slices.Clone(r.latency)
	seqs := slices.Clone(r.transSeqs)
	other := make(map[string]int64, len(r.other))
	for k, v := range r.other {
		other[k] = v
	}
	r.mu.Unlock()
	slices.Sort(lat)
	q := func(p float64) float64 {
		if len(lat) == 0 {
			return 0
		}
		return lat[max(0, int(math.Ceil(p*float64(len(lat))))-1)]
	}
	s := Summary{
		Mode: r.cfg.Mode, TargetRate: r.cfg.Rate, DurationSeconds: elapsed.Seconds(), Batch: r.cfg.Batch,
		Requests: r.launched.Load(), RequestsHTTP: r.reqHTTP.Load(), RequestsGRPC: r.reqGRPC.Load(),
		LaunchedSpans: r.launchedSpans.Load(), SkippedBacklog: r.skipped.Load(), SkippedBacklogSpans: r.skippedSpans.Load(),
		SentSpans: r.sent.Load(), AcceptedSpans: r.accepted.Load(), RejectedSpans: r.rejected.Load(),
		DroppedSpans: r.dropped.Load(), OtherErrorSpans: r.otherSpans.Load(), OtherErrors: other,
		TransportErrors: r.transport.Load(), TransportErrorSpans: r.transportSpans.Load(), TransportErrorSeqs: seqs,
		GRPCRejections: r.grpcRej.Load(), GRPCRejectionsWithoutRetryInfo: r.grpcRejNoInfo.Load(),
		HTTP429WithoutRetryAfter: r.http429NoRetryAfter.Load(),
		LatencyP50ms:             q(0.50), LatencyP95ms: q(0.95), LatencyP99ms: q(0.99),
	}
	if elapsed > 0 {
		s.AchievedRate = float64(s.LaunchedSpans) / elapsed.Seconds()
	}
	return s
}
