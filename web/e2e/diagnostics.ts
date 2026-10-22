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

/**
 * Logs what the cart actually contains, rather than a truncated body.
 *
 * The raw payload was useless: `lines` sorts after `free_shipping` in the JSON, so a
 * character limit cut it off exactly where the answer was. Log the LINES -- the
 * variant, the quantity and the product name the row will render.
 */
export function logCartResponses(page: Page): void {
  page.on('response', async (r) => {
    if (!r.url().includes('/api/v1/cart')) return
    const body = await r.text().catch(() => '')
    let summary = body.slice(0, 200)
    try {
      const parsed = JSON.parse(body) as {
        lines?: { variant_id: string; quantity: number; product_name?: string }[]
      }
      if (Array.isArray(parsed.lines)) {
        summary = `lines=${parsed.lines.length} ` +
          parsed.lines.map((l) => `${l.product_name ?? '(no product_name)'} x${l.quantity} ${l.variant_id}`).join(' | ')
      } else {
        summary = `NO lines key: ${body.slice(0, 200)}`
      }
    } catch {
      summary = `unparseable: ${body.slice(0, 200)}`
    }
    console.log('BROWSER-CART', r.status(), r.url(), summary)
  })
}

/** Logs the accessible names of every button on the page, and the page's text. */
export async function logPageSurface(page: Page, label: string): Promise<void> {
  const names = await page.getByRole('button').allInnerTexts().catch(() => [] as string[])
  console.log(`PAGE-SURFACE ${label} buttons=${JSON.stringify(names.map((n) => n.trim()).filter(Boolean))}`)
  const text = await page.locator('main').innerText().catch(() => '')
  console.log(`PAGE-SURFACE ${label} text=${text.replace(/\s+/g, ' ').slice(0, 400)}`)
}
