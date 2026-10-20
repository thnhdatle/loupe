// SPDX-License-Identifier: Apache-2.0

package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type nopStore struct{}

func (*nopStore) WriteBatch(context.Context, *trace.Batch) (trace.BatchResult, error) {
	return trace.BatchResult{}, nil
}

func genAILog(name, body string) trace.RawLogRecord {
	tid, sid := newIDs()
	return trace.RawLogRecord{
		Resource: res, TraceID: tid, SpanID: sid, TimeNs: epoch.UnixNano(),
		EventName: name, Body: trace.StringValue(body),
	}
}

func TestIngestor_IngestLogs(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil, ingest.WithContentPolicy(content.Policy{Mode: content.ModeOn}))
	f.run()
	kept := genAILog("gen_ai.user.message", "hello")
	noSpan := genAILog("gen_ai.choice", "x")
	noSpan.SpanID = trace.SpanID{}
	other := genAILog("app.started", "boot")
	res, err := f.in.IngestLogs(t.Context(), []trace.RawLogRecord{kept, noSpan, other})
	require.NoError(t, err)
	assert.Equal(t, trace.IngestResult{Accepted: 3}, res, "discarded records are reported as accepted")
	f.flushUntil(func() bool { return len(f.store.Content()) == 1 })
	part := f.store.Content()[0]
	assert.Equal(t, kept.SpanID, part.SpanID)
	assert.Equal(t, trace.SourceLog, part.Source)
	assert.Equal(t, "user", part.Role)
	assert.Equal(t, "hello", string(part.Body))
	assert.Equal(t, int64(3), f.count(trace.SignalLogs, trace.ResultReceived, ""))
	assert.Equal(t, int64(1), f.count(trace.SignalLogs, trace.ResultDiscarded, trace.ReasonNoSpanContext))
	assert.Equal(t, int64(1), f.count(trace.SignalLogs, trace.ResultDiscarded, trace.ReasonNotGenAI))
}

func TestIngestor_IngestLogs_CaptureOffStoresMarker(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.run()
	_, err := f.in.IngestLogs(t.Context(), []trace.RawLogRecord{genAILog("gen_ai.user.message", "secret")})
	require.NoError(t, err)
	f.flushUntil(func() bool { return len(f.store.Content()) == 1 })
	part := f.store.Content()[0]
	assert.True(t, part.IsMarker())
	assert.Equal(t, len("secret"), part.Bytes)
	assert.Equal(t, int64(1), f.count(trace.SignalLogs, trace.ResultContentStripped, ""))
}

func TestIngestor_IngestLogs_AllDiscardedSkipsQueue(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []writer.Option{writer.WithQueueLimits(1, 1)})
	_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()}) // fills the queue
	require.NoError(t, err)
	res, err := f.in.IngestLogs(t.Context(), []trace.RawLogRecord{genAILog("app.event", "x")})
	require.NoError(t, err, "discarded records never meet a full queue")
	assert.Equal(t, trace.IngestResult{Accepted: 1}, res)
}

func TestIngestor_IngestLogs_Overflow(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []writer.Option{writer.WithQueueLimits(1, 1<<30)})
	_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.NoError(t, err)
	res, err := f.in.IngestLogs(t.Context(), []trace.RawLogRecord{
		genAILog("gen_ai.user.message", "a"), genAILog("other", "b"),
	})
	require.ErrorIs(t, err, trace.ErrOverloaded)
	assert.Equal(t, 1, res.Rejected)
	assert.Zero(t, res.Accepted, "a refused request accepts nothing")
}
