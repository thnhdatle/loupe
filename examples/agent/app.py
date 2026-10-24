# SPDX-License-Identifier: Apache-2.0
"""A two-step tool-using agent with manual agent and tool spans around openai-v2 llm spans."""

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
    tracer = trace.get_tracer("example")
    tools = [
        {
            "type": "function",
            "function": {
                "name": "lookup_weather",
                "description": "Look up the weather for a city",
                "parameters": {
                    "type": "object",
                    "properties": {"input": {"type": "string"}},
                    "required": ["input"],
                },
            },
        }
    ]
    messages = [{"role": "user", "content": "what is the weather in Paris"}]

    with tracer.start_as_current_span(
        "invoke_agent weather-agent",
        kind=trace.SpanKind.CLIENT,
        attributes={
            "gen_ai.operation.name": "invoke_agent",
            "gen_ai.agent.name": "weather-agent",
            "gen_ai.provider.name": "openai",
            "gen_ai.conversation.id": os.environ["OTEL_SERVICE_NAME"],
        },
    ):
        first = client.chat.completions.create(
            model="gpt-4o-mini",
            messages=messages,
            tools=tools,
            extra_headers={"X-Fake-Scenario": "tool-call"},
        )
        call = first.choices[0].message.tool_calls[0]
        messages.append(first.choices[0].message.model_dump(exclude_none=True))

        with tracer.start_as_current_span(
            f"execute_tool {call.function.name}",
            attributes={
                "gen_ai.operation.name": "execute_tool",
                "gen_ai.tool.name": call.function.name,
                "gen_ai.tool.call.id": call.id,
                "gen_ai.tool.type": "function",
                "gen_ai.tool.call.arguments": call.function.arguments,
            },
        ) as tool_span:
            result = "sunny, 21C"
            tool_span.set_attribute("gen_ai.tool.call.result", result)
        messages.append({"role": "tool", "tool_call_id": call.id, "content": result})

        final = client.chat.completions.create(model="gpt-4o-mini", messages=messages, tools=tools)
        print("answer:", final.choices[0].message.content)

    tracer_provider.shutdown()
    logger_provider.shutdown()
    meter_provider.shutdown()


main()
