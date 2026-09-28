import { test, expect } from '@playwright/test'
import { API, BUYER, clearCartLine, firstVariant, login, loginViaUi } from './fixtures'

/**
 * Cart quantity stepper under rapid clicking.
 *
 * The bug class: the row is mutated optimistically (onMutate patches the cached
 * quantity, onSettled invalidates). A burst of clicks could interleave an
 * in-flight GET /cart — which carries the PRE-click quantity — so a stale
 * response overwrote the optimistic value and the counter jumped backwards. The
 * previous code used the mutation's single `isPending` to disable the row,
 * which additionally froze every other row while one was in flight.
 *
 * The assertion is deliberately "exactly one increment per request the browser
 * actually sent" rather than a fixed number. Some of the clicks land while the
 * button is disabled (the component disables the row in flight, and a disabled
 * button swallows the event), so how many land is not deterministic — but the
 * relationship between clicks that landed, requests sent, and the final number
 * is, and that is the invariant a lost update would break.
 */
test.describe('cart: quantity stepper', () => {
  test('rapid + clicks produce exactly one increment per request, and the UI agrees with the server', async ({
    page,
    request,
  }) => {
    const token = await login(request, BUYER)
    const auth = { Authorization: `Bearer ${token}` }
    const variant = await firstVariant(request, token, 'smartphone-aurora-x5-pro')

    // PUT is an absolute set, so the starting quantity is deterministic no
    // matter what a previous test left in the cart.
    const set = await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: variant.id, quantity: 1 },
    })
    expect(set.ok(), `could not seed the cart: ${set.status()}`).toBeTruthy()

    await loginViaUi(page, BUYER)
    await page.goto('/cart')

    const increase = page.getByRole('button', { name: `Tambah jumlah ${variant.product_name}` })
    await expect(increase).toBeVisible({ timeout: 20_000 })

    // The stepper renders as [−] [quantity] [+], so the number is the span
    // immediately before the + button. Anchoring on the button (rather than on
    // some ancestor div) keeps this correct when the cart has several rows.
    const quantityCell = increase.locator('xpath=preceding-sibling::span[1]')
    await expect(quantityCell).toHaveText('1')

    // Count the requests the browser actually issues. A lost update shows up as
    // "3 requests sent, 2 increments applied" or the reverse.
    const putBodies: number[] = []
    page.on('request', (req) => {
      if (req.method() !== 'PUT') return
      if (!req.url().includes('/api/v1/cart/items')) return
      try {
        const body = JSON.parse(req.postData() ?? '{}') as { quantity?: number }
        if (typeof body.quantity === 'number') putBodies.push(body.quantity)
      } catch {
        // A non-JSON body would itself be the bug; let the count assertion fail.
      }
    })

    // Fire a burst without waiting for the UI to settle between clicks.
    const BURST = 5
    await Promise.allSettled(
      Array.from({ length: BURST }, () => increase.click({ timeout: 3_000 })),
    )

    // Let every in-flight request finish and the cache settle.
    await page.waitForLoadState('networkidle')
    await expect
      .poll(
        async () => {
          const res = await request.get(`${API}/api/v1/cart`, { headers: auth })
          const lines = (await res.json()).lines as { variant_id: string; quantity: number }[]
          return lines.find((l) => l.variant_id === variant.id)?.quantity ?? 0
        },
        { timeout: 20_000, message: 'server quantity never settled' },
      )
      .toBe(1 + putBodies.length)

    // The last PUT the browser sent must be the final state: no two requests
    // may claim the same quantity, which is exactly the stale-closure race.
    expect(new Set(putBodies).size, 'two PUTs sent the same target quantity').toBe(putBodies.length)
    expect(putBodies.length, 'no increment ever reached the API').toBeGreaterThan(0)

    // The rendered number must equal the server number. An in-flight GET
    // overwriting the optimistic patch is what produced a visible counter that
    // disagreed with the cart.
    const serverLines = (await (await request.get(`${API}/api/v1/cart`, { headers: auth })).json())
      .lines as { variant_id: string; quantity: number }[]
    const serverQty = serverLines.find((l) => l.variant_id === variant.id)?.quantity
    await expect(quantityCell).toHaveText(String(serverQty))

    // The row must not be left stuck disabled — a failed request releases the
    // row in onSettled, not onSuccess.
    await expect(increase).toBeEnabled({ timeout: 10_000 })

    await clearCartLine(request, token, variant.id)
  })

  test('the stepper is per-row: touching one row never disables another', async ({
    page,
    request,
  }) => {
    const token = await login(request, BUYER)
    const auth = { Authorization: `Bearer ${token}` }
    const a = await firstVariant(request, token, 'smartphone-aurora-x5-pro')
    const b = await firstVariant(request, token, 'true-wireless-earbuds-mini')

    await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: a.id, quantity: 1 },
    })
    await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: b.id, quantity: 1 },
    })

    await loginViaUi(page, BUYER)
    await page.goto('/cart')

    const incA = page.getByRole('button', { name: `Tambah jumlah ${a.product_name}` })
    const incB = page.getByRole('button', { name: `Tambah jumlah ${b.product_name}` })
    await expect(incA).toBeVisible({ timeout: 20_000 })
    await expect(incB).toBeVisible({ timeout: 20_000 })

    // Slow row A's response down so it is reliably still in flight when B is
    // clicked. A single shared isPending would leave B disabled.
    await page.route(`**/api/v1/cart/items`, async (route) => {
      const req = route.request()
      if (req.method() === 'PUT' && req.postData()?.includes(a.id)) {
        await new Promise((r) => setTimeout(r, 1_500))
      }
      await route.fallback()
    })

    await incA.click()
    await expect(incA).toBeDisabled()
    await expect(incB, 'row B must stay usable while row A is in flight').toBeEnabled()

    await page.unroute(`**/api/v1/cart/items`)
    await clearCartLine(request, token, a.id)
    await clearCartLine(request, token, b.id)
  })
})
