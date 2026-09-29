#!/usr/bin/env node
/**
 * Mutation test for scripts/check-compose-env.mjs.
 *
 * A guard that cannot fail is worse than no guard, because it is read as
 * evidence of something. This reintroduces each of the three real defects the
 * guard exists to catch and asserts the guard rejects every one.
 *
 * The three defects, for reference:
 *
 *   1. APP_ENV set on `worker` but not `api` -- produced a production worker
 *      and a development API, so the sandbox payment gateway (published webhook
 *      secret) was registered and POST /payments/sandbox/orders/{id}/approve
 *      was mounted.
 *   2. REDIS_PASSWORD set on `worker` but not `api` -- the worker comment
 *      documented this exact failure and the fix was applied only there.
 *   3. Redis healthcheck running an unauthenticated `redis-cli ping` while the
 *      server has --requirepass -- returns NOAUTH, marks a healthy server
 *      unhealthy, and because `depends_on: condition: service_healthy` waits on
 *      it, stops the API and worker from starting at all.
 *
 * Run: node scripts/check-compose-env.test.mjs
 */

import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const TARGET = join(ROOT, 'infra/compose/compose.full.yaml')
const GUARD = join(ROOT, 'scripts/check-import-boundaries.mjs').replace(
  'check-import-boundaries.mjs',
  'check-compose-env.mjs',
)

const original = readFileSync(TARGET, 'utf8')

/** Run the guard; return { code, out }. Never throws on a non-zero exit. */
function runGuard() {
  try {
    const out = execFileSync(process.execPath, [GUARD], { encoding: 'utf8', stdio: 'pipe' })
    return { code: 0, out }
  } catch (e) {
    return { code: e.status ?? 1, out: `${e.stdout ?? ''}${e.stderr ?? ''}` }
  }
}

/**
 * Remove a single `KEY: value` line from a named service's environment block.
 * Line-based, so it does not depend on what happens to be adjacent to the key.
 */
function removeEnvKey(text, service, key) {
  const lines = text.split(/\r?\n/)
  let start = -1
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/^ {2}([a-z][a-zA-Z0-9_-]*):\s*(.*)$/)
    if (m && m[1] === service) { start = i; break }
  }
  if (start === -1) throw new Error(`service ${service} not found -- is the compose file being renamed?`)

  let inEnv = false
  for (let i = start + 1; i < lines.length; i++) {
    if (/^ {0,2}\S/.test(lines[i]) && !/^ {2}[a-z]/.test(lines[i])) break
    if (/^ {2}[a-z][a-zA-Z0-9_-]*:\s*/.test(lines[i])) break
    if (/^ {4}environment:\s*$/.test(lines[i])) { inEnv = true; continue }
    if (inEnv) {
      const indent = lines[i].length - lines[i].trimStart().length
      if (lines[i].trim() && indent <= 4) break
      if (new RegExp(`^ {6}${key}:`).test(lines[i])) {
        lines.splice(i, 1)
        return lines.join('\n')
      }
    }
  }
  throw new Error(`key ${key} not found in service ${service} -- the mutation would be a no-op`)
}

const MUTATIONS = [
  {
    name: 'APP_ENV missing from api (production worker + development API)',
    why: 'the sandbox gateway and its published webhook secret become reachable',
    apply: (t) => removeEnvKey(t, 'api', 'APP_ENV'),
    expect: /service "api" does not set APP_ENV/,
  },
  {
    name: 'REDIS_PASSWORD missing from api (rate limits fail open)',
    why: 'the API connects to Redis unauthenticated; /auth/login becomes unlimited',
    apply: (t) => removeEnvKey(t, 'api', 'REDIS_PASSWORD'),
    expect: /service "api" does not set REDIS_PASSWORD/,
  },
  {
    name: 'REDIS_PASSWORD missing from worker (every scheduled job stops)',
    why: 'expired-order cancellation, escrow sweeps and cart recovery go silent',
    apply: (t) => removeEnvKey(t, 'worker', 'REDIS_PASSWORD'),
    expect: /service "worker" does not set REDIS_PASSWORD/,
  },
  {
    name: 'redis --requirepass removed (unauthenticated PUBLISH from the network)',
    why: 'anyone on the compose network can write fake order events to any user',
    apply: (t) => {
      if (!t.includes('      - --requirepass')) throw new Error('--requirepass not present -- mutation is a no-op')
      return t.replace(/^ {6}- --requirepass\r?\n {6}- "\$\{REDIS_PASSWORD[^\n]*\n/m, '')
    },
    expect: /service "redis" is missing --requirepass/,
  },
  {
    name: 'redis healthcheck reverted to an unauthenticated ping',
    why: 'NOAUTH makes a healthy server unhealthy, and depends_on then blocks the api and worker from starting',
    apply: (t) => {
      // The line in the compose file is YAML with escaped quotes:
      //   test: ["CMD-SHELL", "redis-cli -a \"$$REDIS_PASSWORD\" ping | grep -q PONG"]
      // so the mutation has to match the backslashes. A mutation that silently
      // fails to apply is worse than no mutation: the guard is reported as
      // "not detecting" a defect that was never actually introduced, which
      // reads as a guard problem and sends you looking in the wrong place.
      const needle = 'redis-cli -a \\"$$REDIS_PASSWORD\\" ping | grep -q PONG'
      if (!t.includes(needle)) {
        throw new Error(
          `healthcheck text not found (looked for ${JSON.stringify(needle)}) -- ` +
            'the mutation would be a no-op',
        )
      }
      return t.replace(needle, 'redis-cli ping')
    },
    expect: /service "redis" is missing redis-cli -a/,
  },
]

let passed = 0
let failed = 0

// Baseline: the unmutated file must PASS, or every mutation assertion below is
// meaningless.
try {
  const { code, out } = runGuard()
  if (code !== 0) {
    console.error('BASELINE FAILED: the guard rejects the real compose file\n')
    console.error(out)
    process.exit(1)
  }
  console.log('  baseline            the real compose file passes\n')
} finally {
  writeFileSync(TARGET, original)
}

for (const m of MUTATIONS) {
  let mutated
  try {
    mutated = m.apply(original)
  } catch (e) {
    console.error(`  FAIL  ${m.name}\n        mutation could not be applied: ${e.message}\n`)
    failed++
    continue
  }

  writeFileSync(TARGET, mutated)
  const { code, out } = runGuard()
  writeFileSync(TARGET, original)

  if (code === 0) {
    console.error(`  FAIL  ${m.name}`)
    console.error(`        the guard PASSED a file with the defect reintroduced`)
    console.error(`        why it matters: ${m.why}\n`)
    failed++
    continue
  }
  if (!m.expect.test(out)) {
    console.error(`  FAIL  ${m.name}`)
    console.error(`        the guard failed but for the wrong reason; expected ${m.expect}`)
    console.error(`        got:\n${out}\n`)
    failed++
    continue
  }
  console.log(`  ok    ${m.name}`)
  passed++
}

console.log(`\n${passed} mutation(s) detected, ${failed} missed`)
process.exit(failed === 0 ? 0 : 1)
