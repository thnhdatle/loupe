// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func strVal(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

type panicIngestor struct{ trace.Ingestor }

func (panicIngestor) IngestSpans(context.Context, []trace.RawSpan) (trace.IngestResult, error) {
	panic("boom")
}

func rewriteIDsToHex(t *testing.T, b []byte) []byte {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal(b, &doc))
	walk(t, doc, func(m map[string]any) {
		for _, k := range []string{"traceId", "spanId", "parentSpanId"} {
			if s, ok := m[k].(string); ok {
				raw, err := base64.StdEncoding.DecodeString(s)
				require.NoError(t, err)
				m[k] = hex.EncodeToString(raw)
			}
		}
	})
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

func hexToBase64(t *testing.T, b []byte) []byte {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal(b, &doc))
	walk(t, doc, func(m map[string]any) {
		for _, k := range []string{"traceId", "spanId", "parentSpanId"} {
			if s, ok := m[k].(string); ok {
				raw, err := hex.DecodeString(s)
				require.NoError(t, err)
				m[k] = base64.StdEncoding.EncodeToString(raw)
			}
		}
	})
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

func walk(t *testing.T, v any, fn func(map[string]any)) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, e := range x {
			walk(t, e, fn)
		}
	case []any:
		for _, e := range x {
			walk(t, e, fn)
		}
	}
}
