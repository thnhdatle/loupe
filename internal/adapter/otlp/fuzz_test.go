// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/testutil/fakeingestor"
)

// fuzzRequest drives the whole HTTP decode path (gzip, protobuf or JSON, conversion)
// with arbitrary bytes. It must never panic, must answer with a status the receiver
// is allowed to use, and must give every in-flight byte back.
func fuzzRequest(f *testing.F, ct string, seeds [][]byte) {
	f.Helper()
	for _, s := range seeds {
		f.Add(s, "/v1/traces", false)
		f.Add(s, "/v1/logs", false)
		f.Add(s, "/v1/metrics", true)
	}
	cfg := otlp.DefaultConfig()
	cfg.MaxRequestBytes = 1 << 20
	lim, err := ingest.NewInflightLimiter(ingest.DefaultMaxInflightBytes)
	require.NoError(f, err)
	srv, err := otlp.NewServer(cfg, fakeingestor.New(), lim)
	require.NoError(f, err)
	h := srv.HTTPHandler()
	f.Fuzz(func(t *testing.T, body []byte, path string, gzipped bool) {
		switch path {
		case "/v1/traces", "/v1/logs", "/v1/metrics":
		default:
			t.Skip("only the three OTLP paths are routed")
		}
		r := httpReq{path: path, ct: ct, body: body}
		if gzipped {
			r.encoding = "gzip"
		}
		rec := do(t, h, r)
		switch rec.Code {
		case http.StatusOK, http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		default:
			t.Fatalf("unexpected status %d for %q", rec.Code, body)
		}
		if lim.InUse() != 0 {
			t.Fatalf("in-flight bytes leaked: %d", lim.InUse())
		}
	})
}

func seedPayloads(f *testing.F) (protoSeeds, jsonSeeds [][]byte) {
	f.Helper()
	raw, err := os.ReadFile("testdata/traces_genai.json")
	require.NoError(f, err)
	logs := logsRequest(&logspb.LogRecord{TraceId: idBytes(16, 1), SpanId: idBytes(8, 1), EventName: "gen_ai.choice"})
	protoSeeds = [][]byte{
		marshalF(f, nSpans(2)), marshalF(f, logs), marshalF(f, metricsRequest()), {}, {0x0a, 0x00},
	}
	jsonSeeds = [][]byte{
		raw, []byte(`{}`), []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"ab"}]}]}]}`),
		[]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"x"}}]}]}]}`),
	}
	return protoSeeds, jsonSeeds
}

func FuzzDecodeProtobuf(f *testing.F) {
	p, _ := seedPayloads(f)
	fuzzRequest(f, ctProto, p)
}

func FuzzDecodeJSON(f *testing.F) {
	_, j := seedPayloads(f)
	fuzzRequest(f, ctJSON, j)
}
