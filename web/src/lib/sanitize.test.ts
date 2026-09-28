import { describe, expect, it } from 'vitest'
import { safeUrl, sanitizeHtml } from './sanitize'

/**
 * The XSS boundary for admin-authored help/legal content, rendered with
 * dangerouslySetInnerHTML on unauthenticated public pages.
 *
 * Every case in "must be dropped" previously survived the old check:
 * `startsWith('javascript:')` on the trimmed value did not match
 * "jav\tascript:" or "jav&#x0A;ascript:" (which DOMParser decodes to a real
 * newline), so the attribute was kept and the link executed.
 */
describe('safeUrl', () => {
  describe('drops script-bearing schemes, including obfuscated forms', () => {
    const vectors = [
      ['plain javascript', 'javascript:alert(1)'],
      ['uppercase', 'JAVASCRIPT:alert(1)'],
      ['mixed case', 'JaVaScRiPt:alert(1)'],
      ['leading whitespace', '   javascript:alert(1)'],
      ['tab inside scheme', 'jav\tascript:alert(1)'],
      ['newline inside scheme', 'jav\nascript:alert(1)'],
      ['carriage return inside scheme', 'jav\rascript:alert(1)'],
      ['form feed inside scheme', 'jav\fscript:alert(1)'],
      ['vertical tab inside scheme', 'jav\x0bcript:alert(1)'],
      ['NUL inside scheme', 'jav\x00ascript:alert(1)'],
      ['several tabs', 'jav\t\t\tascript:alert(1)'],
      ['zero-width space', 'jav\u200bascript:alert(1)'],
      ['zero-width no-break space (BOM)', 'jav\ufeffascript:alert(1)'],
      ['soft hyphen', 'jav\u00adascript:alert(1)'],
      ['right-to-left override', 'jav\u202eascript:alert(1)'],
      ['vbscript', 'vbscript:msgbox(1)'],
      ['data uri html', 'data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg=='],
      ['data uri svg', 'data:image/svg+xml,<svg onload=alert(1)>'],
      ['encoded data uri', 'data:%74ext/html,x'],
      ['leading control char', '\x01javascript:alert(1)'],
      ['escaped entity colon', 'javascript&colon;alert(1)'],
    ] as const

    for (const [name, value] of vectors) {
      it(`drops ${name}`, () => {
        expect(safeUrl(value)).toBeNull()
      })
    }
  })

  describe('allows legitimate links', () => {
    const vectors = [
      ['absolute https', 'https://vincommerce.com/help', 'https://vincommerce.com/help'],
      ['absolute http', 'http://example.com', 'http://example.com'],
      ['with query and hash', 'https://example.com/a?b=1#c', 'https://example.com/a?b=1#c'],
      // Scheme-relative: no scheme, so it cannot execute. Kept (but not
      // target=_blank'd), because dropping it would break pasted CDN links.
      ['protocol relative', '//example.com', '//example.com'],
      ['mailto', 'mailto:support@vincommerce.com', 'mailto:support@vincommerce.com'],
      ['tel', 'tel:+6281234567890', 'tel:+6281234567890'],
      ['site relative', '/products/handphone', '/products/handphone'],
      ['anchor', '#faq-1', '#faq-1'],
      ['query only', '?page=2', '?page=2'],
      ['parent relative', '../legal/terms', '../legal/terms'],
    ] as const

    for (const [name, value, want] of vectors) {
      it(`keeps ${name}`, () => {
        expect(safeUrl(value)).toBe(want)
      })
    }
  })

  it('rejects non-strings and empty input', () => {
    expect(safeUrl('')).toBeNull()
    expect(safeUrl('   ')).toBeNull()
    expect(safeUrl(null)).toBeNull()
    expect(safeUrl(undefined)).toBeNull()
  })

  it('does not treat a bare "word:rest" as a relative path', () => {
    // "foo:bar" is resolved as a scheme by browsers, so it must not pass as
    // a relative path.
    expect(safeUrl('foo:bar')).toBeNull()
  })
})

describe('sanitizeHtml', () => {
  it('removes script elements and their contents', () => {
    const out = sanitizeHtml('<p>ok</p><script>alert(1)</script>')
    expect(out).not.toMatch(/script/i)
    expect(out).toContain('ok')
  })

  it('removes style, iframe, object and embed', () => {
    const out = sanitizeHtml(
      '<style>body{display:none}</style><iframe src="//evil"></iframe><object data="x"></object><embed src="y">',
    )
    expect(out).not.toMatch(/<(style|iframe|object|embed)/i)
  })

  it('strips every on* event handler', () => {
    const out = sanitizeHtml('<p onclick="alert(1)" onmouseover="x()">text</p>')
    expect(out).not.toMatch(/onclick/i)
    expect(out).not.toMatch(/onmouseover/i)
    expect(out).toContain('text')
  })

  it('drops an obfuscated javascript: href but keeps the anchor text', () => {
    const out = sanitizeHtml('<a href="jav&#x0A;ascript:alert(1)">click me</a>')
    expect(out).not.toMatch(/href/i)
    expect(out).toContain('click me')
  })

  it('drops javascript: image sources', () => {
    const out = sanitizeHtml('<img src="javascript:alert(1)" alt="x">')
    expect(out).not.toMatch(/src=/i)
  })

  it('adds rel=noopener noreferrer to external links', () => {
    const out = sanitizeHtml('<a href="https://example.com">ext</a>')
    expect(out).toContain('rel="noopener noreferrer nofollow"')
    expect(out).toContain('target="_blank"')
  })

  it('does not force target=_blank on in-page anchors', () => {
    const out = sanitizeHtml('<a href="#section">jump</a>')
    expect(out).toContain('href="#section"')
    expect(out).not.toMatch(/target=/i)
  })

  it('rejects protocol-relative and remote-tracking image tricks', () => {
    // data: images are refused outright; remote https images are allowed but
    // stripped of referrer so they cannot correlate the visitor.
    const blocked = sanitizeHtml('<img src="data:image/png;base64,AAAA" alt="a">')
    expect(blocked).not.toMatch(/src=/i)

    const remote = sanitizeHtml('<img src="https://tracker.example/pixel.gif" alt="a">')
    expect(remote).toContain('referrerpolicy="no-referrer"')
    expect(remote).toContain('loading="lazy"')
  })

  it('unwraps unknown containers so prose survives', () => {
    const out = sanitizeHtml('<marquee>keep this text</marquee>')
    expect(out).toContain('keep this text')
    expect(out).not.toMatch(/marquee/i)
  })

  it('drops svg (a known XSS vector that is otherwise "allowed" content)', () => {
    const out = sanitizeHtml('<svg><script>alert(1)</script></svg>')
    expect(out).not.toMatch(/<svg/i)
    expect(out).not.toMatch(/alert/i)
  })

  it('strips style and id, keeps title', () => {
    const out = sanitizeHtml('<p id="x" style="position:fixed" title="t" data-evil="1">p</p>')
    expect(out).not.toMatch(/id=/i)
    expect(out).not.toMatch(/style=/i)
    expect(out).not.toMatch(/data-evil/i)
    expect(out).toContain('title="t"')
  })

  // --- regressions: gaps found by an adversarial review ---

  it('drops the `is` attribute (custom-element upgrade)', () => {
    // `is` upgrades the host element to a registered custom element. It is not
    // in the allowlist, so it must be stripped. It also has to be stripped with
    // removeAttributeNode: removeAttribute('is') is a no-op in some DOM
    // implementations, which let it survive the first version of this file.
    for (const tag of ['div', 'span', 'p', 'button']) {
      const out = sanitizeHtml(`<${tag} is="evil-element" data-x="1">t</${tag}>`)
      expect(out, `${tag} kept the is attribute`).not.toMatch(/\bis=/)
    }
  })

  it('drops class, so Tailwind cannot be used as a phishing primitive', () => {
    // The app is Tailwind, so an attacker-authored class is a full styling
    // primitive: a viewport-sized white overlay with arbitrary text on a
    // public help/legal page is a credential-phishing control.
    const out = sanitizeHtml(
      '<div class="fixed inset-0 z-[99999] bg-white">Verify your account</div>',
    )
    expect(out).not.toMatch(/class=/)
    // The text still survives — the point is to neutralise the styling, not
    // the prose.
    expect(out).toContain('Verify your account')
  })

  it('does not throw on deeply nested input', () => {
    // The original recursion blew the stack at ~4000 nested nodes, which the
    // error boundary caught — but that still blanked the page. The walk is now
    // iterative with an explicit depth cap.
    //
    // The timeout is generous because parsing thousands of nested elements is
    // genuinely slow in jsdom; the point of the assertion is that it does not
    // throw, not that it is fast.
    for (const depth of [1000, 2000, 4000, 6000]) {
      const input = '<div>'.repeat(depth) + 'deep' + '</div>'.repeat(depth)
      expect(() => sanitizeHtml(input), `depth ${depth}`).not.toThrow()
    }
  }, 60_000)

  it('neutralises rawtext-container breakout', () => {
    // Inside <plaintext>/<textarea>/<xmp> the parser switches to raw text, so a
    // trailing <img onerror> is inert CONTENT; re-parenting on unwrap must not
    // promote it to a real element.
    //
    // Assert on ELEMENTS, not on substrings: the escaped output legitimately
    // contains the characters "onerror" as visible text, and an assertion that
    // greps for it fails on correct behaviour.
    for (const tag of ['plaintext', 'textarea', 'xmp', 'noscript', 'template']) {
      const out = sanitizeHtml(`<${tag}><img src=x onerror=alert(1)></${tag}>`)
      const doc = new DOMParser().parseFromString(out, 'text/html')
      expect(doc.body.querySelector('img'), tag).toBeNull()
      expect(doc.body.querySelector('script'), tag).toBeNull()
      // No element anywhere carries an event handler.
      for (const el of Array.from(doc.body.querySelectorAll('*'))) {
        for (const a of Array.from(el.attributes)) {
          expect(a.name.toLowerCase(), `${tag}: ${a.name}`).not.toMatch(/^on/i)
        }
      }
    }
  })

  it('drops srcdoc, formaction and other non-allowlisted URL sinks', () => {
    const out = sanitizeHtml(
      '<div srcdoc="<script>alert(1)</script>" formaction="javascript:alert(1)" ping="//track" ' +
        'usemap="#m" ismap download="x">t</div>',
    )
    for (const attr of ['srcdoc', 'formaction', 'ping', 'usemap', 'ismap', 'download']) {
      expect(out, attr).not.toMatch(new RegExp(`${attr}=`, 'i'))
    }
  })

  it('does not match substrings when asserting element removal', () => {
    // Guards the test suite itself: a help article legitimately titled
    // "Mengaktifkan JavaScript" must not fail an assertion that greps for
    // "script". Assert on the element, not on prose.
    const out = sanitizeHtml('<p>Mengaktifkan JavaScript itu penting</p>')
    expect(out).toContain('JavaScript')
    expect(out).not.toMatch(/<script/i)
  })

  it('removes script elements and their contents even when the prose mentions script', () => {
    const out = sanitizeHtml('<p>about scripts</p><script>alert(1)</script>')
    expect(out).not.toMatch(/<script/i)
    expect(out).toContain('about scripts')
  })

  it('preserves tables and adds scope hints untouched', () => {
    const out = sanitizeHtml(
      '<table><thead><tr><th scope="col">Item</th></tr></thead><tbody><tr><td>1</td></tr></tbody></table>',
    )
    expect(out).toContain('<table>')
    expect(out).toContain('scope="col"')
  })

  it('handles empty and non-string input safely', () => {
    expect(sanitizeHtml('')).toBe('')
    expect(sanitizeHtml(null)).toBe('')
    expect(sanitizeHtml(undefined)).toBe('')
  })

  it('does not double-escape already-safe content', () => {
    const src = '<p>Halo <strong>dunia</strong></p>'
    expect(sanitizeHtml(src)).toBe(src)
  })
})
