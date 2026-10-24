// SPDX-License-Identifier: Apache-2.0

package fakeingestor

import (
	"context"
	"sync"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Ingestor records ingest calls.
type Ingestor struct {
	mu     sync.Mutex
	spans  []trace.RawSpan
	logs   []trace.RawLogRecord
	counts []trace.IngestCount
	err    error
	result *trace.IngestResult
}

var _ trace.Ingestor = (*Ingestor)(nil)

// New returns an empty Ingestor that accepts everything.
func New() *Ingestor { return &Ingestor{} }

func (f *Ingestor) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *Ingestor) SetResult(r *trace.IngestResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = r
}

func (f *Ingestor) IngestSpans(_ context.Context, spans []trace.RawSpan) (trace.IngestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return trace.IngestResult{Rejected: len(spans)}, f.err
	}
	f.spans = append(f.spans, spans...)
	return f.reply(len(spans)), nil
}

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

func (f *Ingestor) Count(c trace.IngestCount) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts = append(f.counts, c)
}

func (f *Ingestor) Spans() []trace.RawSpan {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trace.RawSpan(nil), f.spans...)
}

func (f *Ingestor) Logs() []trace.RawLogRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trace.RawLogRecord(nil), f.logs...)
}

func (f *Ingestor) Counts() []trace.IngestCount {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trace.IngestCount(nil), f.counts...)
}
