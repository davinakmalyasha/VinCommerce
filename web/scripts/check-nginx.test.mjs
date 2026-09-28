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
  try {
    const out = execFileSync(process.execPath, [checker], { encoding: 'utf8' })
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
    name: 'Host header forwarding removed from /docs',
    expect: /does not forward Host/i,
    mutate: (s) => s.replace(/(location = \/docs \{[^}]*?)proxy_set_header Host \$host;\s*\n/s, '$1'),
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
