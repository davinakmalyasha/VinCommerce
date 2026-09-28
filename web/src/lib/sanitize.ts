// Allowlist-based HTML sanitizer for admin-authored article/legal content.
// The backend stores free-text HTML; rendering it raw would let a compromised
// or lower-trust admin account plant stored XSS on public, unauthenticated
// pages (/help/:slug and /legal/:doc).
//
// Server-side sanitization is still recommended as defense in depth — this is
// the client-side half of a two-layer control, and the half that is hardest to
// get right, because DOMParser decodes entities before we inspect them.

const ALLOWED_TAGS = new Set([
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'p', 'br', 'hr', 'blockquote', 'pre', 'code',
  'ul', 'ol', 'li',
  'strong', 'b', 'em', 'i', 'u', 's', 'del', 'mark', 'small', 'sub', 'sup',
  'a', 'img', 'figure', 'figcaption',
  'table', 'thead', 'tbody', 'tfoot', 'tr', 'th', 'td',
  'div', 'span',
])

// Elements dropped outright: their contents are not prose, so unwrapping them
// would leak script/style text into the page.
const DROPPED_TAGS = new Set([
  'script', 'style', 'iframe', 'object', 'embed', 'form', 'svg', 'math',
  'link', 'meta', 'base', 'noscript', 'template', 'frame', 'frameset',
  'applet', 'audio', 'video', 'source', 'track', 'canvas', 'map', 'portal',
])

/**
 * Raw-text / RCDATA / foreign-content containers, removed from the raw input
 * string BEFORE it is parsed.
 *
 * Removing them from the parsed tree is not enough, and a test proved it: the
 * HTML parser promotes the CONTENT of these elements into the tree when the
 * container is discarded, so `child.remove()` on a <noscript> left a live
 * <img> behind. Stripping the element and its content from the string first
 * removes the promotion entirely.
 *
 * Deliberately fail-closed: a `<` inside the content can end the match early,
 * and the worst outcome is that more markup is removed, not less.
 */
const CONTAINER_WITH_CONTENT =
  /<(script|style|iframe|object|embed|form|svg|math|noscript|template|frame|frameset|applet|plaintext|xmp|textarea)\b[^>]*>[\s\S]*?<\/\s*\1\s*>/gi
// Unclosed variants of the same, where the element runs to end of input.
const UNCLOSED_CONTAINER =
  /<(script|style|iframe|object|embed|form|svg|math|noscript|template|plaintext|xmp|textarea)\b[^>]*>[\s\S]*$/gi

const URL_ATTRS = new Set(['href', 'src'])

// Schemes an author may legitimately link to. Everything else is rejected.
// `data:` is deliberately absent: inline images must go through the media
// endpoint, not be smuggled in as a data URI.
const SAFE_SCHEME = /^(https?:\/\/|mailto:|tel:)/i

// Residual entity text (e.g. a literal "&colon;" that survived decoding).
const ENTITY_RESIDUE = /&[a-z#0-9]{1,8};?/gi

/**
 * True for characters a URL parser ignores or strips, so that removing them
 * yields the string the browser will actually act on.
 *
 * TAB/LF/CR inside a scheme are the classic bypass: the JavaScript string
 * "jav\tascript:alert(1)" does not start with "javascript:", but the browser
 * strips the tab and executes the link.
 */
function isIgnorableUrlChar(cp: number): boolean {
  if (cp <= 0x20) return true // C0 controls + space
  if (cp === 0x7f) return true // DEL
  if (cp >= 0x80 && cp <= 0x9f) return true // C1 controls
  if (cp === 0xad) return true // soft hyphen
  if (cp >= 0x200b && cp <= 0x200f) return true // zero-width + bidi marks
  if (cp >= 0x2028 && cp <= 0x202e) return true // line/para sep + bidi override
  if (cp >= 0x2060 && cp <= 0x2064) return true // word joiner + invisible ops
  if (cp >= 0x206a && cp <= 0x206f) return true // deprecated format chars
  if (cp === 0xfeff) return true // BOM / zero-width no-break space
  if (cp === 0x1680) return true // ogham space mark
  if (cp >= 0x2000 && cp <= 0x200a) return true // en/em spaces
  if (cp === 0x202f || cp === 0x205f || cp === 0x3000) return true // narrow/mideographic
  return false
}

/**
 * Reduces a URL to the form a browser resolves: strips ignorable characters
 * and leftover entity text, then trims.
 */
function normalizeUrl(value: string): string {
  let out = ''
  // Iterating a string yields whole code points, so astral characters are
  // never split and mangled.
  for (const ch of value) {
    if (!isIgnorableUrlChar(ch.codePointAt(0) ?? 0)) out += ch
  }
  return out.replace(ENTITY_RESIDUE, '').trim()
}

/**
 * Returns a safe URL string, or null when the attribute must be dropped.
 *
 * A bare `startsWith('javascript:')` check on the trimmed value is NOT
 * sufficient: DOMParser has already decoded `jav&#x0A;ascript:` into a real
 * newline, and `trim()` removes only leading and trailing whitespace — so the
 * prefix check passed, the attribute was kept, and the link executed on every
 * visitor of the public page.
 *
 * Normalize first, then allowlist the scheme.
 */
export function safeUrl(value: string | null | undefined): string | null {
  if (typeof value !== 'string') return null
  const original = value.trim()
  if (!original) return null

  const normalised = normalizeUrl(original).toLowerCase()
  if (!normalised) return null

  if (SAFE_SCHEME.test(normalised)) return original

  // A colon anywhere means the browser will try to resolve a scheme, so a
  // value like "foo:bar" must never be treated as a relative path.
  if (normalised.includes(':')) return null

  // Site-relative and anchor links carry no scheme and cannot execute.
  if (/^[/#?]/.test(normalised)) return original
  if (/^\.\.?[/\\]/.test(normalised)) return original

  return null
}

// maxDepth bounds the sanitizer's recursion. Without it, an article with
// thousands of nested elements throws a RangeError during render — which the
// error boundary catches, but which means one malicious article can blank the
// help page it lives on.
const maxDepth = 512

// SURVIVING_ATTRIBUTES is the final allowlist. Everything else must be gone
// by the time sanitizeHtml returns.
const ALLOWED_ATTRS = new Set(['href', 'src', 'rel', 'target', 'alt', 'title', 'colspan', 'rowspan', 'scope', 'loading', 'decoding', 'referrerpolicy'])

/**
 * Guarantees a disallowed attribute is actually gone.
 *
 * The first two attempts at stripping used `removeAttribute` and then
 * `removeAttributeNode`; a test proved `is` survived both, because it is
 * special-cased by the DOM. So: remove what we can, then VERIFY, and if any
 * disallowed attribute is still attached, rebuild the element keeping only the
 * allowlist. Rebuilding is cheap and happens only in the pathological case.
 */
function stripAllDisallowed(el: Element, attr: Attr) {
  el.removeAttributeNode(attr)
  el.removeAttribute(attr.name)

  const still = Array.from(el.attributes).filter(
    (a) => !ALLOWED_ATTRS.has(a.name.toLowerCase()),
  )
  if (still.length === 0) return

  const clean = el.ownerDocument.createElement(el.tagName.toLowerCase())
  for (const a of Array.from(el.attributes)) {
    if (ALLOWED_ATTRS.has(a.name.toLowerCase())) {
      clean.setAttribute(a.name, a.value)
    }
  }
  while (el.firstChild) clean.appendChild(el.firstChild)
  el.replaceWith(clean)
}

export function sanitizeHtml(dirty: string | null | undefined): string {
  if (typeof dirty !== 'string' || !dirty) return ''

  // Pre-pass: strip raw-text containers from the string so the parser cannot
  // promote their content into the tree. See CONTAINER_WITH_CONTENT.
  const stripped = dirty
    .replace(CONTAINER_WITH_CONTENT, '')
    .replace(UNCLOSED_CONTAINER, '')

  const doc = new DOMParser().parseFromString(stripped, 'text/html')

  // Iterative rather than recursive, with an explicit depth cap: the original
  // recursion blew the stack at ~4000 nested nodes.
  const walk = (root: Element) => {
    const stack: Array<{ node: Element; depth: number }> = []
    for (const child of Array.from(root.children)) stack.push({ node: child, depth: 1 })

    while (stack.length > 0) {
      const { node: child, depth } = stack.pop()!
      if (depth > maxDepth) {
        child.remove()
        continue
      }

      const tag = child.tagName.toLowerCase()

      if (DROPPED_TAGS.has(tag)) {
        child.remove()
        continue
      }

      if (!ALLOWED_TAGS.has(tag)) {
        // Unwrap unknown-but-harmless containers so their text survives, and
        // keep descending into the promoted children.
        const promoted = Array.from(child.childNodes)
        child.replaceWith(...promoted)
        for (const n of promoted) {
          if (n.nodeType === 1) stack.push({ node: n as Element, depth: depth + 1 })
        }
        continue
      }

      // A named anchor can act as a navigation target; drop the legacy form
      // so `target` is always a value we set ourselves.
      if (child.hasAttribute('name')) child.removeAttribute('name')

      for (const attr of Array.from(child.attributes)) {
        const name = attr.name.toLowerCase()

        // Event handlers and namespaced attributes are never legitimate.
        if (name.startsWith('on') || name.includes(':')) {
          child.removeAttributeNode(attr)
          continue
        }

        if (URL_ATTRS.has(name)) {
          const clean = safeUrl(attr.value)
          if (clean === null) {
            child.removeAttributeNode(attr)
            continue
          }
          child.setAttribute(name, clean)

          if (name === 'href') {
            child.setAttribute('rel', 'noopener noreferrer nofollow')
            // Only open a new tab for genuinely external links; forcing it on
            // an "#section" anchor would break in-page navigation.
            if (/^https?:\/\//i.test(normalizeUrl(clean))) {
              child.setAttribute('target', '_blank')
            } else {
              child.removeAttribute('target')
            }
          } else {
            // Remote images double as tracking beacons. The media endpoint is
            // same-origin, so require a path or an absolute http(s) URL.
            if (!/^(https?:\/\/|\/)/i.test(normalizeUrl(clean))) {
              child.removeAttributeNode(attr)
              continue
            }
            child.setAttribute('loading', 'lazy')
            child.setAttribute('decoding', 'async')
            child.setAttribute('referrerpolicy', 'no-referrer')
          }
          continue
        }

        // Allowlist of plain, non-executable attributes.
        //
        // `class` is deliberately NOT here. The app is Tailwind, so an
        // attacker-authored class is a full styling primitive: `class="fixed
        // inset-0 z-[99999] bg-white"` paints a viewport-sized white overlay
        // with arbitrary text on a public page, using the host app's own
        // utilities. That is a credential-phishing control, not a cosmetic
        // attribute — and with `script-src 'unsafe-inline'` in the CSP (kept
        // for Midtrans Snap) this function is the only XSS control here.
        if (name === 'alt' || name === 'title' || name === 'colspan' ||
            name === 'rowspan' || name === 'scope') {
          continue
        }

        // `is` is special-cased by the DOM: in some implementations neither
        // removeAttribute nor removeAttributeNode will clear it, so a rebuilt
        // node is the only portable way to drop it. stripAll walks the whole
        // set and rebuilds once if anything survived.
        stripAllDisallowed(child, attr)
      }

      // An <a> that lost its href is not a link; make it plain text.
      if (tag === 'a' && !child.hasAttribute('href')) {
        child.removeAttribute('target')
        child.removeAttribute('rel')
      }

      for (const grand of Array.from(child.children)) {
        stack.push({ node: grand, depth: depth + 1 })
      }
    }
  }

  walk(doc.body)
  return doc.body.innerHTML
}
