// SPDX-License-Identifier: Apache-2.0
// Dashboard smoke test (acceptance LPE-11). Every test() is one check.
const fs = require('node:fs');
const { test, expect } = require('@playwright/test');

const baseURL = process.env.LPE11_BASE_URL;
const loginURL = process.env.LPE11_LOGIN_URL;
const token = process.env.LPE11_TOKEN;
const traceID = process.env.LPE11_TRACE_ID;
const sessionID = process.env.LPE11_SESSION_ID;
const consoleOut = process.env.LPE11_CONSOLE_ERRORS;
const STRIPPED = 'content not stored: set content.capture=on';

// Every console error, page error, CSP violation and failed same-origin request.
const problems = [];

async function watch(page, label) {
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      console.error('CSP violation: ' + e.violatedDirective + ' ' + e.blockedURI);
    });
  });
  page.on('console', (m) => {
    if (m.type() === 'error') problems.push(`${label}: console: ${m.text()}`);
  });
  page.on('pageerror', (e) => problems.push(`${label}: pageerror: ${e.message}`));
  page.on('requestfailed', (r) => problems.push(`${label}: request failed: ${r.url()}`));
  page.on('response', (r) => {
    if (r.url().startsWith(baseURL) && r.status() >= 400) {
      problems.push(`${label}: HTTP ${r.status()} ${r.url()}`);
    }
  });
}

function expectClean(from) {
  expect(problems.slice(from)).toEqual([]);
}

test.afterAll(() => {
  if (consoleOut) fs.writeFileSync(consoleOut, JSON.stringify(problems));
});

test.describe.configure({ mode: 'serial' });

test('unauthenticated / redirects to /login', async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const from = problems.length;
  await watch(page, 'unauth');
  const resp = await page.goto(baseURL + '/');
  expect(new URL(page.url()).pathname).toBe('/login');
  expect(resp.status()).toBe(200);
  await expect(page.locator('form[action="/login"] input[name="token"]')).toBeVisible();
  expectClean(from);
  await ctx.close();
});

test('login with the one-time code URL opens the Overview', async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const from = problems.length;
  await watch(page, 'code login');
  await page.goto(loginURL);
  expect(new URL(page.url()).pathname).toBe('/');
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  expectClean(from);
  await ctx.close();
});

test('login form rejects a wrong token', async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(baseURL + '/login');
  await page.fill('input[name="token"]', 'not-the-token');
  await page.click('form[action="/login"] button[type="submit"]');
  await expect(page.getByText('The token is not valid.')).toBeVisible();
  await page.goto(baseURL + '/');
  expect(new URL(page.url()).pathname).toBe('/login');
  await ctx.close();
});

let page;
let ctx;

test('login form with the API token opens the Overview', async ({ browser }) => {
  ctx = await browser.newContext();
  page = await ctx.newPage();
  const from = problems.length;
  await watch(page, 'token login');
  await page.goto(baseURL + '/login');
  await page.fill('input[name="token"]', token);
  await page.click('form[action="/login"] button[type="submit"]');
  await expect(page).toHaveURL(baseURL + '/');
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  expectClean(from);
});

test('Overview shows tiles and charts', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/');
  await expect(page.locator('.tiles .tile')).toHaveCount(5);
  await expect(page.locator('.tile-label', { hasText: 'Requests' })).toBeVisible();
  await expect(page.locator('.charts figure')).toHaveCount(3);
  await expect(page.locator('.notice', { hasText: 'No LLM spans' })).toHaveCount(0);
  // uPlot draws a canvas into each chart once the deferred scripts ran.
  await expect(page.locator('.chart canvas').first()).toBeVisible();
  expectClean(from);
});

test('Traces lists the fixture traces', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/traces');
  await expect(page.getByRole('heading', { name: 'Traces', level: 1 })).toBeVisible();
  const link = page.locator(`a[href="/traces/${traceID}"]`).first();
  await expect(link).toBeVisible();
  expect(await page.locator('table.data tbody tr').count()).toBeGreaterThanOrEqual(3);
  expectClean(from);
});

test('Trace detail shows the nested agent tree with tool spans', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/traces/' + traceID);
  const rows = page.locator('.waterfall li');
  await expect(rows.first()).toBeVisible();
  expect(await rows.count()).toBeGreaterThanOrEqual(8);
  await expect(page.locator('.waterfall .kind-agent').first()).toBeVisible();
  await expect(page.locator('.waterfall .kind-llm').first()).toBeVisible();
  await expect(page.locator('.waterfall .kind-tool').first()).toBeVisible();
  expect(await page.locator('.waterfall .kind-tool').count()).toBeGreaterThanOrEqual(2);
  expect(await page.locator('.waterfall .kind-agent').count()).toBeGreaterThanOrEqual(2);
  // Nesting: root at depth 0, the nested sub-agent's tool call at depth >= 3.
  await expect(page.locator('.waterfall li.depth-0')).toHaveCount(1);
  await expect(page.locator('.waterfall li.depth-1').first()).toBeVisible();
  await expect(page.locator('.waterfall li.depth-2').first()).toBeVisible();
  await expect(page.locator('.waterfall li.depth-3').first()).toBeVisible();
  const nested = page.locator('.waterfall li.depth-3', { hasText: 'execute_tool fetch_page' });
  await expect(nested.locator('.kind-tool')).toBeVisible();
  expectClean(from);
});

test('selecting the stripped span shows the content-not-stored hint', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/traces/' + traceID);
  await page.locator('.waterfall a.wrow', { hasText: 'chat gpt-4o' }).first().click();
  const panel = page.locator('#span-panel');
  await expect(panel).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'chat gpt-4o', level: 2 })).toBeVisible();
  await expect(panel.getByText(STRIPPED, { exact: true })).toBeVisible();
  expectClean(from);
});

test('selecting a tool span updates the side panel', async () => {
  const from = problems.length;
  await page.locator('.waterfall a.wrow', { hasText: 'execute_tool search_web' }).first().click();
  const panel = page.locator('#span-panel');
  await expect(panel.getByRole('heading', { name: 'execute_tool search_web', level: 2 })).toBeVisible();
  expectClean(from);
});

test('Sessions lists sessions and the session detail loads', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/sessions');
  await expect(page.getByRole('heading', { name: 'Sessions', level: 1 })).toBeVisible();
  expect(await page.locator('table.data tbody tr').count()).toBeGreaterThanOrEqual(2);
  await page.locator(`a[href="/sessions/${sessionID}"]`).click();
  await expect(page.getByRole('heading', { name: 'Session ' + sessionID, level: 1 })).toBeVisible();
  await expect(page.locator('section.timeline a.wrow').first()).toBeVisible();
  await expect(page.locator(`section.timeline a[href="/traces/${traceID}"]`)).toBeVisible();
  expectClean(from);
});

test('Models lists the fixture models', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/models');
  await expect(page.getByRole('heading', { name: 'Models', level: 1 })).toBeVisible();
  await expect(page.locator('table.data tbody th', { hasText: 'gpt-4o' }).first()).toBeVisible();
  await expect(page.locator('table.data tbody th', { hasText: 'claude-sonnet-4-5' })).toBeVisible();
  expectClean(from);
});

test('logout ends the session', async () => {
  const from = problems.length;
  await page.goto(baseURL + '/');
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.goto(baseURL + '/traces');
  expect(new URL(page.url()).pathname).toBe('/login');
  await page.goto(baseURL + '/traces/' + traceID);
  expect(new URL(page.url()).pathname).toBe('/login');
  expectClean(from);
  await ctx.close();
});
