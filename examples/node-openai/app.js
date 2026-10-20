// SPDX-License-Identifier: Apache-2.0
// OpenAI chat calls traced with the OpenTelemetry JS OpenAI instrumentation.
// The NodeSDK reads the standard OTEL_* environment variables (OTEL_*_EXPORTER=otlp): traces,
// metrics and logs all export OTLP/HTTP to OTEL_EXPORTER_OTLP_ENDPOINT. Loupe stores spans and discards metrics.
'use strict';

const { NodeSDK } = require('@opentelemetry/sdk-node');
const { OpenAIInstrumentation } = require('@opentelemetry/instrumentation-openai');
const { trace } = require('@opentelemetry/api');

const sdk = new NodeSDK({ instrumentations: [new OpenAIInstrumentation()] });
sdk.start();

async function main() {
  // openai must be required after the instrumentation is registered.
  const { OpenAI } = require('openai');
  const client = new OpenAI({ baseURL: process.env.FAKELLM_URL, apiKey: 'fake' });
  await trace.getTracer('example').startActiveSpan('node-openai-example', async (span) => {
    try {
      const reply = await client.chat.completions.create({
        model: 'gpt-4o-mini',
        messages: [{ role: 'user', content: 'say hello to loupe' }],
      });
      console.log('reply:', reply.choices[0].message.content);
    } finally {
      span.end();
    }
  });
}

main()
  .catch((err) => {
    console.error(err);
    process.exitCode = 1;
  })
  // shutdown flushes spans, log records and a final metrics export.
  .finally(() => sdk.shutdown());
