# SPDX-License-Identifier: Apache-2.0
"""OpenAI chat calls traced with the OpenTelemetry GenAI openai-v2 instrumentation."""

import os

from openai import OpenAI
from opentelemetry import _logs, metrics, trace
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


def main() -> None:
    resource = Resource.create()

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

    client = OpenAI(base_url=os.environ["FAKELLM_URL"], api_key="fake")
    with trace.get_tracer("example").start_as_current_span("python-openai-example"):
        reply = client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[{"role": "user", "content": "say hello to loupe"}],
        )
        print("reply:", reply.choices[0].message.content)

    tracer_provider.shutdown()
    logger_provider.shutdown()
    meter_provider.shutdown()


main()
