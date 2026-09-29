// Mutation test for check-nginx.mjs: deliberately reintroduce each bug the
// checker claims to catch, and assert it is actually reported.
//
// A validator that cannot fail is worse than no validator, so each case below
// removes or corrupts one specific thing and checks the expected error fires.
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'

const confPath = path.resolve('nginx.conf')
const original = fs.readFileSync(confPath, 'utf8')
const checker = path.resolve('scripts/check-nginx.mjs')

function run() {
  // stdio: ['ignore', 'pipe', 'pipe'] is load-bearing for legibility, not
  // correctness. The checker writes its findings to stderr and exits non-zero
  // when it finds one -- which is exactly what a SUCCESSFUL mutation should do.
  // Without piping, every one of those detections printed a bare "FAIL ..."
  // line to the terminal, interleaved with this suite's own real failure output,
  // so a green run looked like a wall of failures. A test harness that makes a
  // pass look like a fail trains you to ignore it.
  const opts = { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }
  try {
    const out = execFileSync(process.execPath, [checker], opts)
    return { code: 0, out }
  } catch (e) {
    return { code: e.status ?? 1, out: (e.stdout ?? '') + (e.stderr ?? '') }
  }
}

const cases = [
  {
    name: 'security-headers include removed from `location /` (the add_header inheritance bug)',
    expect: /location "\/".*does not include security-headers|discard/i,
    mutate: (s) =>
      s.replace(
        /(location \/ \{\s*\n\s*try_files \$uri \$uri\/ \/index\.html;\s*\n)\s*include \/etc\/nginx\/snippets\/security-headers\.conf;/,
        '$1',
      ),
  },
  {
    name: 'security-headers include removed from `location /assets/`',
    expect: /assets[\s\S]*security-headers|discard/i,
    mutate: (s) =>
      s.replace(
        /(location \/assets\/ \{\s*\n\s*try_files \$uri =404;\s*\n)\s*include \/etc\/nginx\/snippets\/security-headers\.conf;/,
        '$1',
      ),
  },
  {
    name: 'proxy_buffering off removed from the SSE location',
    expect: /proxy_buffering/i,
    mutate: (s) => s.replace(/^\s*proxy_buffering off;\s*$/m, ''),
  },
  {
    name: 'proxy_buffering off put back on the blanket /api/ location',
    expect: /disables proxy_buffering for every JSON/i,
    mutate: (s) => s.replace(/(location \/api\/ \{[^}]*?)(proxy_set_header X-Forwarded-Proto \$scheme;)/, '$1$2\n        proxy_buffering off;'),
  },
  {
    // Was `/docs`. The Swagger UI moved to `/api-docs` because proxying `/docs`
    // shadowed the SPA's own `/docs` route and `/docs/api` quickstart page, so
    // the product's documentation was unreachable behind the edge in every
    // containerised deployment. This mutation caught that move, which is the
    // suite working: a rename that invalidated a test meant the test was
    // pointing at something that no longer existed.
    name: 'Host header forwarding removed from /api-docs',
    expect: /does not forward Host/i,
    mutate: (s) => s.replace(/(location = \/api-docs \{[^}]*?)proxy_set_header Host \$host;\s*\n/s, '$1'),
  },
  {
    name: 'X-Forwarded-For removed from /uploads/',
    expect: /does not forward X-Forwarded-For/i,
    mutate: (s) => s.replace(/(location \/uploads\/ \{[^}]*?)proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;\s*\n/s, '$1'),
  },
  {
    name: 'rate limiting applied to the SSE location',
    expect: /SSE location is rate limited/i,
    mutate: (s) => s.replace(/(location \^~ \/api\/v1\/stream\/ \{[^}]*?)(\n    \})/s, '$1\n        limit_req zone=api_per_ip burst=60 nodelay;$2'),
  },
  {
    name: 'brotli enabled inline without the module',
    expect: /brotli is enabled inline/i,
    mutate: (s) => s.replace(/^(\s*gzip on;)/m, '    brotli on;\n    brotli_types text/css application/javascript application/json;\n$1'),
  },
  {
    name: 'privileged port bound while running as non-root',
    expect: /privileged port/i,
    mutate: (s) => s.replace(/listen 8080;/, 'listen 80;'),
  },
  {
    name: 'X-Content-Type-Options removed from the header snippet',
    // The snippet's own prose mentions this header name, so a presence check
    // against the RAW file (comments included) let this pass. This is the
    // false pass that made the checker untrustworthy.
    expect: /missing X-Content-Type-Options/i,
    file: 'security-headers.conf',
    mutate: (s) =>
      s.replace(/^add_header X-Content-Type-Options[^\n]*\n/m, ''),
  },
  {
    name: 'final image stage switched back to USER root',
    expect: /final stage is USER root/i,
    file: 'Dockerfile',
    mutate: (s) => s.replace(/^USER nginx\s*$/m, 'USER root'),
  },
  {
    name: 'add_header without `always` (dropped on 4xx/5xx)',
    expect: /lack 'always'/i,
    file: 'security-headers.conf',
    mutate: (s) => s.replace(/"nosniff" always;/, '"nosniff";'),
  },
  {
    // Added with the edge rate limiter. `limit_req` keys on $binary_remote_addr,
    // which is the DIRECT PEER -- so behind the TLS terminator every visitor
    // shares one 30r/s bucket and the edge limiter becomes a self-inflicted
    // denial of service that looks like a traffic spike. `realip` runs in the
    // preaccess phase, before limit_req, so the bucketing uses the real address.
    name: 'real_ip_header removed (every visitor shares one rate-limit bucket behind a proxy)',
    expect: /real_ip_header/i,
    mutate: (s) => s.replace(/^\s*real_ip_header\s+X-Forwarded-For;\s*$/m, ''),
  },
  {
    // 0.0.0.0/0 in set_real_ip_from is the classic mistake: it lets ANY client
    // set X-Forwarded-For and therefore choose its own rate-limit bucket and its
    // own audit-logged client IP, defeating per-IP limiting and corrupting the
    // audit trail in one move.
    name: 'set_real_ip_from widened to 0.0.0.0/0 (client-controlled client IP)',
    expect: /0\.0\.0\.0\/0|set_real_ip_from/i,
    mutate: (s) =>
      s.replace(
        /set_real_ip_from 172\.16\.0\.0\/12;/,
        'set_real_ip_from 0.0.0.0/0;\n    set_real_ip_from 172.16.0.0/12;',
      ),
  },
  {
    // Without `resolver`, nginx resolves `api` once at config-parse time, so
    // `docker compose up -d --force-recreate api` gives the container a new IP
    // and every proxied request 502s until nginx is reloaded.
    name: 'resolver removed (nginx 502s after any API container recreate)',
    expect: /resolver/i,
    mutate: (s) => s.replace(/^resolver 127\.0\.0\.11[^\n]*\n/m, ''),
  },
  {
    // The 429 body must be JSON matching the app's envelope, or lib/api.ts
    // surfaces a JSON parse error instead of "terlalu banyak permintaan".
    name: 'rate-limit response body is not the app error envelope',
    expect: /application\/json|429|envelope/i,
    mutate: (s) => s.replace(/default_type application\/json;/, 'default_type text/html;'),
  },
  {
    // The @too_many_requests location defines its own add_header (Retry-After),
    // and nginx's inheritance rule is that a child defining ANY add_header
    // discards the entire inherited set. This exact mistake was made and
    // caught by the checker when the location was first added.
    name: 'security-headers include dropped from the @too_many_requests location',
    expect: /@too_many_requests.*does not include security-headers|discard/i,
    mutate: (s) =>
      s.replace(
        /(location @too_many_requests \{)\n(\s*include \/etc\/nginx\/snippets\/security-headers\.conf;)/,
        '$1',
      ),
  },
  {
    // Swagger at /docs shadowed the SPA's own /docs and /docs/api routes, so
    // the product's documentation pages were unreachable behind the edge. The
    // commit that fixed the dev-mode 404 fixed the wrong layer.
    name: 'Swagger moved back to /docs (shadows the SPA documentation routes)',
    expect: /\/docs|shadow/i,
    mutate: (s) => s.replace(/location = \/api-docs \{/, 'location = /docs {'),
  },
]

let pass = 0
let fail = 0

// Baseline: the real config must pass.
{
  fs.writeFileSync(confPath, original)
  const r = run()
  if (r.code !== 0) {
    console.error(`FAIL  baseline: unmutated config reported errors\n${r.out}`)
    fail++
  } else {
    console.log('ok    baseline (unmutated config passes)')
    pass++
  }
}

for (const c of cases) {
  // Some cases mutate a second file; restore both afterwards.
  const target = c.file ? path.resolve(c.file) : confPath
  const origTarget = fs.readFileSync(target, 'utf8')

  const mutated = c.mutate(origTarget)
  if (mutated === origTarget) {
    console.error(`FAIL  ${c.name}\n        mutation did not change the file (regex no longer matches)`)
    fail++
    continue
  }
  fs.writeFileSync(target, mutated)
  const r = run()
  fs.writeFileSync(target, origTarget)

  if (r.code !== 0 && c.expect.test(r.out)) {
    console.log(`ok    detects: ${c.name}`)
    pass++
  } else {
    console.error(
      `FAIL  missed: ${c.name}\n        exit=${r.code} expected pattern ${c.expect}\n        output: ${r.out.trim().slice(0, 400)}`,
    )
    fail++
  }
}

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
