// Minimal allowlist-based HTML sanitizer for admin-authored article/legal
// content. The backend stores free-text HTML; rendering it raw would let a
// compromised admin account plant stored XSS on public pages.
// (Server-side sanitization is still recommended as defense in depth.)

const ALLOWED_TAGS = new Set([
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'p', 'br', 'hr', 'blockquote', 'pre', 'code',
  'ul', 'ol', 'li',
  'strong', 'b', 'em', 'i', 'u', 's', 'del', 'mark', 'small', 'sub', 'sup',
  'a', 'img', 'figure', 'figcaption',
  'table', 'thead', 'tbody', 'tr', 'th', 'td',
  'div', 'span',
])

const URL_ATTRS = new Set(['href', 'src'])

function safeUrl(value: string): string | null {
  const v = value.trim().toLowerCase()
  if (v.startsWith('javascript:') || v.startsWith('data:') || v.startsWith('vbscript:')) {
    return null
  }
  return value
}

export function sanitizeHtml(dirty: string): string {
  const doc = new DOMParser().parseFromString(dirty, 'text/html')

  const walk = (node: Element) => {
    for (const child of Array.from(node.children)) {
      walk(child)
      const tag = child.tagName.toLowerCase()
      if (!ALLOWED_TAGS.has(tag)) {
        // Unwrap containers so text survives; drop everything else entirely.
        if (tag === 'script' || tag === 'style' || tag === 'iframe' ||
            tag === 'object' || tag === 'embed' || tag === 'form') {
          child.remove()
        } else {
          child.replaceWith(...Array.from(child.childNodes))
        }
        continue
      }
      // Strip every attribute except a small safe set; kill all on* handlers.
      for (const attr of Array.from(child.attributes)) {
        const name = attr.name.toLowerCase()
        if (URL_ATTRS.has(name)) {
          const clean = safeUrl(attr.value)
          if (clean === null) {
            child.removeAttribute(attr.name)
          } else if (name === 'href') {
            child.setAttribute('rel', 'noopener noreferrer')
            child.setAttribute('target', '_blank')
          }
          continue
        }
        if (name === 'alt' || name === 'title' || name === 'class' || name === 'colspan' || name === 'rowspan') {
          continue
        }
        child.removeAttribute(attr.name)
      }
    }
  }

  walk(doc.body)
  return doc.body.innerHTML
}
