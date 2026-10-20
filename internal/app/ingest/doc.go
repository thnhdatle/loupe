// SPDX-License-Identifier: Apache-2.0

// Package ingest implements the trace.Ingestor port:
// every span is translated (semconv), its content goes through the capture
// policy and redaction (content), it is priced (cost) and the request is
// enqueued as one item on the bounded writer queue, with its estimated bytes.
// GenAI log events become batch-level content parts; other log records are
// discarded and counted. A full queue rejects the request (trace.ErrOverloaded)
// or, with overflow "drop", drops and counts it. With sync_ack the call waits
// until the request's batch commits.
//
// Main types: Ingestor, Queue (implemented by app/writer), Overflow and
// InflightLimiter (receiver.max_inflight_bytes accounting for the OTLP adapter).
//
// Usage:
//
//	ing, err := ingest.New(w, calc, clock.System{}, ingest.WithOverflow(ingest.OverflowReject))
//	if err != nil {
//		return err
//	}
//	res, err := ing.IngestSpans(ctx, raw)
package ingest
