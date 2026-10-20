// SPDX-License-Identifier: Apache-2.0
// Playwright config for the dashboard smoke test (acceptance LPE-11).
const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: __dirname,
  testMatch: /.*\.spec\.js/,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30000,
  expect: { timeout: 10000 },
  reporter: [['json', { outputFile: process.env.LPE11_REPORT || 'test-results/lpe11.json' }]],
  outputDir: process.env.LPE11_OUTPUT_DIR || 'test-results/lpe11',
  use: {
    baseURL: process.env.LPE11_BASE_URL,
    browserName: 'chromium',
    headless: true,
  },
});
