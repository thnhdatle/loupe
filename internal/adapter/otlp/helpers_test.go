// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/testutil/fakeingestor"
)

func strKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func idBytes(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

func testSpan(name string, seed byte) *tracepb.Span {
	return &tracepb.Span{
		TraceId:           idBytes(16, seed),
		SpanId:            idBytes(8, seed),
		Name:              name,
		StartTimeUnixNano: 10,
		EndTimeUnixNano:   20,
		Attributes:        []*commonpb.KeyValue{strKV("gen_ai.operation.name", "chat")},
	}
}

func tracesRequest(spans ...*tracepb.Span) *coltrace.ExportTraceServiceRequest {
	return &coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: []*commonpb.KeyValue{strKV("service.name", "svc")}},
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
	}}}
}

func nSpans(n int) *coltrace.ExportTraceServiceRequest {
	spans := make([]*tracepb.Span, n)
	for i := range spans {
		spans[i] = testSpan("s", byte(i+1))
	}
	return tracesRequest(spans...)
}

func logsRequest(recs ...*logspb.LogRecord) *collogs.ExportLogsServiceRequest {
	return &collogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: recs}},
	}}}
}

func metricsRequest() *colmetrics.ExportMetricsServiceRequest {
	return &colmetrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{
			{Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{}, {}}}}},
			{Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: []*metricspb.NumberDataPoint{{}}}}},
			{Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{{}},
			}}},
			{Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}},
			}}},
			{Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{{}},
			}}},
			{},
		}}},
	}}}
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	require.NoError(t, err)
	return b
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

type fixture struct {
	srv *otlp.Server
	ing *fakeingestor.Ingestor
	lim *ingest.InflightLimiter
}

func newFixture(t *testing.T, mutate func(*otlp.Config)) *fixture {
	t.Helper()
	cfg := otlp.DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	lim, err := ingest.NewInflightLimiter(ingest.DefaultMaxInflightBytes)
	require.NoError(t, err)
	ing := fakeingestor.New()
	srv, err := otlp.NewServer(cfg, ing, lim)
	require.NoError(t, err)
	return &fixture{srv: srv, ing: ing, lim: lim}
}

func marshalF(f *testing.F, m proto.Message) []byte {
	f.Helper()
	b, err := proto.Marshal(m)
	require.NoError(f, err)
	return b
}
