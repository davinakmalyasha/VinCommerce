import type { Page } from '@playwright/test'

/**
 * Makes a failure legible in the CI log.
 *
 * Most of what this suite reported for a long time was unactionable: a blank
 * screenshot, a "Test timeout exceeded" with no call named, an expect that failed
 * without saying which request had failed. Every one of those cost a CI cycle to
 * diagnose, and every one of them was solved the moment the browser said what
 * happened.
 *
 * So: log the page errors, the console errors, a crash, and every response that
 * came back >= 400 with the endpoint and the body. It is diagnostic output, not
 * behaviour -- no assertion depends on it.
 */
export function attachDiagnostics(page: Page): void {
  page.on('pageerror', (e) => console.log('PAGEERROR:', e.message, e.stack ?? ''))
  page.on('console', (m) => {
    if (m.type() === 'error') console.log('CONSOLE-ERROR:', m.text())
  })
  page.on('crash', () => console.log('PAGE CRASHED'))
  page.on('response', async (r) => {
    if (r.status() < 400) return
    const body = await r.text().catch(() => '')
    console.log('HTTP-FAIL', r.status(), r.request().method(), r.url(), body.slice(0, 200))
  })
}

/** Logs every /cart response the browser receives, with its body. */
export function logCartResponses(page: Page): void {
  page.on('response', async (r) => {
    if (!r.url().includes('/api/v1/cart')) return
    const body = await r.text().catch(() => '')
    console.log('BROWSER-CART', r.status(), r.url(), body.slice(0, 600))
  })
}
