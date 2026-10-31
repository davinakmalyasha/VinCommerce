import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// The offline shell is the one screen no component test and no e2e run ever
// renders: it is only shown by the service worker when a navigation fails.
// That is exactly how it kept an English heading in an `lang="id"` app --
// every other page was reviewed, this one was not. These assertions read the
// shipped file, not a copy, so they cannot drift from it.
const shell = readFileSync(resolve(process.cwd(), 'public', 'offline.html'), 'utf8')
const sw = readFileSync(resolve(process.cwd(), 'public', 'sw.js'), 'utf8')

describe('offline shell', () => {
  it('declares the Indonesian locale it is written in', () => {
    expect(shell).toMatch(/<html lang="id">/)
  })

  it('has no English copy left in the visible text', () => {
    const visibleText = shell
      .replace(/<style>[\s\S]*?<\/style>/g, '')
      .replace(/<script>[\s\S]*?<\/script>/g, '')
      .replace(/<!--[\s\S]*?-->/g, '')

    expect(visibleText).not.toContain('You\'re offline')
    expect(visibleText).toContain('Kamu sedang offline')
  })

  it('is served by a service-worker revision that will replace stale copies', () => {
    // A precached file alone never reaches an existing install: the browser
    // only updates a service worker when sw.js changes. If this assertion
    // fails because someone edited the shell without touching sw.js, the edit
    // is invisible to every current user.
    expect(sw).toMatch(/const VERSION = 'v[3-9]\d*'/)
    expect(sw).toContain("'/offline.html'")
  })
})
