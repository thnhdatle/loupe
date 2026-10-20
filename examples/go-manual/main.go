// SPDX-License-Identifier: Apache-2.0

// Command go-manual records an LLM call with hand-written OpenTelemetry spans that
// carry gen_ai attributes, and exports them to Loupe over OTLP/HTTP.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const model = "gpt-4o-mini"

type chatResponse struct {
	Model   string `json:"model"`
	ID      string `json:"id"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// autoexport reads OTEL_TRACES_EXPORTER, OTEL_METRICS_EXPORTER, OTEL_LOGS_EXPORTER and the
	// OTEL_EXPORTER_OTLP_* variables; "otlp" with http/protobuf (or http/json) is used here.
	spanExp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return err
	}
	metricReader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return err
	}
	logExp, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(spanExp))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	callErr := chat(ctx, tp.Tracer("example"), mp.Meter("example"))

	// Flush everything before exit so the spans reach Loupe.
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, shutdown := range []func(context.Context) error{tp.Shutdown, mp.Shutdown, lp.Shutdown} {
		if err := shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return callErr
}

func chat(ctx context.Context, tracer trace.Tracer, meter metric.Meter) error {
	ctx, root := tracer.Start(ctx, "go-manual-example")
	defer root.End()

	prompt := "say hello to loupe"
	ctx, span := tracer.Start(ctx, "chat "+model, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	span.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", "openai"),
		attribute.String("gen_ai.request.model", model),
		// Content opt-in: prompt and completion as span attributes.
		attribute.String("gen_ai.input.messages",
			fmt.Sprintf(`[{"role":"user","parts":[{"type":"text","content":%q}]}]`, prompt)),
	)

	body, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, os.Getenv("FAKELLM_URL")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if len(out.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}

	completion := out.Choices[0].Message.Content
	span.SetAttributes(
		attribute.String("gen_ai.response.model", out.Model),
		attribute.String("gen_ai.response.id", out.ID),
		attribute.StringSlice("gen_ai.response.finish_reasons", []string{out.Choices[0].FinishReason}),
		attribute.Int64("gen_ai.usage.input_tokens", out.Usage.PromptTokens),
		attribute.Int64("gen_ai.usage.output_tokens", out.Usage.CompletionTokens),
		attribute.String("gen_ai.output.messages",
			fmt.Sprintf(`[{"role":"assistant","finish_reason":%q,"parts":[{"type":"text","content":%q}]}]`,
				out.Choices[0].FinishReason, completion)),
	)

	// The default metrics exporter stays on; Loupe accepts and discards these.
	tokens, err := meter.Int64Histogram("gen_ai.client.token.usage", metric.WithUnit("{token}"))
	if err != nil {
		return err
	}
	tokens.Record(ctx, out.Usage.PromptTokens, metric.WithAttributes(
		attribute.String("gen_ai.operation.name", "chat"), attribute.String("gen_ai.token.type", "input")))
	tokens.Record(ctx, out.Usage.CompletionTokens, metric.WithAttributes(
		attribute.String("gen_ai.operation.name", "chat"), attribute.String("gen_ai.token.type", "output")))

	fmt.Println("reply:", completion)
	return nil
}
