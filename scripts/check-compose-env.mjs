#!/usr/bin/env node
/**
 * Compose environment assertions.
 *
 * Three of the most severe deployment defects in this repository were compose
 * files that were missing one environment variable on one service. None of them
 * is detectable by running the stack, because the stack starts, reports healthy,
 * and serves traffic in all three cases. They are only visible in a production
 * configuration, and only in the ways that matter:
 *
 *   1. `APP_ENV` was set on `worker` but not on `api`. An operator running
 *      `APP_ENV=production docker compose up` got a production worker and a
 *      development API: cfg.IsDev() was true, the sandbox payment gateway was
 *      registered with its published webhook secret, and
 *      POST /payments/sandbox/orders/{id}/approve was mounted -- so any
 *      authenticated order owner could mark their own order paid, and anyone
 *      could forge a settlement webhook for any order. The config guard that
 *      refuses the sandbox gateway outside development never fired, because the
 *      environment was not production.
 *
 *   2. `REDIS_PASSWORD` was set on `worker` but not on `api`. The comment in
 *      the worker block documented the exact failure and the fix was applied
 *      there only. The moment an operator secured Redis, the API kept
 *      connecting unauthenticated: rate limits failed OPEN (the limiter treats
 *      any Redis error as "no limit", so /auth/login became unlimited
 *      brute-force), the SSE broker was dead, and the cache was a no-op.
 *
 *   3. Redis had no --requirepass at all, so anything on the compose network
 *      could reach it. stream.Broker.Publish is a plain PUBLISH to
 *      `user:<id>:events`, which makes that a fraud primitive: write a fake
 *      "escrow released" or "order shipped" event to any user id.
 *
 * This asserts on the compose source rather than on the rendered output,
 * because the rendered output is exactly what differs between a dev machine
 * (where interpolation supplies defaults) and a production operator's shell.
 *
 * Dependency-free Node stdlib, like check-import-boundaries.mjs and
 * check-nginx.mjs, so it runs in the existing CI job with no install step.
 */

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const COMPOSE_FILES = [
  'infra/compose/compose.full.yaml',
  'infra/compose/compose.yaml',
]

/** Keys every DB-touching service must declare, with why. */
const REQUIRED_SERVICE_ENV = {
  // NB: the service is named `api`. This key was `app` in the first draft, and
  // `serviceEnv` returns null for an unknown service, which the caller treats as
  // "not present in this file" and skips. So the whole api block was silently
  // unchecked and the guard reported PASS while every one of its rules was
  // inert -- the guard had a bug of exactly the class it exists to prevent.
  //
  // The mutation test is what caught it, and it caught it by asserting that
  // removing a real key produces a failure. A guard that has never been shown
  // to fail is a claim, not evidence.
  api: [
    { key: 'APP_ENV', why: 'absent, the API runs in development mode: the sandbox payment gateway is registered with its PUBLISHED webhook secret and POST /payments/sandbox/orders/{id}/approve is mounted, so any order owner can self-approve payment' },
    { key: 'DB_PASSWORD', why: 'absent, the API cannot connect to Postgres' },
    { key: 'REDIS_ADDR', why: 'absent, the API has no cache, no rate limiting and no SSE' },
    { key: 'REDIS_PASSWORD', why: 'absent, the API connects to Redis unauthenticated even when the server requires a password -- rate limits then fail OPEN' },
  ],
  worker: [
    { key: 'APP_ENV', why: 'absent, the worker runs in development mode' },
    { key: 'REDIS_ADDR', why: 'absent, the worker has no task queue' },
    { key: 'REDIS_PASSWORD', why: 'absent, every scheduled job silently stops the moment Redis requires a password' },
  ],
}

/** Things the redis service must do, with why. */
const REDIS_REQUIREMENTS = [
  { needle: '--requirepass', why: 'Redis accepts unauthenticated commands, so anything on the compose network can PUBLISH a fake order event to any user id via stream.Broker' },
  { needle: 'redis-cli -a', why: 'the healthcheck runs `redis-cli ping`, which returns NOAUTH once --requirepass is set; that marks a healthy server unhealthy and, because depends_on waits on it, stops the API and worker from starting at all' },
]

/**
 * Extract the environment keys declared for one service.
 *
 * Line-based rather than a block-slice-and-regex, because the obvious version
 * of this breaks in a way that is easy to miss: splitting the service block on
 * `/\n {4}[a-zA-Z]/` to isolate the environment section also matches the
 * `    environment:` header itself at offset 0, so every service reports zero
 * keys and the guard silently passes everything. A guard that cannot fail is
 * worse than no guard, so this walks lines and tracks indentation explicitly.
 *
 * It is still a targeted parser, not a YAML implementation: the question is
 * "which keys appear under services.<name>.environment", and
 * `docker compose config` in CI is what validates the YAML itself.
 */
function serviceEnv(text, service) {
  const lines = text.split(/\r?\n/)

  // 1. Find the service header: exactly two spaces of indent, then the name.
  let start = -1
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/^ {2}([a-z][a-zA-Z0-9_-]*):\s*(.*)$/)
    if (m && m[1] === service) {
      start = i
      break
    }
  }
  if (start === -1) return null

  // 2. The service block ends at the next two-space-indented key, or a
  //    top-level key (`volumes:`, `networks:`) with no indent.
  let end = lines.length
  for (let i = start + 1; i < lines.length; i++) {
    if (/^ {0,2}\S/.test(lines[i]) && !/^ {2}[a-z]/.test(lines[i])) {
      end = i
      break
    }
    if (/^ {2}[a-z][a-zA-Z0-9_-]*:\s*/.test(lines[i])) {
      end = i
      break
    }
  }
  const block = lines.slice(start, end)

  // 3. Find `    environment:` inside the block, then collect the keys indented
  //    deeper than it until the indentation comes back up.
  const envIdx = block.findIndex((l) => /^ {4}environment:\s*$/.test(l))
  if (envIdx === -1) return null

  const keys = new Set()
  for (let i = envIdx + 1; i < block.length; i++) {
    const line = block[i]
    if (line.trim() === '' || line.trimStart().startsWith('#')) continue
    // Anything indented 4 or less is the end of the environment section.
    const indent = line.length - line.trimStart().length
    if (indent <= 4) break
    const m = line.match(/^\s{6}([A-Za-z_][A-Za-z0-9_]*):/)
    if (m) keys.add(m[1])
  }
  return { keys, block: block.join('\n') }
}

const failures = []

for (const rel of COMPOSE_FILES) {
  let text
  try {
    text = readFileSync(join(ROOT, rel), 'utf8')
  } catch {
    continue // the minimal dev compose may not define the full topology
  }

  for (const [service, requirements] of Object.entries(REQUIRED_SERVICE_ENV)) {
    const found = serviceEnv(text, service)
    if (!found) continue // service not present in this file
    for (const req of requirements) {
      if (!found.keys.has(req.key)) {
        failures.push(`${rel}: service "${service}" does not set ${req.key} -- ${req.why}`)
      }
    }
  }

  const redis = serviceEnv(text, 'redis')
  if (redis) {
    for (const req of REDIS_REQUIREMENTS) {
      if (!redis.block.includes(req.needle)) {
        failures.push(`${rel}: service "redis" is missing ${req.needle} -- ${req.why}`)
      }
    }
  }
}

if (failures.length === 0) {
  console.log('PASS: compose service environment is complete')
  console.log(`  checked ${COMPOSE_FILES.length} file(s): APP_ENV, DB_PASSWORD, REDIS_ADDR, REDIS_PASSWORD on api+worker; --requirepass and an authenticated healthcheck on redis`)
  process.exit(0)
}

console.error(`FAIL: ${failures.length} compose environment problem(s)\n`)
for (const f of failures) {
  console.error(`  ${f}\n`)
}
process.exit(1)
