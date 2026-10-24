// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fixtureEvent struct {
	Name       string         `json:"name"`
	TimeNs     int64          `json:"time_ns"`
	Attributes map[string]any `json:"attributes"`
}

type fixture struct {
	Resource     map[string]any `json:"resource"`
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id"`
	Name         string         `json:"name"`
	SpanKind     int32          `json:"span_kind"`
	StartNs      int64          `json:"start_ns"`
	EndNs        int64          `json:"end_ns"`
	StatusCode   int32          `json:"status_code"`
	Attributes   map[string]any `json:"attributes"`
	Events       []fixtureEvent `json:"events"`
}

func toValue(v any) trace.Value {
	switch x := v.(type) {
	case string:
		return trace.StringValue(x)
	case bool:
		return trace.BoolValue(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return trace.IntValue(n)
		}
		f, _ := x.Float64()
		return trace.DoubleValue(f)
	case []any:
		vs := make([]trace.Value, len(x))
		for i, e := range x {
			vs[i] = toValue(e)
		}
		return trace.ArrayValue(vs...)
	case map[string]any:
		return trace.MapValue(toAttrs(x)...)
	default:
		return trace.Value{}
	}
}

func toAttrs(m map[string]any) []trace.Attr {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]trace.Attr, 0, len(keys))
	for _, k := range keys {
		out = append(out, trace.Attr{Key: k, Value: toValue(m[k])})
	}
	return out
}

func loadFixture(t *testing.T, path string) *trace.RawSpan {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var f fixture
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&f), path)
	raw := &trace.RawSpan{
		Name: f.Name, SpanKind: f.SpanKind, StartNs: f.StartNs, EndNs: f.EndNs,
		StatusCode: f.StatusCode, Attributes: toAttrs(f.Attributes),
		Resource: &trace.RawResource{Attributes: toAttrs(f.Resource)},
	}
	raw.TraceID, err = trace.ParseID(f.TraceID)
	require.NoError(t, err)
	raw.SpanID, err = trace.ParseSpanID(f.SpanID)
	require.NoError(t, err)
	if f.ParentSpanID != "" {
		raw.ParentSpanID, err = trace.ParseSpanID(f.ParentSpanID)
		require.NoError(t, err)
	}
	for _, e := range f.Events {
		raw.Events = append(raw.Events, trace.RawEvent{Name: e.Name, TimeNs: e.TimeNs, Attributes: toAttrs(e.Attributes)})
	}
	return raw
}

type goldenPart struct {
	Part   int    `json:"part"`
	Source string `json:"source"`
	Key    string `json:"key"`
	Role   string `json:"role"`
	Bytes  int    `json:"bytes"`
	Body   string `json:"body"`
	TimeNs int64  `json:"time_ns,omitempty"`
}

type goldenSpan struct {
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Service    string          `json:"service"`
	Model      string          `json:"model"`
	Provider   string          `json:"provider"`
	Usage      trace.Usage     `json:"usage"`
	HasUsage   bool            `json:"has_usage"`
	SessionID  string          `json:"session_id"`
	UserID     string          `json:"user_id"`
	ErrorType  string          `json:"error_type"`
	Flags      uint32          `json:"flags"`
	Attributes json.RawMessage `json:"attributes"`
	Content    []goldenPart    `json:"content"`
}

func normalise(s *trace.Span) goldenSpan {
	g := goldenSpan{
		Kind: string(s.Kind), Status: string(s.Status), Service: s.Service, Model: s.Model,
		Provider: s.Provider, Usage: s.Usage, HasUsage: s.HasUsage, SessionID: s.SessionID,
		UserID: s.UserID, ErrorType: s.ErrorType, Flags: uint32(s.Flags), Attributes: s.Attributes,
		Content: []goldenPart{},
	}
	for _, p := range s.Content {
		g.Content = append(g.Content, goldenPart{
			Part: p.Part, Source: string(p.Source), Key: p.Key, Role: p.Role, Bytes: p.Bytes,
			Body: string(p.Body), TimeNs: p.TimeNs,
		})
	}
	return g
}

func fixturePaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "otlp", "*", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	return paths
}

func goldenName(path string) string {
	family := filepath.Base(filepath.Dir(path))
	return family + "_" + strings.TrimSuffix(filepath.Base(path), ".json") + ".golden"
}

func TestTranslate_Golden_Fixtures(t *testing.T) {
	t.Parallel()
	for _, path := range fixturePaths(t) {
		t.Run(goldenName(path), func(t *testing.T) {
			t.Parallel()
			span := semconv.Translate(loadFixture(t, path), semconv.Options{})
			got, err := json.MarshalIndent(normalise(&span), "", "  ")
			require.NoError(t, err)
			got = append(got, '\n')
			golden := filepath.Join("testdata", "golden", goldenName(path))
			if *update {
				require.NoError(t, os.WriteFile(golden, got, 0o600))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err, "run go test -update to create it")
			assert.Equal(t, string(want), string(got))
		})
	}
}

func TestFixtures_NoUnmappedUsageKeys(t *testing.T) {
	t.Parallel()
	for _, path := range fixturePaths(t) {
		t.Run(goldenName(path), func(t *testing.T) {
			t.Parallel()
			raw := loadFixture(t, path)
			assert.Empty(t, semconv.UnmappedUsageKeys(raw.Attributes))
		})
	}
}

func TestFixtures_FamiliesCoverCacheAndReasoning(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"genai", "openinference", "legacy"} {
		t.Run(family, func(t *testing.T) {
			t.Parallel()
			paths, err := filepath.Glob(filepath.Join("testdata", "otlp", family, "*.json"))
			require.NoError(t, err)
			var covered trace.Usage
			for _, p := range paths {
				span := semconv.Translate(loadFixture(t, p), semconv.Options{})
				covered = covered.Add(span.Usage)
			}
			assert.Positive(t, covered.CacheRead)
			assert.Positive(t, covered.CacheWrite)
			assert.Positive(t, covered.Reasoning)
		})
	}
}

func TestFixtures_ContentNeverInAttributes(t *testing.T) {
	t.Parallel()
	for _, path := range fixturePaths(t) {
		t.Run(goldenName(path), func(t *testing.T) {
			t.Parallel()
			span := semconv.Translate(loadFixture(t, path), semconv.Options{})
			for k := range attrsJSON(t, span.Attributes) {
				assert.False(t, semconv.IsContentKey(k), k)
			}
		})
	}
}
