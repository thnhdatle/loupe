//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
	"github.com/thnhdatle/loupe/bench/gen"
)

// LPE-04 dataset and server settings.
const (
	lpe04Seed      = 1
	lpe04Days      = 30
	lpe04Anchor    = "2026-10-01T00:00:00Z"
	lpe04Retention = "400d"
	lpe04GenTime   = 30 * time.Minute
	lpe04ReqTime   = 30 * time.Second
	lpe04PageLimit = 50  // query.DefaultLimit: q1–q4 and q8 ask for the first page
	lpe04Session   = 200 // q5 traces per session
	lpe04TraceSize = 40  // q6 spans per trace
	lpe04MinDurMs  = 5000
	lpe04Prefix    = "agent."
)

// lpe04CacheInputs are the sources whose change makes a cached dataset stale:
// the generator and the schema it is written with.
var lpe04CacheInputs = []string{
	"bench/gen",
	"bench/cmd/gentraces",
	"internal/adapter/sqlite/migrations",
}

// lpe04Trace is the subset of TraceSummary the checks read.
type lpe04Trace struct {
	TraceID    string   `json:"trace_id"`
	StartNs    int64    `json:"start_ns"`
	DurationNs int64    `json:"duration_ns"`
	SpanCount  int      `json:"span_count"`
	Models     []string `json:"models"`
	CostNanos  int64    `json:"cost_nanos"`
	HasError   bool     `json:"has_error"`
	SessionID  string   `json:"session_id"`
	RootName   string   `json:"root_name"`
}

type lpe04TracePage struct {
	Data       []lpe04Trace `json:"data"`
	NextCursor string       `json:"next_cursor"`
}

type lpe04SessionDetail struct {
	Summary struct {
		ID         string `json:"id"`
		TraceCount int    `json:"trace_count"`
	} `json:"summary"`
	Data []lpe04Trace `json:"data"`
}

type lpe04TraceDetail struct {
	Summary lpe04Trace `json:"summary"`
	Spans   []struct {
		SpanID  string         `json:"span_id"`
		Content []lpe04Content `json:"content"`
	} `json:"spans"`
}

type lpe04Content struct {
	Body string `json:"body"`
}

type lpe04Overview struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Total struct {
		Requests int64 `json:"requests"`
	} `json:"total"`
	Groups []struct {
		Key   string `json:"key"`
		Stats struct {
			Requests int64 `json:"requests"`
		} `json:"stats"`
	} `json:"groups"`
	Series []struct {
		Bucket string `json:"bucket"`
	} `json:"series"`
}

// lpe04Query is one of the eight benchmark queries: path returns the request for run i (parameters
// rotate with i) and check validates the response body, so that a fast but
// wrong answer cannot pass.
type lpe04Query struct {
	name  string
	path  func(i int) string
	check func(i int, body []byte) error
}

// lpe04Stats are the latencies of one query, in seconds.
type lpe04Stats struct {
	p50, p95, max float64
}

// lpe04Nearest is the nearest-rank quantile of sorted values.
func lpe04Nearest(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	k := max(int(math.Ceil(q*float64(len(sorted))))-1, 0)
	return sorted[min(k, len(sorted)-1)]
}

func lpe04Summarize(lat []time.Duration) lpe04Stats {
	s := make([]float64, len(lat))
	for i, d := range lat {
		s[i] = d.Seconds()
	}
	sort.Float64s(s)
	return lpe04Stats{p50: lpe04Nearest(s, 0.50), p95: lpe04Nearest(s, 0.95), max: s[len(s)-1]}
}

func lpe04Num(run *harness.Run, key string) float64 {
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

// lpe04CacheDir is ~/.cache/rayld/loupe/lpe04 (or $XDG_CACHE_HOME/rayld/loupe/lpe04).
func lpe04CacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "rayld", "loupe", "lpe04")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// lpe04CacheKey hashes the non-test Go and SQL sources of lpe04CacheInputs.
func lpe04CacheKey(root string) (string, error) {
	h := sha256.New()
	for _, dir := range lpe04CacheInputs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			return "", fmt.Errorf("cache key: %w", err)
		}
		for _, e := range entries {
			n := e.Name()
			source := strings.HasSuffix(n, ".go") || strings.HasSuffix(n, ".sql")
			if e.IsDir() || strings.HasSuffix(n, "_test.go") || !source {
				continue
			}
			data, rerr := os.ReadFile(filepath.Join(root, dir, n))
			if rerr != nil {
				return "", fmt.Errorf("cache key: %w", rerr)
			}
			_, _ = fmt.Fprintf(h, "%s/%s %d\n", dir, n, len(data))
			_, _ = h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// lpe04Cache is one cached dataset: <spans>-seed1-<key>.db and its manifest
// in ~/.cache/rayld/loupe/lpe04, where key hashes lpe04CacheInputs.
type lpe04Cache struct {
	root, dir, stem, key string
	spans                int
}

func (c lpe04Cache) dbPath() string       { return filepath.Join(c.dir, c.stem+c.key+".db") }
func (c lpe04Cache) manifestPath() string { return filepath.Join(c.dir, c.stem+c.key+".manifest.json") }

// lpe04Dataset returns the cached database and manifest for spans, building
// them with gentraces (the production write path) when missing. Datasets of
// the same size built from older sources are removed.
func lpe04Dataset(tb testing.TB, root string, spans int) (string, gen.Manifest, error) {
	tb.Helper()
	var m gen.Manifest
	dir, err := lpe04CacheDir()
	if err != nil {
		return "", m, err
	}
	key, err := lpe04CacheKey(root)
	if err != nil {
		return "", m, err
	}
	c := lpe04Cache{root: root, dir: dir, stem: fmt.Sprintf("%d-seed%d-", spans, lpe04Seed), key: key, spans: spans}
	if _, serr := os.Stat(c.dbPath()); errors.Is(serr, os.ErrNotExist) {
		if gerr := c.generate(tb); gerr != nil {
			return "", m, gerr
		}
	} else if serr != nil {
		return "", m, serr
	}
	c.removeStale(tb)
	data, err := os.ReadFile(c.manifestPath())
	if err != nil {
		return "", m, fmt.Errorf("manifest: %w", err)
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return "", m, fmt.Errorf("manifest: %w", err)
	}
	if m.Spans != spans || m.Seed != lpe04Seed || m.End != lpe04Anchor || m.Days != lpe04Days {
		return "", m, fmt.Errorf("manifest %s does not describe the requested dataset: %+v", c.manifestPath(), m)
	}
	return c.dbPath(), m, nil
}

// generate builds gentraces and runs it into temporary files, then installs
// them, so an interrupted build never leaves a cached dataset behind.
func (c lpe04Cache) generate(tb testing.TB) error {
	tb.Helper()
	tmpDB, tmpMF := c.dbPath()+".tmp", c.manifestPath()+".tmp"
	bin := filepath.Join(c.dir, fmt.Sprintf("gentraces-%d", os.Getpid()))
	defer func() { _ = os.Remove(bin) }()
	ctx, cancel := context.WithTimeout(context.Background(), lpe04GenTime)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./bench/cmd/gentraces")
	build.Dir = c.root
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build gentraces: %w\n%s", err, out)
	}
	logPath := filepath.Join(c.dir, "gentraces.log")
	logf, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("gentraces log: %w", err)
	}
	defer func() { _ = logf.Close() }()
	tb.Logf("LPE-04: building the %d-span dataset with gentraces (log %s)", c.spans, logPath)
	start := time.Now()
	cmd := exec.CommandContext(ctx, bin,
		"--spans", strconv.Itoa(c.spans), "--days", strconv.Itoa(lpe04Days), "--end", lpe04Anchor,
		"--seed", strconv.Itoa(lpe04Seed), "--capture", "on", "--force",
		"--db", tmpDB, "--manifest", tmpMF)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("gentraces: %w (log %s)", err, logPath)
	}
	tb.Logf("LPE-04: dataset built in %s", time.Since(start).Round(time.Second))
	if fi, serr := os.Stat(tmpDB + "-wal"); serr == nil && fi.Size() > 0 {
		return fmt.Errorf("gentraces left a non-empty WAL %s-wal", tmpDB)
	}
	for _, s := range []string{"-wal", "-shm", ".lock"} {
		_ = os.Remove(tmpDB + s)
	}
	if err = os.Rename(tmpMF, c.manifestPath()); err != nil {
		return fmt.Errorf("install manifest: %w", err)
	}
	if err = os.Rename(tmpDB, c.dbPath()); err != nil {
		return fmt.Errorf("install dataset: %w", err)
	}
	return nil
}

// removeStale deletes cached datasets of the same size with another key.
func (c lpe04Cache) removeStale(tb testing.TB) {
	tb.Helper()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, c.stem) || strings.HasPrefix(n, c.stem+c.key+".") {
			continue
		}
		if os.Remove(filepath.Join(c.dir, n)) == nil {
			tb.Logf("LPE-04: removed stale cached dataset file %s", n)
		}
	}
}

// lpe04Copy copies src to dst.
func lpe04Copy(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// lpe04Get runs one GET and returns the body and the wall time from sending
// the request to reading the last byte.
func lpe04Get(client *http.Client, base, path string) ([]byte, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lpe04ReqTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, http.NoBody)
	if err != nil {
		return nil, 0, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	body, err := io.ReadAll(resp.Body)
	el := time.Since(start)
	_ = resp.Body.Close()
	if err != nil {
		return nil, el, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, el, fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	return body, el, nil
}

// lpe04Set builds the eight benchmark queries over a manifest. Ranges end at the
// anchor and are always explicit; parameters rotate with the run index.
type lpe04Set struct {
	m      gen.Manifest
	anchor time.Time
	// contentSpans counts q6 spans returned with a content body.
	contentSpans int
}

func newLPE04Set(m gen.Manifest) (*lpe04Set, error) {
	anchor, err := time.Parse(time.RFC3339, m.End)
	if err != nil {
		return nil, fmt.Errorf("manifest end: %w", err)
	}
	if len(m.Models) == 0 || len(m.Services) == 0 || len(m.Sessions200) == 0 || len(m.Traces40) == 0 {
		return nil, fmt.Errorf("manifest lacks rotation parameters: %d models, %d services, "+
			"%d sessions of 200, %d traces of 40",
			len(m.Models), len(m.Services), len(m.Sessions200), len(m.Traces40))
	}
	return &lpe04Set{m: m, anchor: anchor}, nil
}

func lpe04Pick(xs []string, i int) string { return xs[i%len(xs)] }

// rng returns the query values of [anchor-d, anchor) and its bounds in ns.
func (s *lpe04Set) rng(d time.Duration) (v url.Values, lo, hi int64) {
	from := s.anchor.Add(-d)
	v = url.Values{}
	v.Set("from", from.Format(time.RFC3339))
	v.Set("to", s.anchor.Format(time.RFC3339))
	return v, from.UnixNano(), s.anchor.UnixNano()
}

// list returns the path of a trace list over the last d with extra values.
func (s *lpe04Set) list(d time.Duration, set func(v url.Values, i int)) func(i int) string {
	return func(i int) string {
		v, _, _ := s.rng(d)
		if set != nil {
			set(v, i)
		}
		return "/v1/traces?" + v.Encode()
	}
}

// lpe04PageCheck describes the expected shape of a trace page.
type lpe04PageCheck struct {
	d      time.Duration
	full   bool // a full first page of lpe04PageLimit traces
	byCost bool // descending cost instead of descending start
	each   func(i int, tr lpe04Trace) error
}

// page decodes a trace page and checks the range, the order and each trace.
func (s *lpe04Set) page(pc lpe04PageCheck) func(int, []byte) error {
	return func(i int, body []byte) error {
		var p lpe04TracePage
		if err := json.Unmarshal(body, &p); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		_, lo, hi := s.rng(pc.d)
		if pc.full && len(p.Data) != lpe04PageLimit {
			return fmt.Errorf("%d traces, want a full page of %d", len(p.Data), lpe04PageLimit)
		}
		for k, tr := range p.Data {
			if k > 0 && lpe04OutOfOrder(p.Data[k-1], tr, pc.byCost) {
				return fmt.Errorf("traces not in descending order at %d", k)
			}
			if err := pc.checkTrace(i, tr, lo, hi); err != nil {
				return err
			}
		}
		return nil
	}
}

// checkTrace checks that tr starts in [lo, hi) and passes each.
func (pc lpe04PageCheck) checkTrace(i int, tr lpe04Trace, lo, hi int64) error {
	if tr.StartNs < lo || tr.StartNs >= hi {
		return fmt.Errorf("trace %s starts at %d, outside [%d, %d)", tr.TraceID, tr.StartNs, lo, hi)
	}
	if pc.each != nil {
		return pc.each(i, tr)
	}
	return nil
}

func lpe04OutOfOrder(prev, cur lpe04Trace, byCost bool) bool {
	if byCost {
		return prev.CostNanos < cur.CostNanos
	}
	return prev.StartNs < cur.StartNs
}

func (s *lpe04Set) queries() []lpe04Query {
	day := 24 * time.Hour
	return []lpe04Query{
		{name: "q1_traces_24h", path: s.list(day, nil), check: s.page(lpe04PageCheck{d: day, full: true})},
		s.q2(7 * day),
		s.q3(7 * day),
		{
			name: "q4_traces_30d_by_cost",
			path: s.list(30*day, func(v url.Values, _ int) {
				v.Set("sort", "cost")
				v.Set("order", "desc")
			}),
			check: s.page(lpe04PageCheck{d: 30 * day, full: true, byCost: true}),
		},
		s.q5(),
		s.q6(),
		s.q7(30 * day),
		{
			name:  "q8_traces_7d_name_prefix",
			path:  s.list(7*day, func(v url.Values, _ int) { v.Set("name", lpe04Prefix) }),
			check: s.page(lpe04PageCheck{d: 7 * day, full: true}),
		},
	}
}

// q2 checks that every trace has an error and the model (a trace with a
// matching span lists the model: dataset traces use at most 5 models).
func (s *lpe04Set) q2(d time.Duration) lpe04Query {
	return lpe04Query{
		name: "q2_traces_7d_model_error",
		path: s.list(d, func(v url.Values, i int) {
			v.Set("model", lpe04Pick(s.m.Models, i))
			v.Set("status", "error")
		}),
		check: s.page(lpe04PageCheck{d: d, each: func(i int, tr lpe04Trace) error {
			want := lpe04Pick(s.m.Models, i)
			if !tr.HasError || !slices.Contains(tr.Models, want) {
				return fmt.Errorf("trace %s: has_error %v, models %v, want an error and %s",
					tr.TraceID, tr.HasError, tr.Models, want)
			}
			return nil
		}}),
	}
}

// q3 checks that every trace lasts at least the span minimum.
func (s *lpe04Set) q3(d time.Duration) lpe04Query {
	return lpe04Query{
		name: "q3_traces_7d_service_min_duration",
		path: s.list(d, func(v url.Values, i int) {
			v.Set("service", lpe04Pick(s.m.Services, i))
			v.Set("min_duration_ms", strconv.Itoa(lpe04MinDurMs))
		}),
		check: s.page(lpe04PageCheck{d: d, full: true, each: func(_ int, tr lpe04Trace) error {
			if tr.DurationNs < lpe04MinDurMs*int64(time.Millisecond) {
				return fmt.Errorf("trace %s lasts %d ns, below the span minimum", tr.TraceID, tr.DurationNs)
			}
			return nil
		}}),
	}
}

func (s *lpe04Set) q5() lpe04Query {
	return lpe04Query{
		name: "q5_session_200",
		path: func(i int) string {
			return "/v1/sessions/" + url.PathEscape(lpe04Pick(s.m.Sessions200, i)) +
				"?limit=" + strconv.Itoa(lpe04Session)
		},
		check: func(i int, body []byte) error {
			var d lpe04SessionDetail
			if err := json.Unmarshal(body, &d); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			id := lpe04Pick(s.m.Sessions200, i)
			if d.Summary.ID != id || d.Summary.TraceCount != lpe04Session || len(d.Data) != lpe04Session {
				return fmt.Errorf("session %s: id %q, trace_count %d, %d traces, want %d",
					id, d.Summary.ID, d.Summary.TraceCount, len(d.Data), lpe04Session)
			}
			for _, tr := range d.Data {
				if tr.SessionID != id {
					return fmt.Errorf("trace %s belongs to session %q", tr.TraceID, tr.SessionID)
				}
			}
			return nil
		},
	}
}

func (s *lpe04Set) q6() lpe04Query {
	return lpe04Query{
		name: "q6_trace_40_content",
		path: func(i int) string { return "/v1/traces/" + lpe04Pick(s.m.Traces40, i) + "?include_content=true" },
		check: func(i int, body []byte) error {
			var d lpe04TraceDetail
			if err := json.Unmarshal(body, &d); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			id := lpe04Pick(s.m.Traces40, i)
			if d.Summary.TraceID != id || len(d.Spans) != lpe04TraceSize {
				return fmt.Errorf("trace %s: id %q, %d spans, want %d", id, d.Summary.TraceID, len(d.Spans), lpe04TraceSize)
			}
			for _, sp := range d.Spans {
				if slices.ContainsFunc(sp.Content, func(c lpe04Content) bool { return c.Body != "" }) {
					s.contentSpans++
				}
			}
			return nil
		},
	}
}

// q7 checks that every model has requests and that all 30 days are present.
func (s *lpe04Set) q7(d time.Duration) lpe04Query {
	return lpe04Query{
		name: "q7_overview_30d_model_day",
		path: func(int) string {
			v, _, _ := s.rng(d)
			v.Set("group_by", "model")
			v.Set("interval", "day")
			return "/v1/stats/overview?" + v.Encode()
		},
		check: func(_ int, body []byte) error {
			var o lpe04Overview
			if err := json.Unmarshal(body, &o); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			models := 0
			for _, g := range o.Groups {
				if slices.Contains(s.m.Models, g.Key) && g.Stats.Requests > 0 {
					models++
				}
			}
			days := map[string]bool{}
			for _, p := range o.Series {
				days[p.Bucket] = true
			}
			if models != len(s.m.Models) || len(days) != lpe04Days || o.Total.Requests <= 0 {
				return fmt.Errorf("overview: %d of %d models, %d day buckets (want %d), %d requests",
					models, len(s.m.Models), len(days), lpe04Days, o.Total.Requests)
			}
			return nil
		},
	}
}

// lpe04Measure runs warm-ups then timed runs of q sequentially and returns
// the timed latencies.
func lpe04Measure(client *http.Client, base string, q lpe04Query, warmups, runs int) ([]time.Duration, error) {
	lat := make([]time.Duration, 0, runs)
	for i := range warmups + runs {
		body, el, err := lpe04Get(client, base, q.path(i))
		if err == nil {
			err = q.check(i, body)
		}
		if err != nil {
			return nil, fmt.Errorf("%s run %d (%s): %w", q.name, i, q.path(i), err)
		}
		if i >= warmups {
			lat = append(lat, el)
		}
	}
	return lat, nil
}

// lpe04Start copies the cached dataset into a fresh work directory under the
// cache and starts the native server on it with capture on and every
// retention class at 400d, so nothing expires and the cache is never touched.
func lpe04Start(tb testing.TB, cached string) (*native.Proc, error) {
	tb.Helper()
	work, err := os.MkdirTemp(filepath.Dir(cached), "run-")
	if err != nil {
		return nil, err
	}
	tb.Cleanup(func() { _ = os.RemoveAll(work) })
	if err = lpe04Copy(cached, filepath.Join(work, "loupe.db")); err != nil {
		return nil, fmt.Errorf("copy dataset: %w", err)
	}
	return native.Start(tb, native.Opts{Dir: work, Env: map[string]string{
		"LOUPE_CONTENT_CAPTURE":          "on",
		"LOUPE_LOG_LEVEL":                "info",
		"LOUPE_RETENTION_SPANS":          lpe04Retention,
		"LOUPE_RETENTION_CONTENT":        lpe04Retention,
		"LOUPE_RETENTION_ROLLUPS_HOURLY": lpe04Retention,
		"LOUPE_RETENTION_ROLLUPS_DAILY":  lpe04Retention,
		"LOUPE_RETENTION_ORPHAN_CONTENT": lpe04Retention,
	}}), nil
}

// lpe04Scenario starts the server on a copy of the dataset and measures every
// query. It returns the measured map and the failure, if any.
func lpe04Scenario(tb testing.TB, run *harness.Run) (map[string]any, error) {
	tb.Helper()
	spans := int(lpe04Num(run, "spans"))
	runs := int(lpe04Num(run, "runs_per_query"))
	warmups := int(lpe04Num(run, "warmups"))
	p95Max := lpe04Num(run, "p95_s_max")
	measured := map[string]any{"spans": spans, "runs_per_query": runs, "warmups": warmups}

	wd, err := os.Getwd()
	if err != nil {
		return measured, err
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		return measured, err
	}
	cached, m, err := lpe04Dataset(tb, root, spans)
	if err != nil {
		return measured, fmt.Errorf("dataset: %w", err)
	}
	if fi, serr := os.Stat(cached); serr == nil {
		measured["db_bytes"] = fi.Size()
	}
	measured["traces"] = m.Traces
	set, err := newLPE04Set(m)
	if err != nil {
		return measured, err
	}
	qs := set.queries()
	if want := int(lpe04Num(run, "queries")); len(qs) != want {
		return measured, fmt.Errorf("%d queries defined, criterion has %d", len(qs), want)
	}
	p, err := lpe04Start(tb, cached)
	if err != nil {
		return measured, err
	}
	client := &http.Client{Timeout: lpe04ReqTime}
	var fails []string
	for _, q := range qs {
		lat, merr := lpe04Measure(client, p.UIURL(), q, warmups, runs)
		if merr != nil {
			return measured, merr
		}
		st := lpe04Summarize(lat)
		measured[q.name+"_p50_s"] = math.Round(st.p50*1e4) / 1e4
		measured[q.name+"_p95_s"] = math.Round(st.p95*1e4) / 1e4
		measured[q.name+"_max_s"] = math.Round(st.max*1e4) / 1e4
		tb.Logf("LPE-04 %-36s p50 %.4fs p95 %.4fs max %.4fs", q.name, st.p50, st.p95, st.max)
		if st.p95 >= p95Max {
			fails = append(fails, fmt.Sprintf("%s p95 %.3fs >= %gs", q.name, st.p95, p95Max))
		}
	}
	measured["q6_spans_with_content"] = set.contentSpans
	if set.contentSpans == 0 {
		fails = append(fails, "q6 returned no content body in any run")
	}
	if len(fails) > 0 {
		return measured, errors.New(strings.Join(fails, "; "))
	}
	return measured, nil
}

// lpe04Ran makes the scenario run once per process: the runner passes
// -benchtime 1x, and other benchtimes must not repeat the 2M-span setup.
var lpe04Ran bool

// BenchmarkAcceptance_LPE04_QueryLatency is the acceptance benchmark of criterion LPE-04.
// The scenario runs once, whatever b.N.
func BenchmarkAcceptance_LPE04_QueryLatency(b *testing.B) {
	if lpe04Ran {
		return
	}
	lpe04Ran = true
	run := harness.Start(b, "LPE-04")
	b.StopTimer()
	measured, err := lpe04Scenario(b, run)
	if err != nil {
		run.Fail(measured, "%v", err)
		return
	}
	run.Pass(measured, fmt.Sprintf("8 queries, %d runs each after %d warm-ups: every p95 below %gs",
		measured["runs_per_query"], measured["warmups"], lpe04Num(run, "p95_s_max")))
}
