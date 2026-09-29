#!/usr/bin/env node
/**
 * Import-boundary checks for the Go backend.
 *
 * The architecture in this repo is described in docs/ARCHITECTURE.md as
 * "dependencies point inward: handler -> service -> repository". That was
 * aspirational for a long time and four specific places broke it:
 *
 *   1. httpapi/handler/seo.go imported internal/repository directly, so a
 *      transport type held two concrete repositories and a cache store and
 *      built the sitemap itself.
 *   2. httpapi/handler/admin_ops.go imported internal/repository AND defined
 *      the feature-flag middleware inside a handler, so a cross-cutting
 *      kill-switch policy lived in the transport layer.
 *   3. httpapi/handler/cart.go imported go-redis and implemented checkout
 *      idempotency in a handler with raw GET/SET, while the service it called
 *      never read the key it had been handed.
 *   4. internal/service/seller_service.go reached through a `Pool()` accessor
 *      to write raw SQL, and internal/service/payment_service.go passed
 *      tx.PgTx() into another repository's methods, so pgx.Tx appeared in 20+
 *      service-layer signatures.
 *
 * None of that is fixable by convention alone, and CI is the only place that
 * can hold a line. This script is that line.
 *
 * It is deliberately dependency-free (Node stdlib only) so it runs in the same
 * job as the build with no install step, and it fails the build rather than
 * warning -- a boundary that is advisory is not a boundary.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

const ROOT = new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const BACKEND = join(ROOT, 'backend')

/** @type {{dir:string, prefix:string, forbidden:{pkg:string, reason:string}[]}[]} */
const RULES = [
  {
    dir: 'internal/httpapi/handler',
    prefix: 'github.com/vincommerce/backend/internal/',
    forbidden: [
      {
        pkg: 'repository',
        reason:
          'a handler must not hold a concrete repository. Add a service method instead. ' +
          '(seo.go and admin_ops.go both did this; the latter also defined the feature-flag ' +
          'middleware in a handler.)',
      },
      {
        pkg: 'db',
        reason: 'a handler must not hold a connection pool. Only the health probe may, and that belongs in its own package.',
      },
    ],
  },
  {
    dir: 'internal/service',
    prefix: 'github.com/vincommerce/backend/internal/',
    forbidden: [
      {
        pkg: 'httpapi',
        reason: 'the service layer must not depend on the transport layer.',
      },
      {
        pkg: 'payments/midtrans',
        reason: 'services depend on the payments.Gateway interface, not a concrete adapter.',
      },
    ],
  },
  {
    dir: 'internal/repository',
    prefix: 'github.com/vincommerce/backend/internal/',
    forbidden: [
      {
        pkg: 'service',
        reason: 'a repository must not depend on a service.',
      },
      {
        pkg: 'httpapi',
        reason: 'a repository must not depend on the transport layer.',
      },
    ],
  },
  {
    dir: 'internal/domain',
    prefix: 'github.com/vincommerce/backend/internal/',
    forbidden: [
      {
        pkg: 'repository',
        reason: 'the domain layer is the innermost ring and imports nothing internal.',
      },
      {
        pkg: 'service',
        reason: 'the domain layer is the innermost ring and imports nothing internal.',
      },
      {
        pkg: 'httpapi',
        reason: 'the domain layer is the innermost ring and imports nothing internal.',
      },
    ],
  },
  {
    dir: 'cmd',
    prefix: 'github.com/vincommerce/backend/internal/',
    forbidden: [
      {
        pkg: 'repository',
        reason:
          'a binary must not construct repositories directly; it calls app.Build. ' +
          '(cmd/seed is the deliberate exception -- see ALLOW below.)',
      },
      {
        pkg: 'service',
        reason: 'a binary must not construct services directly; it calls app.Build.',
      },
    ],
  },
]

/**
 * cmd/seed builds partial graphs on purpose: it is a development fixture
 * loader, not a process that serves requests or schedules work, and it has to
 * construct data that spans services. It is the one place allowed to import
 * internal/service and internal/repository directly.
 *
 * handler/health.go is allowed to hold a connection pool: a readiness probe
 * genuinely has to ping the thing it is reporting on, and routing it through a
 * service layer would only add a service whose entire job is to forward the
 * ping. It is scoped to one file precisely because the exemption is
 * specific -- a new handler that wants the pool should have to be added here
 * deliberately rather than reaching for it by proximity to health.go.
 *
 * Compared as forward-slash paths so the set is platform-independent.
 */
const ALLOW = new Set(['cmd/seed/main.go', 'internal/httpapi/handler/health.go'])

/** Normalise a path to forward slashes for comparison against ALLOW. */
const asRepoPath = (p) => p.split(sep).join('/')

/**
 * Service-layer third-party restrictions.
 *
 * These are TYPE-level rules, not import-level rules. A service is allowed to
 * import pgx: `pgx.ErrNoRows` is how a repository distinguishes "the row does
 * not exist" from "the query failed", and matching on that sentinel is
 * legitimate. What is NOT legitimate is naming `pgx.Tx` in a service
 * signature, because that means the caller is being handed the transaction
 * rather than a repository method that participates in one -- which is how
 * OrderTx.PgTx() ended up spread across 20 call sites in payment_service.go
 * and how the unit of work lost a single owner.
 */
const SERVICE_FORBIDDEN_TYPES = [
  {
    type: 'pgx.Tx',
    reason:
      'pgx.Tx in a service signature means the repository abstraction has been escaped. ' +
      'Use *repository.OrderTx (or its Querier()) so the repository keeps ownership of the ' +
      'transaction boundary.',
  },
  {
    type: 'pgxpool.Pool',
    reason: 'a service must not see a connection pool; it goes through internal/repository',
  },
  {
    type: 'redis.Client',
    reason:
      'a service must reach Redis through internal/stream or internal/cache. ' +
      'handler/cart.go implemented checkout idempotency with raw GET/SET for exactly this reason.',
  },
  {
    type: 'db.Pool',
    reason: 'a service must not see a connection pool; it goes through internal/repository',
  },
]

function goFiles(dir) {
  const out = []
  let entries
  try {
    entries = readdirSync(dir)
  } catch {
    return out
  }
  for (const name of entries) {
    const full = join(dir, name)
    const st = statSync(full)
    if (st.isDirectory()) {
      out.push(...goFiles(full))
    } else if (name.endsWith('.go') && !name.endsWith('_test.go')) {
      out.push(full)
    }
  }
  return out
}

/** Extract the import block of a Go file. */
function importsOf(src) {
  const block = src.match(/^import\s*\(([\s\S]*?)^\)/m)
  if (!block) return []
  const body = block[1]
  const out = []
  // Quoted import paths, with or without a preceding alias.
  for (const m of body.matchAll(/"([^"]+)"/g)) {
    out.push({ alias: null, path: m[1], line: body.slice(0, m.index).split('\n').length })
  }
  return out
}

const violations = []

for (const rule of RULES) {
  const dir = join(BACKEND, rule.dir)
  for (const file of goFiles(dir)) {
    const rel = asRepoPath(relative(BACKEND, file))
    if (ALLOW.has(rel)) continue
    const src = readFileSync(file, 'utf8')
    for (const imp of importsOf(src)) {
      if (!imp.path.startsWith(rule.prefix)) continue
      for (const bad of rule.forbidden) {
        if (imp.path === rule.prefix + bad.pkg || imp.path.startsWith(rule.prefix + bad.pkg + '/')) {
          violations.push({ file: rel, path: imp.path, reason: bad.reason })
        }
      }
    }
  }
}

// Service-layer type restrictions.
for (const file of goFiles(join(BACKEND, 'internal', 'service'))) {
  const rel = asRepoPath(relative(BACKEND, file))
  const src = readFileSync(file, 'utf8')
  // Strip comments and string literals so a mention of a forbidden type in a
  // doc comment (which several of these files have, describing the very defect
  // this rule exists to prevent) is not reported as a violation.
  const code = src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/\/\/[^\n]*/g, ' ')
    .replace(/`[^`]*`/g, ' ')
    .replace(/"(?:[^"\\]|\\.)*"/g, ' ')

  for (const bad of SERVICE_FORBIDDEN_TYPES) {
    // A type reference: preceded by start-of-line/whitespace/comma/paren/bracket
    // and followed by a word boundary, and NOT preceded by a package selector
    // (so `pgx.ErrNoRows` is fine while `pgx.Tx` is not).
    const re = new RegExp(`(?<![.\\w])${bad.type.replace('.', '\\.')}\\b`)
    const m = code.match(re)
    if (m) {
      const line = code.slice(0, m.index).split('\n').length
      violations.push({
        file: `${rel}:${line}`,
        path: bad.type,
        reason: bad.reason,
      })
    }
  }
}

if (violations.length === 0) {
  console.log('PASS: import boundaries respected')
  console.log(`  ${RULES.length} rules over ${RULES.map((r) => r.dir).join(', ')}`)
  process.exit(0)
}

console.error(`FAIL: ${violations.length} import-boundary violation(s)\n`)
for (const v of violations) {
  console.error(`  ${v.file}`)
  console.error(`    imports ${v.path}`)
  console.error(`    ${v.reason}\n`)
}
process.exit(1)
