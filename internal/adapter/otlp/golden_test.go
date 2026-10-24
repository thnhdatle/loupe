// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var update = flag.Bool("update", false, "rewrite the testdata/otlp golden files")

const otlpDir = "../../../testdata/otlp"

var families = []string{"openai-v2", "openinference", "node-openai", "go-manual", "agent"}

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
	Name         string          `json:"name"`
	TraceID      string          `json:"trace_id"`
	SpanID       string          `json:"span_id"`
	ParentSpanID string          `json:"parent_span_id"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Service      string          `json:"service"`
	Model        string          `json:"model"`
	Provider     string          `json:"provider"`
	Usage        trace.Usage     `json:"usage"`
	HasUsage     bool            `json:"has_usage"`
	SessionID    string          `json:"session_id"`
	UserID       string          `json:"user_id"`
	ErrorType    string          `json:"error_type"`
	Attributes   json.RawMessage `json:"attributes"`
	Content      []goldenPart    `json:"content"`
}

type goldenLog struct {
	EventName string       `json:"event_name"`
	TraceID   string       `json:"trace_id"`
	SpanID    string       `json:"span_id"`
	Kept      bool         `json:"kept"`
	Reason    string       `json:"reason,omitempty"`
	Content   []goldenPart `json:"content"`
}

type golden struct {
	Spans []goldenSpan `json:"spans,omitempty"`
	Logs  []goldenLog  `json:"logs,omitempty"`
}

func partsOf(parts []trace.ContentPart) []goldenPart {
	out := make([]goldenPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, goldenPart{
			Part: p.Part, Source: string(p.Source), Key: p.Key, Role: p.Role, Bytes: p.Bytes,
			Body: string(p.Body), TimeNs: p.TimeNs,
		})
	}
	return out
}

type decoded struct {
	spans []trace.RawSpan
	logs  []trace.RawLogRecord
}

func decodePayload(t *testing.T, path string) decoded {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &top), path)
	route := "/v1/traces"
	if _, ok := top["resourceLogs"]; ok {
		route = "/v1/logs"
	}
	f := newFixture(t, nil)
	rec := do(t, f.srv.HTTPHandler(), httpReq{path: route, ct: ctJSON, body: body})
	require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	return decoded{spans: f.ing.Spans(), logs: f.ing.Logs()}
}

func translate(d decoded) golden {
	var g golden
	spans := slices.Clone(d.spans)
	slices.SortFunc(spans, func(a, b trace.RawSpan) int {
		if a.StartNs != b.StartNs {
			return int(a.StartNs - b.StartNs)
		}
		return strings.Compare(a.Name, b.Name)
	})
	for i := range spans {
		s := semconv.Translate(&spans[i], semconv.Options{})
		gs := goldenSpan{
			Name: s.Name, TraceID: s.TraceID.String(), SpanID: s.SpanID.String(),
			Kind: string(s.Kind), Status: string(s.Status), Service: s.Service, Model: s.Model,
			Provider: s.Provider, Usage: s.Usage, HasUsage: s.HasUsage, SessionID: s.SessionID,
			UserID: s.UserID, ErrorType: s.ErrorType, Attributes: s.Attributes, Content: partsOf(s.Content),
		}
		if !s.ParentSpanID.IsZero() {
			gs.ParentSpanID = s.ParentSpanID.String()
		}
		g.Spans = append(g.Spans, gs)
	}
	for i := range d.logs {
		rec := &d.logs[i]
		parts, reason, keep := semconv.TranslateLog(rec)
		g.Logs = append(g.Logs, goldenLog{
			EventName: semconv.EventName(rec), TraceID: rec.TraceID.String(), SpanID: rec.SpanID.String(),
			Kept: keep, Reason: reason, Content: partsOf(parts),
		})
	}
	return g
}

func globPayloads(t *testing.T, fam, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(otlpDir, fam, pattern))
	require.NoError(t, err)
	return slices.DeleteFunc(matches, func(m string) bool { return strings.HasSuffix(m, ".golden.json") })
}

func payloadPaths(t *testing.T) []string {
	t.Helper()
	paths := make([]string, 0, 2*len(families))
	for _, fam := range families {
		paths = append(paths, globPayloads(t, fam, "*.json")...)
	}
	require.NotEmpty(t, paths, "no payloads under testdata/otlp")
	return paths
}

func goldenPath(payload string) string {
	return strings.TrimSuffix(payload, ".json") + ".golden.json"
}

func TestGolden_RecordedPayloads(t *testing.T) {
	t.Parallel()
	for _, path := range payloadPaths(t) {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			got, err := json.MarshalIndent(translate(decodePayload(t, path)), "", "  ")
			require.NoError(t, err)
			got = append(got, '\n')
			if *update {
				require.NoError(t, os.WriteFile(goldenPath(path), got, 0o600))
			}
			want, err := os.ReadFile(goldenPath(path))
			require.NoError(t, err, "run go test ./internal/adapter/otlp -run Golden -update to create it")
			assert.Equal(t, string(want), string(got))
		})
	}
}

func TestGolden_NoUnmappedUsageKeys(t *testing.T) {
	t.Parallel()
	for _, path := range payloadPaths(t) {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			d := decodePayload(t, path)
			for i := range d.spans {
				assert.Empty(t, semconv.UnmappedUsageKeys(d.spans[i].Attributes), "span %s", d.spans[i].Name)
			}
			for i := range d.logs {
				assert.Empty(t, semconv.UnmappedUsageKeys(d.logs[i].Attributes), "log %s", semconv.EventName(&d.logs[i]))
			}
		})
	}
}

func TestGolden_FamiliesCoverCacheAndReasoning(t *testing.T) {
	t.Parallel()
	for _, fam := range families {
		t.Run(fam, func(t *testing.T) {
			t.Parallel()
			paths := globPayloads(t, fam, "handwritten-*.json")
			require.NotEmpty(t, paths)
			var total trace.Usage
			for _, p := range paths {
				d := decodePayload(t, p)
				for i := range d.spans {
					total = total.Add(semconv.Translate(&d.spans[i], semconv.Options{}).Usage)
				}
			}
			assert.Positive(t, total.CacheRead)
			assert.Positive(t, total.CacheWrite)
			assert.Positive(t, total.Reasoning)
		})
	}
}

func TestGolden_RecordedFamiliesTranslate(t *testing.T) {
	t.Parallel()
	for _, fam := range families {
		t.Run(fam, func(t *testing.T) {
			t.Parallel()
			paths := globPayloads(t, fam, "recorded-*-traces-*.json")
			require.NotEmpty(t, paths)
			for _, p := range paths {
				g := translate(decodePayload(t, p))
				var llm int
				for _, s := range g.Spans {
					var attrs map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(s.Attributes, &attrs))
					for k := range attrs {
						assert.False(t, semconv.IsContentKey(k), "%s: content key %s kept", s.Name, k)
					}
					if s.Kind == string(trace.KindLLM) {
						llm++
						assert.NotEmpty(t, s.Model, s.Name)
						assert.True(t, s.HasUsage, s.Name)
						assert.Positive(t, s.Usage.Input, s.Name)
						assert.Positive(t, s.Usage.Output, s.Name)
					}
				}
				assert.Positive(t, llm, p)
			}
		})
	}
}
