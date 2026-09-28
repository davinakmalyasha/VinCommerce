/**
 * Structural validation for web/nginx.conf.
 *
 * This is NOT a substitute for `nginx -t` — that requires the binary. It
 * exists because the realistic failure modes in this file are structural, and
 * all of them are silent:
 *
 *   1. nginx's `add_header` is inherited ONLY if the child block defines no
 *      `add_header` of its own. One `add_header Cache-Control` inside a
 *      location therefore silently discards every header set at server level.
 *      That is exactly what left the HTML document and every hashed asset
 *      served with no CSP and no HSTS.
 *   2. Every `include` path must exist in the image, or nginx refuses to boot.
 *   3. `listen` must match the port the Dockerfile EXPOSEs and compose
 *      publishes, or nothing connects.
 *   4. Braces must balance, and no directive may be stranded outside a block.
 *
 * Run: node scripts/check-nginx.mjs
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const confPath = path.join(root, 'nginx.conf')
const dockerfilePath = path.join(root, 'Dockerfile')
const headersPath = path.join(root, 'security-headers.conf')

const errors = []
const warnings = []
const fail = (m) => errors.push(m)
const warn = (m) => warnings.push(m)

const conf = fs.readFileSync(confPath, 'utf8')

// Strip comments so brace counting is not fooled by prose.
//
// `#` comments run to end of line. For block comments the opening marker must
// start a line: an unanchored pattern also matches the two-character sequence
// inside a CSP value such as https://*.midtrans.com, and then consumes
// everything up to the next closing marker — silently deleting most of the
// directive it thought it was tidying.
const stripComments = (s) =>
  s
    .replace(/#.*$/gm, '')
    .replace(/^[ \t]*\/\*[\s\S]*?\*\/[ \t]*$/gm, '')

const body = stripComments(conf)

/** Extract `location` blocks nested inside `server`, with their line ranges. */
function parseLocations(src) {
  const lines = src.split('\n')
  const out = []
  let depth = 0
  let cur = null
  const locRe = /^\s*location\s+([^{]+?)\s*\{/

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const before = depth

    for (const ch of line) {
      if (ch === '{') {
        depth++
        // depth 1 -> 2 means we just opened a block directly inside server{}.
        if (before === 1 && depth === 2) {
          const m = line.match(locRe)
          cur = m
            ? { name: m[1].trim(), body: '', startLine: i + 1 }
            : { name: '(unnamed)', body: '', startLine: i + 1 }
        }
      } else if (ch === '}') {
        // Decrement BEFORE the test: the test is about the depth *after* this
        // brace closes the block.
        depth--
        if (before === 2 && depth === 1 && cur) {
          cur.body = cur.body.trimEnd()
          out.push(cur)
          cur = null
        }
      }
    }

    // A single-line location block (`location /x { add_header ... }`) has its
    // directives on the opening line, so that line has to be part of the body.
    // Excluding it made the add_header-inheritance check silently pass on
    // exactly the shape it exists to catch.
    const openedHere = before === 1 && depth === 2
    if (cur && !openedHere) cur.body += line + '\n'
    else if (cur && openedHere && !/^\s*location\b/.test(line.trim())) {
      // Single-line block: capture the part after the opening brace.
      const brace = line.indexOf('{')
      if (brace >= 0) cur.body += line.slice(brace + 1) + '\n'
    }
  }
  return out
}

/** Extract the argument text of every `add_header` directive.
 *
 * Deliberately line-based: every directive in this config is one per line, and
 * a hand-rolled character scanner is a trap here. A naive
 * /add_header\s+([^;]+);/ stops at the first semicolon, which for
 * Content-Security-Policy is *inside* the quoted value
 * ("default-src 'self'; base-uri 'self'; ..."), so the real terminator is never
 * reached and the directive looks like it is missing its flags. A quote-aware
 * scanner is worse still: the CSP contains both quote characters
 * (`'self'` inside "..."), and any mistake in the state machine makes it run
 * past the end of the file and swallow every following directive.
 *
 * The one assumption is that an add_header fits on one line. That holds for
 * this file, and a violation would show up here as a false error rather than
 * as a silently missed header.
 */
function addHeaderDirectives(src) {
  return src
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l.startsWith('add_header'))
    .map((l) => l.slice('add_header'.length).replace(/;\s*$/, '').trim())
}

// --- 1. brace balance -------------------------------------------------------
{
  const opens = (body.match(/\{/g) || []).length
  const closes = (body.match(/\}/g) || []).length
  if (opens !== closes) fail(`unbalanced braces: ${opens} '{' vs ${closes} '}'`)
}

// --- 2. add_header inheritance ---------------------------------------------
const locations = parseLocations(body)
if (locations.length === 0) fail('no location blocks found — parser may be broken')

const headerInclude = /include\s+\/etc\/nginx\/snippets\/security-headers\.conf\s*;/
for (const loc of locations) {
  const hasAddHeader = /^\s*add_header\s/m.test(loc.body)
  if (hasAddHeader && !headerInclude.test(loc.body)) {
    fail(
      `location "${loc.name}" (nginx.conf:${loc.startLine}) defines add_header but does not ` +
        `include security-headers.conf — it will DISCARD the server-level headers`,
    )
  }
}

// --- 3. include paths exist -------------------------------------------------
for (const m of conf.matchAll(/^\s*include\s+(\S+)\s*;/gm)) {
  const inc = m[1]
  if (!inc.startsWith('/etc/nginx/')) continue
  // Map the in-image path back to a repo file.
  const repoFile =
    inc === '/etc/nginx/snippets/security-headers.conf'
      ? headersPath
      : inc === '/etc/nginx/snippets/brotli.conf'
        ? path.join(root, 'nginx-brotli.conf')
        : null
  if (repoFile && !fs.existsSync(repoFile)) {
    fail(`include "${inc}" has no source file in the repo (expected ${path.basename(repoFile)})`)
  }
  if (inc.endsWith('security-headers.conf') && !fs.existsSync(headersPath)) {
    fail('security-headers.conf is missing — nginx will refuse to start')
  }
}

// --- 4. listen / EXPOSE / compose port agreement ---------------------------
{
  const listen = [...conf.matchAll(/^\s*listen\s+(\d+)/gm)].map((m) => m[1])
  const df = fs.readFileSync(dockerfilePath, 'utf8')
  const expose = [...df.matchAll(/^\s*EXPOSE\s+(\d+)/gm)].map((m) => m[1])

  // Take the LAST USER in the Dockerfile: that is the final image stage. The
  // first match would let a builder stage's USER mask a root runtime stage.
  const users = [...df.matchAll(/^\s*USER\s+(\S+)/gm)].map((m) => m[1])
  const user = users.length ? users[users.length - 1] : null

  if (listen.length === 0) fail('no listen directive found')
  for (const p of listen) {
    if (expose.length && !expose.includes(p)) {
      fail(`listen ${p} but Dockerfile EXPOSEs ${expose.join(',')} — nothing can connect`)
    }
    if (Number(p) < 1024 && user && user[1] !== 'root') {
      fail(
        `listen ${p} is a privileged port but the final image stage runs as ` +
          `USER ${user} — bind will fail with EACCES`,
      )
    }
  }

  // The published port must map the container port. compose is what actually
  // connects the two, so a mismatch here means nothing reaches nginx.
  for (const cf of ['../infra/compose/compose.full.yaml', '../infra/compose/compose.yaml']) {
    const p = path.resolve(root, '..', cf)
    if (!fs.existsSync(p)) continue
    const text = fs.readFileSync(p, 'utf8')
    for (const l of listen) {
      // A published mapping looks like "HOST:CONTAINER" or "HOST:CONTAINER/PROTO".
      const maps = [...text.matchAll(/"(\d+):(\d+)[\/]?"/g)].map((m) => m[2])
      if (maps.length && !maps.includes(l)) {
        warn(
          `${path.basename(p)} publishes ${maps.join(',')} but nginx listens on ${l} — ` +
            `check the port mapping`,
        )
      }
    }
  }

  if (!user) warn('Dockerfile has no USER directive — the runtime stage runs as root')
  else if (user === 'root') fail('Dockerfile final stage is USER root')
}

// --- 5. SSE specifics -------------------------------------------------------
{
  const sse = locations.find((l) => l.name.includes('/api/v1/stream/'))
  if (!sse) fail('no dedicated SSE location — proxy_buffering off would have to be global')
  else {
    if (!/proxy_buffering\s+off\s*;/.test(sse.body)) {
      fail('SSE location does not disable proxy_buffering — streams will appear to hang')
    }
    if (!/proxy_http_version\s+1\.1\s*;/.test(sse.body)) {
      fail('SSE location does not pin proxy_http_version 1.1')
    }
    if (!/Connection\s+""\s*;/.test(sse.body)) fail('SSE location missing Connection "" header')
  }
  // The blanket /api/ must NOT disable buffering for ordinary JSON.
  const api = locations.find((l) => l.name === '/api/')
  if (api && /proxy_buffering\s+off\s*;/.test(api.body)) {
    fail('/api/ disables proxy_buffering for every JSON response (throughput regression)')
  }
}

// --- 6. proxy_set_header completeness --------------------------------------
{
  const proxied = locations.filter((l) => /proxy_pass\s+http/.test(l.body))
  for (const loc of proxied) {
    if (!/proxy_set_header\s+Host\s/.test(loc.body)) {
      fail(`proxied location "${loc.name}" does not forward Host — the upstream sees its own name`)
    }
    if (!/proxy_set_header\s+X-Real-IP\s/.test(loc.body)) {
      fail(`proxied location "${loc.name}" does not forward X-Real-IP — the client IP is lost`)
    }
    if (!/proxy_set_header\s+X-Forwarded-For\s/.test(loc.body)) {
      fail(`proxied location "${loc.name}" does not forward X-Forwarded-For`)
    }
  }
  if (proxied.length === 0) fail('no proxied locations found — parser may be broken')
}

// --- 7. rate limiting is not on streams ------------------------------------
{
  const sse = locations.find((l) => l.name.includes('/api/v1/stream/'))
  if (sse && /limit_req\s/.test(sse.body)) {
    fail('SSE location is rate limited — one stream legitimately serves hundreds of events')
  }
  if (!/limit_req_zone\s/.test(conf)) warn('no edge rate limit configured')
}

// --- 8. brotli must not be a hard dependency -------------------------------
{
  // ngx_brotli is not in the official images; a bare `brotli on;` breaks -t.
  if (/^\s*brotli\s+on\s*;/m.test(body)) {
    fail('brotli is enabled inline but ngx_brotli is not in the official nginx images — nginx -t will fail')
  }
  const brotliSrc = path.join(root, 'nginx-brotli.conf')
  if (fs.existsSync(brotliSrc)) {
    const df = fs.readFileSync(dockerfilePath, 'utf8')
    if (!/with-brotli/.test(df)) {
      warn('nginx-brotli.conf exists but the Dockerfile has no module detection guard')
    }
  }
}

// --- 9. the security header set is complete --------------------------------
{
  if (!fs.existsSync(headersPath)) {
    fail('security-headers.conf missing')
  } else {
    const hRaw = fs.readFileSync(headersPath, 'utf8')
    // Presence checks MUST run against the comment-stripped text. The raw file
    // mentions several header names in its own prose ("no CSP, no HSTS and no
    // X-Content-Type-Options"), so checking the raw file let a deleted header
    // still pass — a false pass in the one place this file exists to be
    // authoritative.
    const h = stripComments(hRaw)
    for (const h2 of [
      'Content-Security-Policy',
      'Strict-Transport-Security',
      'X-Content-Type-Options',
      'X-Frame-Options',
      'Referrer-Policy',
    ]) {
      if (!h.includes(h2)) fail(`security-headers.conf is missing ${h2}`)
    }
    // Every add_header should be `always`, or it is dropped on error responses.
    const withoutAlways = addHeaderDirectives(h).filter((d) => !/\balways\s*$/.test(d))
    if (withoutAlways.length) {
      fail(
        `${withoutAlways.length} add_header directive(s) lack 'always' and are dropped on 4xx/5xx: ` +
          withoutAlways.map((d) => d.slice(0, 40)).join(', '),
      )
    }
  }
}

// --- report ----------------------------------------------------------------
for (const w of warnings) console.warn(`WARN  ${w}`)
for (const e of errors) console.error(`FAIL  ${e}`)

const ok = errors.length === 0
console.log(
  `\n${ok ? 'PASS' : 'FAIL'}: ${locations.length} locations, ${warnings.length} warning(s), ${errors.length} error(s)`,
)
console.log('Note: this is a structural check, not `nginx -t`. Run the docker job in CI for that.')
process.exit(ok ? 0 : 1)
