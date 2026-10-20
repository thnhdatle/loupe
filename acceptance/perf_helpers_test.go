//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite".

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
	"github.com/thnhdatle/loupe/bench/load"
)

// Shared machinery of the ingest performance criteria LPE-02 and LPE-03: one
// "perf run" starts a fresh native server on a fresh database, samples its RSS
// every second, drives bench/load against it, waits for the
// drain, reads /v1/ingest/stats and the Overview banner, stops the server
// gracefully and counts the stored spans in the database file.

const (
	perfRSSEvery     = time.Second
	perfDrainTimeout = 10 * time.Minute
	perfDrainPoll    = 250 * time.Millisecond
	// perfStableFor is how long the stats must stay unchanged with an empty
	// queue before the drain counts as finished: the writer folds pending
	// counters into ingest_stats on its next tick (writer.batch_interval, 200 ms).
	perfStableFor = time.Second
	perfHTTPWait  = 30 * time.Second
	// perfResultsFile collects one JSON line per perf run, for bench/RESULTS.md.
	perfResultsFile = "perf-results.jsonl"
)

// perfRSS samples the resident set size of a process every perfRSSEvery.
type perfRSS struct {
	pid     int
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	peakKiB int64
	samples int
	lastErr error
}

func perfStartRSS(pid int) *perfRSS {
	r := &perfRSS{pid: pid, stop: make(chan struct{}), done: make(chan struct{})}
	go r.loop()
	return r
}

func (r *perfRSS) loop() {
	defer close(r.done)
	t := time.NewTicker(perfRSSEvery)
	defer t.Stop()
	for {
		r.sample()
		select {
		case <-r.stop:
			return
		case <-t.C:
		}
	}
}

func (r *perfRSS) sample() {
	kib, err := perfRSSKiB(r.pid)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.lastErr = err
		return
	}
	r.samples++
	r.peakKiB = max(r.peakKiB, kib)
}

// Stop ends sampling and returns the peak in MiB and the number of samples.
func (r *perfRSS) Stop() (peakMiB float64, samples int, err error) {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return math.Round(float64(r.peakKiB)/1024*10) / 10, r.samples, r.lastErr
}

// perfRSSKiB reads the RSS of pid with ps (KiB on macOS and Linux).
func perfRSSKiB(pid int) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("ps rss: %w", err)
	}
	kib, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ps rss %q: %w", out, err)
	}
	return kib, nil
}

// perfStats is the part of GET /v1/ingest/stats the perf criteria compare.
type perfStats struct {
	Received      int64            `json:"received"`
	Stored        int64            `json:"stored"`
	Duplicates    int64            `json:"duplicates"`
	Rejected      map[string]int64 `json:"rejected"`
	Dropped       map[string]int64 `json:"dropped"`
	QueueSpans    int64            `json:"queue_spans"`
	QueueBytes    int64            `json:"queue_bytes"`
	InflightBytes int64            `json:"inflight_bytes"`
}

func perfSum(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

func (s perfStats) rejected() int64 { return perfSum(s.Rejected) }
func (s perfStats) dropped() int64  { return perfSum(s.Dropped) }

func (s perfStats) idle() bool { return s.QueueSpans == 0 && s.QueueBytes == 0 && s.InflightBytes == 0 }

func (s perfStats) sameCounters(o perfStats) bool {
	return s.Received == o.Received && s.Stored == o.Stored && s.Duplicates == o.Duplicates &&
		maps.Equal(s.Rejected, o.Rejected) && maps.Equal(s.Dropped, o.Dropped)
}

func perfGet(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s: %s", u, resp.Status, bytes.TrimSpace(body))
	}
	return body, nil
}

func perfGetStats(ctx context.Context, p *native.Proc) (perfStats, error) {
	body, err := perfGet(ctx, &http.Client{Timeout: perfHTTPWait}, p.UIURL()+"/v1/ingest/stats")
	if err != nil {
		return perfStats{}, err
	}
	var st perfStats
	if err := json.Unmarshal(body, &st); err != nil {
		return perfStats{}, fmt.Errorf("decode ingest stats: %w", err)
	}
	return st, nil
}

// perfDrain waits until the queue and the in-flight bytes are empty and the
// counters have not changed for perfStableFor, and returns the final stats.
func perfDrain(ctx context.Context, p *native.Proc) (perfStats, time.Duration, error) {
	start := time.Now()
	deadline := start.Add(perfDrainTimeout)
	var (
		last        perfStats
		stableSince time.Time
	)
	for time.Now().Before(deadline) {
		st, err := perfGetStats(ctx, p)
		if err != nil {
			return st, time.Since(start), err
		}
		switch {
		case !st.idle() || !st.sameCounters(last):
			stableSince = time.Time{}
		case stableSince.IsZero():
			stableSince = time.Now()
		case time.Since(stableSince) >= perfStableFor:
			return st, time.Since(start), nil
		}
		last = st
		select {
		case <-ctx.Done():
			return st, time.Since(start), ctx.Err()
		case <-time.After(perfDrainPoll):
		}
	}
	return last, time.Since(start), fmt.Errorf("queue not drained within %s (queue_spans=%d)", perfDrainTimeout,
		last.QueueSpans)
}

// perfBanner is what the Overview banner shows: spans rejected and dropped in the range.
type perfBanner struct {
	Shown    bool  `json:"shown"`
	Rejected int64 `json:"rejected"`
	Dropped  int64 `json:"dropped"`
}

var perfBannerRE = regexp.MustCompile(
	`Spans were not stored:</strong>\s*([0-9,]+)\s+spans rejected and\s+([0-9,]+)\s+dropped`)

// perfGetBanner renders the Overview page for [from, to) and parses its
// rejected/dropped banner (absent when nothing was rejected or dropped).
func perfGetBanner(ctx context.Context, p *native.Proc, from, to time.Time) (perfBanner, error) {
	q := url.Values{}
	q.Set("from", from.UTC().Format(time.RFC3339))
	q.Set("to", to.UTC().Format(time.RFC3339))
	body, err := perfGet(ctx, &http.Client{Timeout: perfHTTPWait}, p.UIURL()+"/?"+q.Encode())
	if err != nil {
		return perfBanner{}, err
	}
	if !bytes.Contains(body, []byte("<section class=\"tiles\"")) {
		return perfBanner{}, errors.New("overview page without tiles: not the Overview")
	}
	m := perfBannerRE.FindSubmatch(body)
	if m == nil {
		if bytes.Contains(body, []byte("Spans were not stored")) {
			return perfBanner{}, errors.New("overview banner present but not parseable")
		}
		return perfBanner{}, nil
	}
	num := func(b []byte) int64 {
		n, _ := strconv.ParseInt(strings.ReplaceAll(string(b), ",", ""), 10, 64)
		return n
	}
	return perfBanner{Shown: true, Rejected: num(m[1]), Dropped: num(m[2])}, nil
}

// perfDBCounts counts the spans of the database after the server stopped, and
// how many of them belong to the given request sequence numbers of run (the
// trace id carries run id and sequence number, gen.Payload.traceID).
func perfDBCounts(ctx context.Context, dbPath string, run uint32, seqs []uint32) (total, ofSeqs int64, err error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM spans`).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("count spans: %w", err)
	}
	for _, seq := range seqs {
		lo := make([]byte, 10)
		lo[0], lo[1] = 0x4c, 0x02
		binary.BigEndian.PutUint32(lo[2:], run)
		binary.BigEndian.PutUint32(lo[6:], seq)
		hi := append(bytes.Clone(lo), bytes.Repeat([]byte{0xff}, 6)...)
		var n int64
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM spans WHERE trace_id >= ? AND trace_id <= ?`, lo, hi).Scan(&n); err != nil {
			return 0, 0, fmt.Errorf("count spans of seq %d: %w", seq, err)
		}
		ofSeqs += n
	}
	return total, ofSeqs, nil
}

// perfDBBytes is the size of the database files (main, WAL, SHM).
func perfDBBytes(dbPath string) int64 {
	var n int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(dbPath + suffix); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// perfDataRoot is ~/.cache/rayld/loupe/acceptance-perf: the runs' databases
// (1-3 GB each) live there and are deleted after every run.
func perfDataRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "rayld", "loupe", "acceptance-perf")
	return dir, os.MkdirAll(dir, 0o750)
}

func perfRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return criteria.FindRoot(wd)
}

// perfConfig describes one perf run.
type perfConfig struct {
	Name         string
	Rate         float64 // spans per second
	Duration     time.Duration
	Mode         string // ingest.overflow and the load generator's classification
	GRPCFraction float64
	RunID        uint32
	Seed         uint64
}

// perfResult is the outcome of one perf run.
type perfResult struct {
	Name         string       `json:"name"`
	Load         load.Summary `json:"load"`
	PeakRSSMiB   float64      `json:"peak_rss_mib"`
	RSSSamples   int          `json:"rss_samples"`
	Stats        perfStats    `json:"ingest_stats"`
	Banner       perfBanner   `json:"overview_banner"`
	DBSpans      int64        `json:"db_spans"`
	DBBytes      int64        `json:"db_bytes"`
	DrainSeconds float64      `json:"drain_seconds"`
	// TransportStored counts DB spans of requests that got no response; they
	// are not "sent" and are subtracted before comparing.
	TransportStored int64  `json:"transport_stored_spans"`
	StopError       string `json:"stop_error,omitempty"`
}

// storedOfSent is the number of stored spans that belong to requests that got
// a response.
func (r perfResult) storedOfSent() int64 { return r.DBSpans - r.TransportStored }

// sentRate is sent spans per second of load.
func (r perfResult) sentRate() float64 {
	if r.Load.DurationSeconds <= 0 {
		return 0
	}
	return float64(r.Load.SentSpans) / r.Load.DurationSeconds
}

// perfRun runs one scenario on a fresh server and database. Server logs are
// copied to acceptance/logs/<id>-<name>-server.log when the run errs.
func perfRun(tb testing.TB, id string, c perfConfig) (res perfResult, err error) {
	tb.Helper()
	res.Name = c.Name
	root, err := perfDataRoot()
	if err != nil {
		return res, err
	}
	dir, err := os.MkdirTemp(root, id+"-"+c.Name+"-")
	if err != nil {
		return res, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Log level info, the configuration default: the native helper
	// defaults to debug, which logs one line per committed batch.
	p := native.Start(tb, native.Opts{Dir: dir, Env: map[string]string{
		"LOUPE_LOG_LEVEL":       "info",
		"LOUPE_INGEST_OVERFLOW": c.Mode,
		"LOUPE_CONTENT_CAPTURE": "off",
	}})
	defer func() {
		if err != nil {
			perfKeepLog(id, c.Name, p.LogPath)
		}
	}()
	rss := perfStartRSS(p.Pid())
	defer func() { _, _, _ = rss.Stop() }()

	ctx := context.Background()
	t0 := time.Now()
	fmt.Printf("%s %s: %.0f spans/s for %s, mode %s, grpc %.0f%%\n", id, c.Name, c.Rate, c.Duration, c.Mode,
		c.GRPCFraction*100)
	res.Load, err = load.Run(ctx, load.Config{
		HTTPURL:      p.HTTPURL(),
		GRPCAddr:     net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Ports.GRPC)),
		Rate:         c.Rate,
		Duration:     c.Duration,
		Seed:         c.Seed,
		RunID:        c.RunID,
		Mode:         c.Mode,
		GRPCFraction: c.GRPCFraction,
		Progress:     os.Stdout,
	})
	if err != nil {
		return res, fmt.Errorf("load: %w", err)
	}
	st, drain, err := perfDrain(ctx, p)
	res.Stats, res.DrainSeconds = st, math.Round(drain.Seconds()*10)/10
	if err != nil {
		return res, fmt.Errorf("drain: %w", err)
	}
	if res.Banner, err = perfGetBanner(ctx, p, t0.Add(-time.Hour), time.Now().Add(time.Hour)); err != nil {
		return res, fmt.Errorf("overview banner: %w", err)
	}
	if serr := p.Stop(); serr != nil {
		res.StopError = serr.Error()
	}
	res.PeakRSSMiB, res.RSSSamples, _ = rss.Stop()
	res.DBBytes = perfDBBytes(p.DBPath)
	res.DBSpans, res.TransportStored, err = perfDBCounts(ctx, p.DBPath, c.RunID, res.Load.TransportErrorSeqs)
	if err != nil {
		return res, err
	}
	if res.RSSSamples == 0 {
		return res, errors.New("no RSS sample taken")
	}
	fmt.Printf("%s %s: sent=%d accepted=%d rejected=%d dropped=%d transport_err=%d skipped=%d db_spans=%d "+
		"stats_stored=%d peak_rss=%.1fMiB p99=%.1fms drain=%.1fs\n", id, c.Name, res.Load.SentSpans,
		res.Load.AcceptedSpans, res.Load.RejectedSpans, res.Load.DroppedSpans, res.Load.TransportErrors,
		res.Load.SkippedBacklog, res.DBSpans, res.Stats.Stored, res.PeakRSSMiB, res.Load.LatencyP99ms,
		res.DrainSeconds)
	return res, nil
}

func perfKeepLog(id, name, path string) {
	root, err := perfRepoRoot()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(root, harness.LogDir, id+"-"+name+"-server.log"), data, 0o600)
}

// perfAppendResults appends one JSON line per record to
// acceptance/logs/perf-results.jsonl, the source of bench/RESULTS.md.
func perfAppendResults(id string, recs ...any) error {
	root, err := perfRepoRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, harness.LogDir, perfResultsFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	at := time.Now().UTC().Format(time.RFC3339)
	for _, r := range recs {
		line, err := json.Marshal(map[string]any{
			"criterion": id, "at": at, "ci": criteria.IsCI(os.Getenv("CI")),
			"result": r,
		})
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// perfNum reads a numeric threshold.
func perfNum(run *harness.Run, key string) float64 {
	switch v := run.Thresholds[key].(type) {
	case int:
		return float64(v)
	case float64:
		return v
	default:
		run.Fail(nil, "threshold %s: unexpected value %v (%T)", key, v, v)
		return 0
	}
}

// perfSummary is the compact per-run record kept in the measured map.
func perfSummary(r perfResult) map[string]any {
	return map[string]any{
		"target_spans_per_s":                r.Load.TargetRate,
		"sent_spans_per_s":                  math.Round(r.sentRate()),
		"duration_s":                        math.Round(r.Load.DurationSeconds*10) / 10,
		"sent":                              r.Load.SentSpans,
		"accepted":                          r.Load.AcceptedSpans,
		"rejected":                          r.Load.RejectedSpans,
		"dropped":                           r.Load.DroppedSpans,
		"other_error_spans":                 r.Load.OtherErrorSpans,
		"transport_errors":                  r.Load.TransportErrors,
		"transport_error_spans":             r.Load.TransportErrorSpans,
		"skipped_backlog":                   r.Load.SkippedBacklog,
		"unfinished_requests":               r.Load.Unfinished,
		"db_spans":                          r.DBSpans,
		"transport_stored":                  r.TransportStored,
		"stats_received":                    r.Stats.Received,
		"stats_stored":                      r.Stats.Stored,
		"stats_rejected":                    r.Stats.Rejected,
		"stats_dropped":                     r.Stats.Dropped,
		"banner":                            r.Banner,
		"peak_rss_mib":                      r.PeakRSSMiB,
		"rss_samples":                       r.RSSSamples,
		"latency_p50_ms":                    math.Round(r.Load.LatencyP50ms*10) / 10,
		"latency_p95_ms":                    math.Round(r.Load.LatencyP95ms*10) / 10,
		"latency_p99_ms":                    math.Round(r.Load.LatencyP99ms*10) / 10,
		"drain_s":                           r.DrainSeconds,
		"db_bytes":                          r.DBBytes,
		"grpc_rejections":                   r.Load.GRPCRejections,
		"grpc_rejections_without_retryinfo": r.Load.GRPCRejectionsWithoutRetryInfo,
		"http_429_without_retry_after":      r.Load.HTTP429WithoutRetryAfter,
		"stop_error":                        r.StopError,
	}
}
