# SPDX-License-Identifier: Apache-2.0
"""An app that calls Triage's OpenAI-compatible endpoint; the app and Triage both export to Loupe.

The app's `chat` and `embeddings` spans come from opentelemetry-instrumentation-openai-v2, an
HTTP client span wraps every request and carries the W3C trace context, so Triage's
`route request` span joins the app's trace. Triage forwards to fakellm, scripted per model
through fakellm's `PUT /_fake/script`.

Providers use the SDK defaults: traces, metrics and logs all export OTLP/HTTP to
OTEL_EXPORTER_OTLP_ENDPOINT.
"""

import os

import httpx
from openai import OpenAI
from opentelemetry import _logs, metrics, propagate, trace
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.openai_v2 import OpenAIInstrumentor
from opentelemetry.sdk._logs import LoggerProvider
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

LONG_ANSWER = " ".join(f"word{i}" for i in range(40))


class TracedTransport(httpx.BaseTransport):
    """Wraps every request in a CLIENT span and injects the trace context into its headers."""

    def __init__(self, inner: httpx.BaseTransport) -> None:
        self._inner = inner
        self._tracer = trace.get_tracer("example.http")

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        with self._tracer.start_as_current_span(
            request.method,
            kind=trace.SpanKind.CLIENT,
            attributes={
                "http.request.method": request.method,
                "url.full": str(request.url),
                "server.address": request.url.host,
            },
        ) as span:
            propagate.inject(request.headers)
            response = self._inner.handle_request(request)
            span.set_attribute("http.response.status_code", response.status_code)
            return response


def script(fake: str, rules: list[dict]) -> None:
    httpx.put(f"{fake}/_fake/script", json=rules).raise_for_status()


def main() -> None:
    resource = Resource.create()  # service.name comes from OTEL_SERVICE_NAME

    tracer_provider = TracerProvider(resource=resource)
    tracer_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(tracer_provider)

    logger_provider = LoggerProvider(resource=resource)
    logger_provider.add_log_record_processor(BatchLogRecordProcessor(OTLPLogExporter()))
    _logs.set_logger_provider(logger_provider)

    meter_provider = MeterProvider(
        resource=resource, metric_readers=[PeriodicExportingMetricReader(OTLPMetricExporter())]
    )
    metrics.set_meter_provider(meter_provider)

    OpenAIInstrumentor().instrument()

    fake = os.environ["FAKELLM_CONTROL_URL"]
    client = OpenAI(
        base_url=os.environ["TRIAGE_URL"],
        api_key="unused",
        http_client=httpx.Client(transport=TracedTransport(httpx.HTTPTransport())),
    )
    httpx.delete(f"{fake}/_fake/script")

    # (a) one chat attempt
    r = client.chat.completions.create(
        model="triage-auto", messages=[{"role": "user", "content": "one attempt"}]
    )
    print("a:", r.choices[0].message.content)

    # (b1) the first target fails with a 500 (no usage), the fallback target answers
    script(fake, [{"model": "fake-small", "scenario": "500", "count": 1}])
    r = client.chat.completions.create(
        model="triage-auto", messages=[{"role": "user", "content": "failed attempt then fallback"}]
    )
    print("b1:", r.choices[0].message.content)

    # (b2) the first target cuts the stream, the fallback target continues it
    script(
        fake,
        [
            {
                "model": "fake-small",
                "scenario": "cut-stream",
                "count": 1,
                "params": {"cut_after": 3, "answer": LONG_ANSWER, "chunk_bytes": 8},
            },
            {"model": "fake-strong", "scenario": "ok", "count": 1, "params": {"answer": LONG_ANSWER}},
        ],
    )
    stream = client.chat.completions.create(
        model="triage-auto",
        messages=[{"role": "user", "content": "cut stream then continuation"}],
        stream=True,
        stream_options={"include_usage": True},
    )
    text = "".join(c.choices[0].delta.content or "" for c in stream if c.choices)
    print("b2 words:", len(text.split()))

    # (c) embeddings
    r = client.embeddings.create(model="triage-embed", input="embed this text please")
    print("c dims:", len(r.data[0].embedding))

    httpx.delete(f"{fake}/_fake/script")

    # Flush everything before exit: spans, log events and one final metrics export.
    tracer_provider.shutdown()
    logger_provider.shutdown()
    meter_provider.shutdown()


main()
