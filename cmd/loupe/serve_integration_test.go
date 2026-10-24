// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/testutil"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func strKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func intKV(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

func stubModel(t *testing.T) *httptest.Server {
	t.Helper()
	answer := `{"causes":[{"text":"stub cause","span_ids":["0101010101010101","ffffffffffffffff"]}],"fixes":[]}`
	reply, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"message": map[string]string{"role": "assistant", "content": answer},
	}}})
	require.NoError(t, err)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-stub" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(reply)
	}))
}

func post(t *testing.T, url string, hdr map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, http.NoBody)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

func TestServe_EndToEnd(t *testing.T) {
	t.Parallel()
	model := stubModel(t)
	defer model.Close()
	keyFile := filepath.Join(t.TempDir(), "llm_key")
	require.NoError(t, os.WriteFile(keyFile, []byte("sk-stub\n"), 0o600))
	cfg := config.Default()
	cfg.Analyze = config.Analyze{
		BaseURL: model.URL + "/v1", Model: "stub", APIKeyFile: keyFile,
		Timeout: config.Duration(10 * time.Second), MaxSpans: 200,
	}
	cfg.Storage.Path = filepath.Join(t.TempDir(), "loupe.db")
	cfg.Receiver.GRPC.Listen = freeAddr(t)
	cfg.Receiver.HTTP.Listen = freeAddr(t)
	cfg.UI.Listen = freeAddr(t)
	cfg.Admin.Listen = freeAddr(t)
	cfg.ShutdownTimeout = config.Duration(5 * time.Second)
	require.NoError(t, cfg.Validate())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, &cfg, io.Discard) }()

	admin := "http://" + cfg.Admin.Listen
	testutil.Eventually(t, func() bool { code, _ := get(t, admin+"/readyz"); return code == http.StatusOK },
		10*time.Second)
	code, _ := get(t, admin+"/healthz")
	require.Equal(t, http.StatusOK, code)

	traceID := bytes.Repeat([]byte{0xab}, 16)
	now := time.Now()
	span := &tracepb.Span{
		TraceId: traceID, SpanId: bytes.Repeat([]byte{0x01}, 8), Name: "chat gpt-4o",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(now.Add(-time.Second).UnixNano()),
		EndTimeUnixNano:   uint64(now.UnixNano()),
		Attributes: []*commonpb.KeyValue{
			strKV("gen_ai.operation.name", "chat"), strKV("gen_ai.system", "openai"),
			strKV("gen_ai.request.model", "gpt-4o"), strKV("gen_ai.response.model", "gpt-4o"),
			intKV("gen_ai.usage.input_tokens", 100), intKV("gen_ai.usage.output_tokens", 20),
		},
	}
	body, err := proto.Marshal(&coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: []*commonpb.KeyValue{strKV("service.name", "demo")}},
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{span}}},
	}}})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+cfg.Receiver.HTTP.Listen+"/v1/traces", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ui := "http://" + cfg.UI.Listen
	id := hex.EncodeToString(traceID)
	testutil.Eventually(t, func() bool {
		c, b := get(t, ui+"/v1/traces")
		if c != http.StatusOK {
			return false
		}
		var page struct {
			Data []struct {
				TraceID string `json:"trace_id"`
			} `json:"data"`
		}
		return json.Unmarshal(b, &page) == nil && len(page.Data) == 1 && page.Data[0].TraceID == id
	}, 10*time.Second)
	code, _ = get(t, ui+"/v1/traces/"+id)
	require.Equal(t, http.StatusOK, code)

	code, analysis := post(t, ui+"/v1/traces/"+id+"/analyze", nil)
	require.Equal(t, http.StatusOK, code, string(analysis))
	require.Contains(t, string(analysis), `"causes":[{"text":"stub cause","span_ids":["0101010101010101"]}]`)
	code, analysis = post(t, ui+"/traces/"+id+"/analyze", map[string]string{"HX-Request": "true"})
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, string(analysis), "stub cause")

	code, exported := get(t, ui+"/v1/export/traces")
	require.Equal(t, http.StatusOK, code)
	lines := bytes.Split(bytes.TrimSpace(exported), []byte("\n"))
	require.Len(t, lines, 1)
	require.Contains(t, string(lines[0]), id)

	code, metricsBody := get(t, admin+"/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, string(metricsBody), `loupe_ingest_spans_total{result="stored"} 1`)
	require.Contains(t, string(metricsBody), "loupe_build_info")

	cancel()
	select {
	case serr := <-done:
		require.NoError(t, serr)
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop")
	}
	code, _ = get(t, admin+"/readyz")
	require.Zero(t, code, "admin listener must be closed")

	store, err := sqlite.Open(context.Background(), cfg.Storage.Path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}
