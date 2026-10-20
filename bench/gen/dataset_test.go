// SPDX-License-Identifier: Apache-2.0

package gen_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/bench/gen"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var testEnd = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func newTestDataset(t *testing.T, spans int, seed uint64) *gen.Dataset {
	t.Helper()
	d, err := gen.NewDataset(gen.DatasetConfig{Spans: spans, Days: 30, End: testEnd, Seed: seed})
	if err != nil {
		t.Fatalf("NewDataset: %v", err)
	}
	return d
}

func attr(sp *trace.RawSpan, key string) (trace.Value, bool) {
	for _, a := range sp.Attributes {
		if a.Key == key {
			return a.Value, true
		}
	}
	return trace.Value{}, false
}

func TestDataset_Deterministic(t *testing.T) {
	t.Parallel()
	a, b := newTestDataset(t, 10_000, 7), newTestDataset(t, 10_000, 7)
	c := newTestDataset(t, 10_000, 8)
	for i := range 50 {
		ta, tb := a.Trace(i), b.Trace(i)
		ja, _ := json.Marshal(ta)
		jb, _ := json.Marshal(tb)
		if string(ja) != string(jb) {
			t.Fatalf("trace %d differs for the same seed", i)
		}
	}
	if a.Trace(0).ID == c.Trace(0).ID {
		t.Fatal("different seeds gave the same trace id")
	}
}

func TestDataset_SpanCountExact(t *testing.T) {
	t.Parallel()
	for _, spans := range []int{3, 57, 999, 10_000, 33_333} {
		d := newTestDataset(t, spans, 1)
		total := 0
		for tr := range d.All() {
			total += len(tr.Spans)
			if len(tr.Spans) < 3 || len(tr.Spans) > 40 {
				t.Fatalf("spans=%d: trace %d has %d spans", spans, tr.Index, len(tr.Spans))
			}
		}
		if total != spans {
			t.Fatalf("spans=%d: generated %d", spans, total)
		}
	}
}

// shapeStats accumulates the properties checked over a whole dataset.
type shapeStats struct {
	ops                              map[string]int
	llm, withContent, errTraces, n40 int
	traces                           int
	sessions                         map[string]int
	spanIDs                          map[trace.SpanID]bool
}

// addSpan checks the per-span invariants; byID holds the trace's spans so far.
func (st *shapeStats) addSpan(t *testing.T, sp *trace.RawSpan, byID map[trace.SpanID]*trace.RawSpan) {
	t.Helper()
	start := testEnd.Add(-30 * 24 * time.Hour).UnixNano()
	if st.spanIDs[sp.SpanID] {
		t.Fatal("duplicate span id")
	}
	st.spanIDs[sp.SpanID] = true
	if sp.StartNs < start || sp.EndNs >= testEnd.UnixNano() || sp.EndNs <= sp.StartNs {
		t.Fatalf("span time out of range: %d..%d", sp.StartNs, sp.EndNs)
	}
	if sp.ParentSpanID != (trace.SpanID{}) {
		if p := byID[sp.ParentSpanID]; p == nil || sp.StartNs < p.StartNs || sp.EndNs > p.EndNs {
			t.Fatal("child not nested in an earlier parent")
		}
	}
	op, _ := attr(sp, "gen_ai.operation.name")
	st.ops[op.Str]++
	if op.Str == "chat" {
		st.llm++
		if checkContent(t, sp) {
			st.withContent++
		}
	}
}

// addTrace checks the per-trace invariants and accumulates the statistics.
func (st *shapeStats) addTrace(t *testing.T, tr *gen.Trace) {
	t.Helper()
	st.traces++
	st.sessions[tr.Session]++
	if len(tr.Spans) == 40 {
		st.n40++
	}
	if root := tr.Spans[0]; root.ParentSpanID != (trace.SpanID{}) || !strings.HasPrefix(root.Name, "agent.") {
		t.Fatal("first span must be the agent root")
	}
	byID := map[trace.SpanID]*trace.RawSpan{}
	hasErr := false
	for i := range tr.Spans {
		sp := &tr.Spans[i]
		byID[sp.SpanID] = sp
		st.addSpan(t, sp, byID)
		hasErr = hasErr || sp.StatusCode == trace.StatusCodeError
	}
	if hasErr {
		st.errTraces++
	}
}

func TestDataset_Shape(t *testing.T) {
	t.Parallel()
	const spans = 30_000
	d := newTestDataset(t, spans, 3)
	st := &shapeStats{ops: map[string]int{}, sessions: map[string]int{}, spanIDs: map[trace.SpanID]bool{}}
	for tr := range d.All() {
		st.addTrace(t, &tr)
	}
	st.verifyRatios(t, spans)
	st.verifyManifest(t, d.Manifest(), spans)
}

func (st *shapeStats) verifyRatios(t *testing.T, spans int) {
	t.Helper()
	if f := float64(st.llm) / float64(spans); f < 0.37 || f > 0.43 {
		t.Errorf("llm share %.3f, want 0.40 +- 0.03", f)
	}
	if f := float64(st.withContent) / float64(st.llm); f < 0.07 || f > 0.13 {
		t.Errorf("content share %.3f of llm spans", f)
	}
	if f := float64(st.errTraces) / float64(st.traces); f < 0.02 || f > 0.08 {
		t.Errorf("error trace share %.3f", f)
	}
	for _, op := range []string{"invoke_agent", "execute_tool", "retrieval", "invoke_workflow"} {
		if st.ops[op] == 0 {
			t.Errorf("no %s spans", op)
		}
	}
}

func (st *shapeStats) verifyManifest(t *testing.T, m gen.Manifest, spans int) {
	t.Helper()
	if st.n40 < 30 || len(m.Traces40) != st.n40 {
		t.Errorf("40-span traces: %d generated, %d in manifest", st.n40, len(m.Traces40))
	}
	if m.Session200 == "" || st.sessions[m.Session200] != 200 {
		t.Errorf("session %q has %d traces", m.Session200, st.sessions[m.Session200])
	}
	for s, c := range st.sessions {
		if c < 1 || c > 200 {
			t.Errorf("session %s has %d traces", s, c)
		}
	}
	if m.Sessions != len(st.sessions) || m.Traces != st.traces || m.Spans != spans {
		t.Errorf("manifest counts differ: %+v", m)
	}
}

func TestDataset_ServicesAndModels(t *testing.T) {
	t.Parallel()
	m := newTestDataset(t, 10_000, 5).Manifest()
	if len(m.Services) != 20 || len(m.Models) != 50 {
		t.Fatalf("services=%d models=%d", len(m.Services), len(m.Models))
	}
	used := map[string]bool{}
	for svc, ms := range m.ServiceModels {
		if len(ms) < 2 || len(ms) > 5 {
			t.Errorf("%s has %d models", svc, len(ms))
		}
		for _, x := range ms {
			used[x] = true
		}
	}
	if len(used) != 50 {
		t.Errorf("%d models used by services, want 50", len(used))
	}
}

// checkContent reports whether sp carries content and checks its size and JSON.
func checkContent(t *testing.T, sp *trace.RawSpan) bool {
	t.Helper()
	in, ok := attr(sp, "gen_ai.input.messages")
	if !ok {
		return false
	}
	out, _ := attr(sp, "gen_ai.output.messages")
	if sz := len(in.Str) + len(out.Str); sz < 2048 || sz > 8*1024+256 {
		t.Fatalf("content size %d", sz)
	}
	if !json.Valid([]byte(in.Str)) || !json.Valid([]byte(out.Str)) {
		t.Fatal("content is not valid JSON")
	}
	return true
}
