// SPDX-License-Identifier: Apache-2.0

package trace

// Signal is an OTLP signal.
type Signal string

// Signals.
const (
	SignalTraces  Signal = "traces"
	SignalLogs    Signal = "logs"
	SignalMetrics Signal = "metrics"
)

// Result is the outcome an ingest counter records (ingest_stats.result).
type Result string

// Ingest results.
const (
	ResultReceived        Result = "received"
	ResultStored          Result = "stored"
	ResultDuplicate       Result = "duplicate" // already stored, ignored by INSERT OR IGNORE
	ResultRejected        Result = "rejected"
	ResultDropped         Result = "dropped"
	ResultDiscarded       Result = "discarded"
	ResultContentStripped Result = "content_stripped"
	ResultUnpriced        Result = "unpriced"
)

// Reasons used with ResultRejected, ResultDropped and ResultDiscarded.
const (
	ReasonTooManySpans  = "too_many_spans"
	ReasonRequestTooBig = "request_too_large"
	ReasonInflight      = "inflight_limit"
	ReasonQueueFull     = "queue_full"
	ReasonStoreError    = "store_error"
	ReasonShutdown      = "shutdown"
	ReasonInvalid       = "invalid"
	ReasonNotStored     = "not_stored"
	ReasonNoSpanContext = "no_span_context"
	ReasonNotGenAI      = "not_gen_ai"
)

// IngestCount is one counter increment, folded by the writer into ingest_stats
// for the UTC hour containing AtNs.
type IngestCount struct {
	AtNs   int64
	Signal Signal
	Result Result
	Reason string
	N      int64
}

// IngestResult is what the Ingestor reports back to the receiver for one request.
// Rejected and Dropped populate OTLP partial_success; Message explains them.
type IngestResult struct {
	Accepted int
	Rejected int
	Dropped  int
	Message  string
}
