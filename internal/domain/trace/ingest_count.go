// SPDX-License-Identifier: Apache-2.0

package trace

type Signal string

const (
	SignalTraces  Signal = "traces"
	SignalLogs    Signal = "logs"
	SignalMetrics Signal = "metrics"
)

type Result string

const (
	ResultReceived        Result = "received"
	ResultStored          Result = "stored"
	ResultDuplicate       Result = "duplicate"
	ResultRejected        Result = "rejected"
	ResultDropped         Result = "dropped"
	ResultDiscarded       Result = "discarded"
	ResultContentStripped Result = "content_stripped"
	ResultUnpriced        Result = "unpriced"
)

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

type IngestCount struct {
	AtNs   int64
	Signal Signal
	Result Result
	Reason string
	N      int64
}

type IngestResult struct {
	Accepted int
	Rejected int
	Dropped  int
	Message  string
}
