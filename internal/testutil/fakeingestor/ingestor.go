// SPDX-License-Identifier: Apache-2.0

package fakeingestor

import (
	"context"
	"sync"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Ingestor records ingest calls. It is safe for concurrent use.
type Ingestor struct {
	mu     sync.Mutex // protects every field below
	spans  []trace.RawSpan
	logs   []trace.RawLogRecord
	counts []trace.IngestCount
	err    error
	result *trace.IngestResult
}

var _ trace.Ingestor = (*Ingestor)(nil)

// New returns an empty Ingestor that accepts everything.
func New() *Ingestor { return &Ingestor{} }

// SetError makes later IngestSpans and IngestLogs calls fail with err (nil clears it).
func (f *Ingestor) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// SetResult makes later calls return r instead of an all-accepted result (nil clears it).
func (f *Ingestor) SetResult(r *trace.IngestResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = r
}

// IngestSpans implements trace.Ingestor.
func (f *Ingestor) IngestSpans(_ context.Context, spans []trace.RawSpan) (trace.IngestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return trace.IngestResult{Rejected: len(spans)}, f.err
	}
	f.spans = append(f.spans, spans...)
	return f.reply(len(spans)), nil
}

// IngestLogs implements trace.Ingestor.
func (f *Ingestor) IngestLogs(_ context.Context, recs []trace.RawLogRecord) (trace.IngestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return trace.IngestResult{Rejected: len(recs)}, f.err
	}
	f.logs = append(f.logs, recs...)
	return f.reply(len(recs)), nil
}

func (f *Ingestor) reply(n int) trace.IngestResult {
	if f.result != nil {
		return *f.result
	}
	return trace.IngestResult{Accepted: n}
}

// Count implements trace.Ingestor.
func (f *Ingestor) Count(c trace.IngestCount) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts = append(f.counts, c)
}

// Spans returns a copy of the spans received so far.
func (f *Ingestor) Spans() []trace.RawSpan {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trace.RawSpan(nil), f.spans...)
}

// Logs returns a copy of the log records received so far.
func (f *Ingestor) Logs() []trace.RawLogRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trace.RawLogRecord(nil), f.logs...)
}

// Counts returns a copy of the counters recorded so far.
func (f *Ingestor) Counts() []trace.IngestCount {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trace.IngestCount(nil), f.counts...)
}
