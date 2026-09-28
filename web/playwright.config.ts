import { defineConfig } from '@playwright/test'

const isCI = !!process.env.CI

/**
 * Self-contained test stack: `npx playwright test` brings up everything the
 * suite needs, so it cannot be run against a half-wired environment.
 *
 * This replaces a CI step that backgrounded `go run ./cmd/seed`, `go run
 * ./cmd/api` and `npm run dev` with `&` inside a single shell and then relied
 * on those processes surviving into the NEXT step. Two distinct failures came
 * out of that:
 *
 *   1. `go run ./cmd/seed &` was never awaited. The readiness poll only waits
 *      for the API, which starts happily against an empty database, so
 *      Playwright began testing against a half-seeded DB — the single largest
 *      source of flake in this suite.
 *   2. Backgrounded children are killed when the step's shell exits, so the
 *      next step raced process teardown.
 *
 * Playwright starts webServer entries in order and waits for each `url` to
 * answer before starting the next, which is the ordering guarantee that was
 * missing.
 *
 * `reuseExistingServer: !isCI` keeps the fast local loop (a developer with the
 * API already up is not made to restart it) while guaranteeing a clean stack on
 * a runner, where a leftover process from an earlier attempt would otherwise be
 * silently adopted — and then tested against.
 */

/**
 * The seed runs as `&&` inside the API's own command rather than as its own
 * webServer entry. A standalone entry cannot work: every webServer entry must
 * declare a `url` for Playwright to poll, and nothing serves HTTP until the API
 * is up, so a seed-only entry would either time out or be reported as "process
 * exited early" before the API ever started. Chaining them is what actually
 * delivers the guarantee the standalone entry was meant to provide: the API
 * process — and therefore the readiness URL Playwright waits on — does not
 * exist until the seed has finished.
 */
const apiEnv = {
  ...process.env,
  DB_HOST: process.env.DB_HOST ?? '127.0.0.1',
  DB_USER: process.env.DB_USER ?? 'vincom',
  DB_PASSWORD: process.env.DB_PASSWORD ?? 'vincom_dev',
  DB_NAME: process.env.DB_NAME ?? 'vincom',
  REDIS_ADDR: process.env.REDIS_ADDR ?? '127.0.0.1:6379',
}

export default defineConfig({
  testDir: './e2e',
  // 30s is not enough for a cold Vite transform of a lazy route plus an API
  // round trip on a loaded CI runner.
  timeout: 60_000,
  expect: { timeout: 15_000 },
  // These tests mutate shared database state — a pending order is consumed, cart
  // rows are written, moderation reports accumulate — so they must not run
  // concurrently. Parallel workers have them trampling each other's fixtures,
  // and the second worker to reach the payments test finds the pending order
  // already confirmed.
  workers: 1,
  retries: isCI ? 1 : 0,
  // A committed `test.only` is a silent coverage hole, not a speedup.
  forbidOnly: isCI,
  reporter: process.env.CI
    ? [['github'], ['html', { open: 'never' }]]
    : [['list']],
  use: {
    baseURL: 'http://localhost:5173',
    headless: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  webServer: [
    {
      // `go run ./cmd/seed && go run ./cmd/api` — see the note above on why the
      // seed is chained rather than a separate entry.
      command: 'go run ./cmd/seed && go run ./cmd/api',
      // Resolved relative to this configuration file's directory.
      cwd: '../backend',
      // /health/ready, not /health/live: it reports 200 only once BOTH
      // Postgres and Redis answer, so no test can start against a half-wired
      // API even if the seed somehow returned early.
      url: 'http://localhost:8080/api/v1/health/ready',
      reuseExistingServer: !isCI,
      // Generous: `go run` recompiles on a cold module cache, and goose applies
      // 40 migrations.
      timeout: 300_000,
      env: apiEnv,
    },
    {
      command: 'npm run dev -- --port 5173 --strictPort',
      cwd: '.',
      url: 'http://localhost:5173/',
      reuseExistingServer: !isCI,
      timeout: 180_000,
    },
  ],
})
