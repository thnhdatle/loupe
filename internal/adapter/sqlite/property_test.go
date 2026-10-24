// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

type payload struct {
	spans   []trace.Span
	content []trace.ContentPart
}

func (p *payload) items() int { return len(p.spans) + len(p.content) }

func (p *payload) batch(idx []int) *trace.Batch {
	b := &trace.Batch{Resources: []trace.Resource{{ID: 7, Attributes: []byte(`{}`)}}}
	for _, i := range idx {
		if i < len(p.spans) {
			b.Spans = append(b.Spans, p.spans[i])
		} else {
			b.Content = append(b.Content, p.content[i-len(p.spans)])
		}
	}
	return b
}

func embeddingTrace(tr int) []trace.Span {
	return []trace.Span{
		mkSpan(tr, 1, 0, trace.KindEmbedding, model("embed-auto"), usage(30, 0)),
		mkSpan(tr, 2, 1, trace.KindOther),
		mkSpan(tr, 3, 2, trace.KindEmbedding, model("route-e"), usage(30, 0)),
		mkSpan(tr, 4, 3, trace.KindEmbedding, model("text-embed"), usage(30, 0), priced(3)),
		mkSpan(tr, 5, 3, trace.KindLLM, model("gpt"), usage(30, 0), priced(9)),
	}
}

var genUsages = []trace.Usage{
	{},
	{Input: 100, Output: 50},
	{Input: 60, Output: 30},
	{Input: 40, Output: 20},
	{Input: 100, Output: 50, CacheRead: 10, Reasoning: 5},
}

func genTrace(t *rapid.T, tr int) []trace.Span {
	sess := rapid.SampledFrom([]string{"", "sA", "sB"}).Draw(t, "session")
	usr := map[string]string{"sA": "uA", "sB": ""}[sess]
	if sess == "" {
		usr = rapid.SampledFrom([]string{"", "uX"}).Draw(t, "user")
	}
	n := rapid.IntRange(1, 8).Draw(t, "spans")
	spans := make([]trace.Span, 0, n)
	for i := 1; i <= n; i++ {
		parent := 0
		if i > 1 {
			parent = rapid.IntRange(1, i-1).Draw(t, "parent")
		}
		kind := rapid.SampledFrom([]trace.Kind{
			trace.KindLLM, trace.KindLLM, trace.KindEmbedding, trace.KindOther, trace.KindChain,
		}).Draw(t, "kind")
		opts := []spanOpt{
			model(rapid.SampledFrom([]string{"m1", "m2", "m3"}).Draw(t, "model")),
			at(time.Duration(rapid.IntRange(0, 180).Draw(t, "minute")) * time.Minute),
			lasting(time.Duration(rapid.IntRange(1, 5000).Draw(t, "ms")) * time.Millisecond),
			session(sess), user(usr),
		}
		u := rapid.SampledFrom(genUsages).Draw(t, "usage")
		opts = append(opts, func(s *trace.Span) { s.Usage, s.HasUsage = u, !u.IsZero() })
		if rapid.Bool().Draw(t, "priced") {
			opts = append(opts, priced(rapid.Int64Range(0, 5000).Draw(t, "cost")))
		}
		if rapid.IntRange(0, 9).Draw(t, "status") == 0 {
			opts = append(opts, failed())
		}
		spans = append(spans, mkSpan(tr, i, parent, kind, opts...))
	}
	return spans
}

func genPayload(t *rapid.T) payload {
	var p payload
	traces := rapid.IntRange(1, 3).Draw(t, "traces")
	for tr := 1; tr <= traces; tr++ {
		p.spans = append(p.spans, genTrace(t, tr)...)
	}
	if rapid.Bool().Draw(t, "triage") {
		p.spans = append(p.spans, triageTrace(90)...)
	}
	if rapid.Bool().Draw(t, "embedding") {
		p.spans = append(p.spans, embeddingTrace(91)...)
	}
	for i := range p.spans {
		if rapid.IntRange(0, 3).Draw(t, "attr-content") == 0 {
			p.spans[i].Content = []trace.ContentPart{{
				Source: trace.SourceAttribute, Key: "input.value",
				Role: "user", Bytes: 5, Body: []byte(fmt.Sprintf("body%d", i)),
			}}
		}
	}
	for range rapid.IntRange(0, 4).Draw(t, "logs") {
		sp := p.spans[rapid.IntRange(0, len(p.spans)-1).Draw(t, "log-span")]
		target := sp.SpanID
		if rapid.IntRange(0, 4).Draw(t, "orphan") == 0 {
			target = sid(77)
		}
		p.content = append(p.content, trace.ContentPart{
			TraceID: sp.TraceID, SpanID: target, Source: trace.SourceLog, Key: "gen_ai.choice", Role: "assistant",
			Bytes: 2, Body: []byte("ok"), TimeNs: sp.StartNs + rapid.Int64Range(0, 100).Draw(t, "log-time"),
		})
	}
	return p
}

func split(t *rapid.T, order []int) [][]int {
	var out [][]int
	for len(order) > 0 {
		n := rapid.IntRange(1, len(order)).Draw(t, "batch")
		out = append(out, order[:n])
		order = order[n:]
	}
	return out
}

func indices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func templateDB(t *testing.T) string {
	t.Helper()
	path := dbPath(t)
	s, err := sqlite.Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	return path
}

func propStore(t *testing.T, tpl string) *sqlite.Store {
	t.Helper()
	data, err := os.ReadFile(tpl)
	require.NoError(t, err)
	path := dbPath(t)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	s, err := sqlite.Open(context.Background(), path,
		sqlite.WithClock(clock.NewFake(anchor)), sqlite.WithReadPoolSize(1))
	require.NoError(t, err)
	return s
}

func writeBatches(t *rapid.T, s *sqlite.Store, p *payload, batches [][]int) []trace.BatchResult {
	out := make([]trace.BatchResult, 0, len(batches))
	for _, b := range batches {
		res, err := s.WriteBatch(context.Background(), p.batch(b))
		if err != nil {
			t.Fatalf("write batch %v: %v", b, err)
		}
		out = append(out, res)
	}
	return out
}

func diffSnapshots(t *rapid.T, want, got map[string][]string) {
	for table := range want {
		if !reflect.DeepEqual(want[table], got[table]) {
			t.Fatalf("table %s differs:\nwant %q\ngot  %q", table, want[table], got[table])
		}
	}
}

func TestStore_ArrivalOrderProperty(t *testing.T) {
	t.Parallel()
	tpl := templateDB(t)
	rapid.Check(t, func(rt *rapid.T) {
		p := genPayload(rt)
		ref := propStore(t, tpl)
		defer func() { _ = ref.Close() }()
		writeBatches(rt, ref, &p, [][]int{indices(p.items())})
		want := snapshotDB(t, ref.ReadPool())

		got := propStore(t, tpl)
		defer func() { _ = got.Close() }()
		order := rapid.Permutation(indices(p.items())).Draw(rt, "order")
		writeBatches(rt, got, &p, split(rt, order))
		diffSnapshots(rt, want, snapshotDB(t, got.ReadPool()))
	})
}

func TestStore_ResendProperty(t *testing.T) {
	t.Parallel()
	tpl := templateDB(t)
	rapid.Check(t, func(rt *rapid.T) {
		p := genPayload(rt)
		s := propStore(t, tpl)
		defer func() { _ = s.Close() }()
		first := rapid.Permutation(indices(p.items())).Draw(rt, "first")
		writeBatches(rt, s, &p, split(rt, first))
		want := snapshotDB(t, s.ReadPool())

		subset := rapid.SliceOfNDistinct(rapid.IntRange(0, p.items()-1), 1, p.items(), rapid.ID[int]).
			Draw(rt, "resend")
		for _, res := range writeBatches(rt, s, &p, split(rt, subset)) {
			if res.Inserted != 0 || len(res.UsageDuplicates) != 0 {
				rt.Fatalf("re-send changed rows: %+v", res)
			}
		}
		diffSnapshots(rt, want, snapshotDB(t, s.ReadPool()))
	})
}
