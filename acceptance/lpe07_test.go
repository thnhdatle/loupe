//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // database/sql driver "sqlite".

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
)

// LPE-07 load shape: one OTLP/HTTP protobuf request of lpe07Batch spans every
// lpe07Tick, i.e. 1,000 spans/s at a constant (open-loop) rate. Every
// lpe07OldEvery-th request carries spans started lpe07OldAge ago: retention
// (retention.spans 24h, interval 1s) purges them while the load runs, so they
// are marked with their own trace-id prefix and excluded from the loss count.
const (
	lpe07Batch      = 50
	lpe07Tick       = 50 * time.Millisecond
	lpe07OldEvery   = 5
	lpe07OldAge     = 72 * time.Hour
	lpe07TraceSpans = 5 // one agent root and four llm children per trace

	lpe07QueueSpans  = 100_000 // ingest.queue_spans default, the non-sync_ack loss bound
	lpe07LoadMin     = 2 * time.Second
	lpe07LoadMax     = 6 * time.Second
	lpe07MaxInflight = 256
	lpe07ReadyWait   = 60 * time.Second

	lpe07KindFresh byte = 1
	lpe07KindOld   byte = 2
)

// lpe07Prefix returns the first four trace-id bytes of a run and kind.
func lpe07Prefix(run int, kind byte) []byte { return []byte{0x4c, 0x07, byte(run), kind} }

// lpe07Gen builds the spans of one run with unique trace and span ids.
type lpe07Gen struct {
	run          int
	traces, sids uint32
}

func lpe07KV(k string, v any) *commonpb.KeyValue {
	av := &commonpb.AnyValue{}
	switch x := v.(type) {
	case string:
		av.Value = &commonpb.AnyValue_StringValue{StringValue: x}
	case int:
		av.Value = &commonpb.AnyValue_IntValue{IntValue: int64(x)}
	}
	return &commonpb.KeyValue{Key: k, Value: av}
}

// request returns one export request of lpe07Batch spans of the given kind.
func (g *lpe07Gen) request(kind byte, now time.Time) *coltrace.ExportTraceServiceRequest {
	start := now
	if kind == lpe07KindOld {
		start = now.Add(-lpe07OldAge)
	}
	spans := make([]*tracepb.Span, 0, lpe07Batch)
	for len(spans) < lpe07Batch {
		g.traces++
		tid := make([]byte, 16)
		copy(tid, lpe07Prefix(g.run, kind))
		binary.BigEndian.PutUint32(tid[4:], g.traces)
		var root []byte
		for j := range lpe07TraceSpans {
			g.sids++
			sid := make([]byte, 8)
			sid[0] = 0x07
			sid[1] = byte(g.run)
			binary.BigEndian.PutUint32(sid[4:], g.sids)
			st := start.Add(time.Duration(j) * time.Millisecond)
			sp := &tracepb.Span{
				TraceId: tid, SpanId: sid,
				StartTimeUnixNano: uint64(st.UnixNano()),
				EndTimeUnixNano:   uint64(st.Add(80 * time.Millisecond).UnixNano()),
			}
			if j == 0 {
				root = sid
				sp.Name, sp.Kind = "invoke_agent planner", tracepb.Span_SPAN_KIND_INTERNAL
				sp.Attributes = []*commonpb.KeyValue{
					lpe07KV("gen_ai.operation.name", "invoke_agent"),
					lpe07KV("session.id", fmt.Sprintf("lpe07-%d-%d", g.run, g.traces%7)),
				}
			} else {
				sp.ParentSpanId = root
				sp.Name, sp.Kind = "chat gpt-4o", tracepb.Span_SPAN_KIND_CLIENT
				sp.Attributes = []*commonpb.KeyValue{
					lpe07KV("gen_ai.operation.name", "chat"),
					lpe07KV("gen_ai.system", "openai"),
					lpe07KV("gen_ai.request.model", "gpt-4o"),
					lpe07KV("gen_ai.usage.input_tokens", 100+j),
					lpe07KV("gen_ai.usage.output_tokens", 20+j),
				}
			}
			spans = append(spans, sp)
		}
	}
	res := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{lpe07KV("service.name", "lpe07")}}
	return &coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: res, ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
	}}}
}

// lpe07Load is the client side of one run: counters of what the server acknowledged.
type lpe07Load struct {
	acked, ackedOld, sent, failed, rejected, late atomic.Int64
	wg                                            sync.WaitGroup
	client                                        *http.Client
	url                                           string // OTLP/HTTP traces endpoint
}

// post sends one request; spans count as acknowledged only in a 200 response
// without a partial-success rejection.
func (l *lpe07Load) post(ctx context.Context, body []byte, old bool) {
	defer l.wg.Done()
	const n = lpe07Batch
	l.sent.Add(n)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url, bytes.NewReader(body))
	if err != nil {
		l.failed.Add(n)
		return
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := l.client.Do(req)
	if err != nil {
		l.failed.Add(n)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		l.failed.Add(n)
		return
	}
	var out coltrace.ExportTraceServiceResponse
	if err := proto.Unmarshal(data, &out); err != nil {
		l.failed.Add(n)
		return
	}
	rej := out.GetPartialSuccess().GetRejectedSpans()
	if rej > 0 {
		l.rejected.Add(rej)
		return
	}
	if old {
		l.ackedOld.Add(n)
		return
	}
	l.acked.Add(n)
}

// drive sends requests every lpe07Tick until ctx ends. Requests in flight
// are not cancelled: they end with the server's answer or its death.
func (l *lpe07Load) drive(ctx context.Context, g *lpe07Gen) {
	// Requests in flight are not cancelled by the end of the load: they end
	// with the server's answer or its death.
	reqCtx := context.WithoutCancel(ctx)
	sem := make(chan struct{}, lpe07MaxInflight)
	tick := time.NewTicker(lpe07Tick)
	defer tick.Stop()
	for i := 0; ; i++ {
		old := i%lpe07OldEvery == lpe07OldEvery-1
		kind := lpe07KindFresh
		if old {
			kind = lpe07KindOld
		}
		body, err := proto.Marshal(g.request(kind, time.Now()))
		if err != nil {
			panic(err) // generated message; cannot fail
		}
		select {
		case sem <- struct{}{}:
		default:
			l.late.Add(1)
			sem <- struct{}{}
		}
		l.wg.Add(1)
		go func() {
			defer func() { <-sem }()
			l.post(reqCtx, body, old)
		}()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// lpe07Admin calls the admin API with the API token.
type lpe07Admin struct {
	client *http.Client
	token  string
}

func (a lpe07Admin) do(method, url string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// lpe07Metrics sums the named counters of the admin /metrics endpoint.
func lpe07Metrics(client *http.Client, url string, names ...string) map[string]float64 {
	out := map[string]float64{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/metrics", nil)
	if err != nil {
		return out
	}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		for _, n := range names {
			if !strings.HasPrefix(line, n+" ") && !strings.HasPrefix(line, n+"{") {
				continue
			}
			f := strings.Fields(line)
			if v, err := strconv.ParseFloat(f[len(f)-1], 64); err == nil {
				out[n] += v
			}
		}
	}
	return out
}

// lpe07AwaitJob polls loupe_writer_jobs_total until it grows (up to two
// retention intervals) and reports whether it did.
func lpe07AwaitJob(client *http.Client, url string) bool {
	const name = "loupe_writer_jobs_total"
	base, ok := lpe07Metrics(client, url, name)[name]
	if !ok {
		return false
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if lpe07Metrics(client, url, name)[name] > base {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// lpe07Check runs PRAGMA integrity_check on a read-only connection and, for
// run ≥ 0, counts the fresh spans of run.
func lpe07Check(dbPath string, run int) (integrity string, stored int64, err error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	lines, err := lpe07Integrity(ctx, db)
	if err != nil || run < 0 {
		return lines, 0, err
	}
	lo := make([]byte, 16)
	copy(lo, lpe07Prefix(run, lpe07KindFresh))
	hi := make([]byte, 16)
	copy(hi, lpe07Prefix(run, lpe07KindFresh+1))
	q := "SELECT count(*) FROM spans WHERE trace_id >= ? AND trace_id < ?"
	if err := db.QueryRowContext(ctx, q, lo, hi).Scan(&stored); err != nil {
		return "", 0, fmt.Errorf("count spans: %w", err)
	}
	return lines, stored, nil
}

// lpe07Integrity returns the rows of PRAGMA integrity_check joined by "; ".
func lpe07Integrity(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return "", fmt.Errorf("integrity_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", fmt.Errorf("integrity_check: %w", err)
		}
		lines = append(lines, s)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("integrity_check: %w", err)
	}
	return strings.Join(lines, "; "), nil
}

// lpe07Tail returns the last n bytes of a log file.
func lpe07Tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// lpe07Result is the outcome of one run.
type lpe07Result struct {
	Run       int    `json:"run"`
	SyncAck   bool   `json:"sync_ack"`
	LoadMS    int64  `json:"load_ms"`
	Recompute string `json:"recompute,omitempty"` // job state at kill
	Sent      int64  `json:"sent"`                // all spans posted, fresh and purge-target
	Late      int64  `json:"late_ticks"`          // ticks delayed by lpe07MaxInflight
	Acked     int64  `json:"acked"`
	Stored    int64  `json:"stored"`
	Lost      int64  `json:"lost"`
	Failed    int64  `json:"failed"`
	Rejected  int64  `json:"rejected"`
	RetPasses int64  `json:"retention_passes"`
	Integrity string `json:"integrity"`
	// InRetention marks a kill issued right after a retention job completed.
	InRetention bool `json:"in_retention,omitempty"`
}

// lpe07Suite is the state of one LPE-07 scenario across its runs.
type lpe07Suite struct {
	t                *testing.T
	runs             int
	rnd              *rand.Rand
	base, dbPath     string
	tokenFile, token string
	client           *http.Client
	adm              lpe07Admin

	results                                     []lpe07Result
	integrityFail, restartFail, overQueue       int
	syncLost, maxLost, acked, stored, retPasses int64
	retDeleted                                  float64
	recomputeRunning, recomputeStarted          int
	retentionKills                              int
	problems                                    []string
}

func (s *lpe07Suite) problem(format string, args ...any) {
	s.problems = append(s.problems, fmt.Sprintf(format, args...))
}

// syncAck reports whether run i uses ingest.sync_ack (odd runs).
func (s *lpe07Suite) syncAck(i int) bool { return i%2 == 1 }

func (s *lpe07Suite) env(syncAck bool) map[string]string {
	return map[string]string{
		"LOUPE_STORAGE_PATH":           s.dbPath,
		"LOUPE_API_TOKEN_FILE":         s.tokenFile,
		"LOUPE_INGEST_SYNC_ACK":        strconv.FormatBool(syncAck),
		"LOUPE_RETENTION_SPANS":        "24h",
		"LOUPE_RETENTION_CONTENT":      "24h",
		"LOUPE_RETENTION_INTERVAL":     "1s",
		"LOUPE_RETENTION_BATCH":        "100",
		"LOUPE_RETENTION_MAX_DB_BYTES": "64MiB",
		"LOUPE_LOG_LEVEL":              "info",
	}
}

// start launches the process for run i on the shared database, with its own
// directory for the log, and waits for readiness without failing the test.
func (s *lpe07Suite) start(i int) (*native.Proc, error) {
	dir := filepath.Join(s.base, fmt.Sprintf("proc-%02d", i))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	p := native.Start(s.t, native.Opts{Dir: dir, Env: s.env(s.syncAck(i)), NoWait: true})
	if err := p.WaitReady(lpe07ReadyWait); err != nil {
		return p, fmt.Errorf("process for run %d not ready: %w; log tail:\n%s", i, err, lpe07Tail(p.LogPath, 4000))
	}
	return p, nil
}

// recompute starts a pricing recompute and returns its job id ("" on error).
func (s *lpe07Suite) recompute(p *native.Proc, i int) string {
	var resp struct {
		JobID string `json:"job_id"`
	}
	if err := s.adm.do(http.MethodPost, p.UIURL()+"/v1/admin/pricing/recompute", &resp); err != nil {
		s.problem("run %d recompute: %v", i, err)
		return ""
	}
	s.recomputeStarted++
	return resp.JobID
}

// jobState records the state of the recompute job right before the kill.
func (s *lpe07Suite) jobState(p *native.Proc, id string, r *lpe07Result) {
	var st struct {
		State string `json:"state"`
	}
	if err := s.adm.do(http.MethodGet, p.UIURL()+"/v1/admin/jobs/"+id, &st); err != nil {
		return
	}
	r.Recompute = st.State
	if st.State == "running" || st.State == "pending" {
		s.recomputeRunning++
	}
}

// crash drives the load against p for a random time and kills p with SIGKILL.
// Runs i%3 == 2 start a recompute just before the kill; runs i%3 == 1 wait
// for a writer job to complete (a retention pass is under way) and kill then.
func (s *lpe07Suite) crash(p *native.Proc, i int) lpe07Result {
	r := lpe07Result{Run: i, SyncAck: s.syncAck(i)}
	loadDur := lpe07LoadMin + time.Duration(s.rnd.Int64N(int64(lpe07LoadMax-lpe07LoadMin)))
	lead := 5*time.Millisecond + time.Duration(s.rnd.Int64N(int64(115*time.Millisecond)))
	r.LoadMS = loadDur.Milliseconds()
	load := &lpe07Load{client: s.client, url: p.HTTPURL() + "/v1/traces"}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { load.drive(ctx, &lpe07Gen{run: i}); close(done) }()
	var jobID string
	switch i % 3 {
	case 2:
		time.Sleep(loadDur - lead)
		jobID = s.recompute(p, i)
		time.Sleep(lead)
	case 1:
		time.Sleep(loadDur)
		if lpe07AwaitJob(s.adm.client, p.AdminURL()) {
			r.InRetention = true
			s.retentionKills++
		}
	default:
		time.Sleep(loadDur)
	}
	if jobID != "" {
		s.jobState(p, jobID, &r)
	}
	m := lpe07Metrics(s.adm.client, p.AdminURL(), "loupe_retention_passes_total", "loupe_retention_deleted_total")
	r.RetPasses = int64(m["loupe_retention_passes_total"])
	s.retPasses += r.RetPasses
	s.retDeleted += m["loupe_retention_deleted_total"]
	p.Kill9()
	stop()
	<-done
	load.wg.Wait()
	r.Acked, r.Failed, r.Rejected = load.acked.Load(), load.failed.Load(), load.rejected.Load()
	r.Sent, r.Late = load.sent.Load(), load.late.Load()
	return r
}

// verify checks the database after the restart that followed run r.
func (s *lpe07Suite) verify(r *lpe07Result) {
	integ, n, err := lpe07Check(s.dbPath, r.Run)
	if err != nil {
		integ = err.Error()
	}
	r.Integrity, r.Stored = integ, n
	r.Lost = r.Acked - r.Stored
	switch {
	case integ != "ok":
		s.integrityFail++
		s.problem("run %d integrity: %s", r.Run, integ)
	case r.SyncAck && r.Lost > 0:
		s.syncLost += r.Lost
		s.problem("run %d sync_ack lost %d", r.Run, r.Lost)
	case !r.SyncAck && r.Lost > lpe07QueueSpans:
		s.overQueue++
		s.problem("run %d lost %d > queue_spans", r.Run, r.Lost)
	case r.Acked == 0:
		s.problem("run %d: vacuous, nothing acknowledged", r.Run)
	}
	s.maxLost = max(s.maxLost, r.Lost)
	s.acked += r.Acked
	s.stored += r.Stored
	s.t.Logf("run %2d sync_ack=%-5v load=%4dms recompute=%-7s in_retention=%-5v acked=%6d stored=%6d "+
		"lost=%6d failed=%5d retention_passes=%d integrity=%s", r.Run, r.SyncAck, r.LoadMS, r.Recompute,
		r.InRetention, r.Acked, r.Stored, r.Lost, r.Failed, r.RetPasses, r.Integrity)
}

// all runs every crash and restart; the last process stops gracefully and the
// stopped database is checked once more.
func (s *lpe07Suite) all(p *native.Proc) {
	for i := range s.runs {
		r := s.crash(p, i)
		np, err := s.start(min(i+1, s.runs-1))
		if err != nil {
			s.restartFail++
			s.results = append(s.results, r)
			s.problem("run %d restart: %v", i, err)
			return
		}
		p = np
		s.verify(&r)
		s.results = append(s.results, r)
	}
	if err := p.Stop(); err != nil {
		s.problem("final stop: %v", err)
	}
	if integ, _, err := lpe07Check(s.dbPath, -1); err != nil || integ != "ok" {
		s.integrityFail++
		s.problem("final integrity: %s %v", integ, err)
	}
}

func (s *lpe07Suite) measured(seed uint64, elapsed time.Duration) map[string]any {
	syncRuns := 0
	for _, r := range s.results {
		if r.SyncAck {
			syncRuns++
		}
	}
	return map[string]any{
		"runs":                         len(s.results),
		"seed":                         seed,
		"sync_ack_runs":                syncRuns,
		"integrity_failures":           s.integrityFail,
		"restart_failures":             s.restartFail,
		"lost_over_queue_spans":        s.overQueue,
		"sync_ack_lost":                s.syncLost,
		"max_lost":                     s.maxLost,
		"acknowledged_total":           s.acked,
		"stored_total":                 s.stored,
		"recompute_started":            s.recomputeStarted,
		"recompute_running_at_kill":    s.recomputeRunning,
		"retention_job_kills":          s.retentionKills,
		"retention_passes_total":       s.retPasses,
		"retention_deleted_rows_total": int64(s.retDeleted),
		"duration_s":                   elapsed.Round(time.Second).Seconds(),
		"per_run":                      s.results,
	}
}

// lpe07Seed returns LOUPE_LPE07_SEED or a fresh seed.
func lpe07Seed() (uint64, error) {
	if v := os.Getenv("LOUPE_LPE07_SEED"); v != "" {
		return strconv.ParseUint(v, 10, 64)
	}
	return uint64(time.Now().UnixNano()), nil
}

// TestAcceptance_LPE07_CrashSafety is the acceptance test of criterion LPE-07.
// The native binary runs on one database reused across all runs (so every
// restart recovers a WAL left by a kill -9 and keeps all earlier data), with
// retention every second (retention.spans 24h, batch 100, disk cap 64 MiB) and
// an API token file. Each run drives 1,000 spans/s of OTLP/HTTP protobuf for a
// random 2–6 s (seeded RNG; LOUPE_LPE07_SEED replays a seed); a third of the
// runs start POST /v1/admin/pricing/recompute 5–120 ms before the kill, another
// third wait after the random point until a writer job completes (a retention
// pass is then under way); then the process is killed with SIGKILL. The next
// process (with the next run's sync_ack setting; odd runs use sync_ack) must
// become ready without cleanup; then integrity_check runs on a read-only
// connection and the run's fresh spans are counted: lost = acknowledged −
// stored must be ≤ ingest.queue_spans, and 0 for sync_ack runs. Purge-target
// spans (72 h old) carry their own trace-id prefix and are not counted.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE07_CrashSafety(t *testing.T) {
	run := harness.Start(t, "LPE-07")
	runs := int(lpe09Threshold(t, run, "runs"))
	if runs < 1 || runs > 255 {
		run.Fail(nil, "runs %d out of range", runs)
	}
	seed, err := lpe07Seed()
	if err != nil {
		run.Fail(nil, "LOUPE_LPE07_SEED: %v", err)
	}
	t.Logf("LPE-07 seed %d", seed)
	s := &lpe07Suite{
		t: t, runs: runs, base: t.TempDir(),
		rnd:    rand.New(rand.NewPCG(seed, seed^0x1907)),
		client: &http.Client{Timeout: 20 * time.Second},
	}
	s.dbPath = filepath.Join(s.base, "loupe.db")
	s.tokenFile = filepath.Join(s.base, "api.tokens")
	s.token = fmt.Sprintf("lpe07-%016x", s.rnd.Uint64())
	s.adm = lpe07Admin{client: &http.Client{Timeout: 5 * time.Second}, token: s.token}
	if werr := os.WriteFile(s.tokenFile, []byte(s.token+"\n"), 0o600); werr != nil {
		run.Fail(nil, "token file: %v", werr)
	}

	began := time.Now()
	p, err := s.start(0)
	if err != nil {
		run.Fail(nil, "initial start: %v", err)
	}
	s.all(p)
	measured := s.measured(seed, time.Since(began))
	summary := fmt.Sprintf("seed %d; %d runs in %s; acked %d stored %d max lost %d; integrity failures %d, "+
		"restart failures %d, over queue %d, sync_ack lost %d; recompute running at %d of %d kills; "+
		"%d kills inside a retention pass; retention passes %d, rows deleted %d",
		seed, len(s.results), time.Since(began).Round(time.Second), s.acked, s.stored, s.maxLost,
		s.integrityFail, s.restartFail, s.overQueue, s.syncLost, s.recomputeRunning, s.recomputeStarted,
		s.retentionKills, s.retPasses, int64(s.retDeleted))
	switch {
	case len(s.problems) > 0:
		run.Fail(measured, "%s; problems: %s", summary, strings.Join(s.problems, "; "))
	case len(s.results) != runs:
		run.Fail(measured, "%s; ran %d of %d", summary, len(s.results), runs)
	case s.retDeleted == 0:
		run.Fail(measured, "%s; vacuous: retention deleted nothing during load", summary)
	case s.retentionKills == 0:
		run.Fail(measured, "%s; vacuous: no kill inside a retention pass", summary)
	case s.recomputeRunning == 0:
		run.Fail(measured, "%s; vacuous: no kill hit a running recompute job", summary)
	}
	run.Pass(measured, summary)
}
