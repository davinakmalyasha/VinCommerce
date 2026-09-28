import { test, expect } from '@playwright/test'
import { BULK_SLUG, RUN_ID } from './fixtures'

/**
 * API failure states.
 *
 * Every one of these pages used to render `if (!data) return <div>Memuat...</div>`
 * for BOTH "still loading" and "the request failed", so a 500 from the API left
 * the visitor staring at a pulsing "Memuat produk..." skeleton forever, with no
 * error and no way to retry. The pages now route through <QueryState>, which
 * distinguishes the two and offers a retry — but only if a test actually forces
 * a failure, because a healthy API never produces one.
 *
 * `page.route` is the only honest way to do this: stubbing the backend in the
 * database would not exercise the client's error path, and stopping the API
 * would break the shared stack for every other test.
 */
test.describe('error states', () => {
  test('a 500 on the product page shows an error, not a perpetual "Memuat..."', async ({ page }) => {
    // Match only the product detail call. A broader glob would also stub
    // /reviews, /photos and /related, which are secondary queries and would
    // obscure which failure produced the message.
    await page.route(`**/api/v1/products/${BULK_SLUG}`, (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        headers: { 'Access-Control-Allow-Origin': '*' },
        body: JSON.stringify({ error: { code: 'INTERNAL', message: 'boom' } }),
      }),
    )

    await page.goto(`/product/${BULK_SLUG}`)

    // The error state, with a retry affordance.
    await expect(page.getByText(`Gagal memuat produk`)).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: 'Coba lagi' })).toBeVisible()

    // And, critically: NOT the loading state. This is the regression — the
    // skeleton and the error were the same markup, so a 500 looked identical to
    // a slow request.
    await expect(page.getByText('Memuat produk...')).toHaveCount(0)
    await expect(page.locator('[role="status"]')).toHaveCount(0)
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0)
  })

  test('the retry button re-issues the request and recovers when the API heals', async ({ page }) => {
    let failNext = true
    await page.route(`**/api/v1/products/${BULK_SLUG}`, async (route) => {
      if (failNext) {
        failNext = false
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({ error: { code: 'UNAVAILABLE', message: 'warming up' } }),
        })
        return
      }
      await route.fallback()
    })

    await page.goto(`/product/${BULK_SLUG}`)
    await expect(page.getByText('Gagal memuat produk')).toBeVisible({ timeout: 20_000 })

    await page.getByRole('button', { name: 'Coba lagi' }).click()

    // React Query retries transient failures on its own too, so a bare
    // "the error is gone" wait is not the assertion — the product itself
    // rendering is.
    await expect(page.getByRole('button', { name: '+ Keranjang' })).toBeVisible({ timeout: 30_000 })
    await expect(page.getByText('Gagal memuat produk')).toHaveCount(0)
  })

  test('a 404 on the product page is an error state, not an empty page', async ({ page }) => {
    const missing = `no-such-product-${RUN_ID}`
    await page.route(`**/api/v1/products/${missing}`, (route) =>
      route.fulfill({
        status: 404,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'NOT_FOUND', message: 'product not found' } }),
      }),
    )

    await page.goto(`/product/${missing}`)
    await expect(page.getByText('Gagal memuat produk')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: 'Coba lagi' })).toBeVisible()
  })

  test('a 500 on the cart page shows an error, not a perpetual "Memuat..."', async ({ page }) => {
    await page.route('**/api/v1/cart', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'INTERNAL', message: 'cart unavailable' } }),
      }),
    )

    await page.goto('/cart')
    await expect(page.getByText('Gagal memuat keranjang')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Memuat keranjang...')).toHaveCount(0)
  })

  test('a 500 on the orders list shows an error, not a perpetual "Memuat..."', async ({ page }) => {
    await page.route('**/api/v1/orders?*', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'INTERNAL', message: 'orders unavailable' } }),
      }),
    )

    // The orders page requires a session; sign in through the API first so the
    // SPA is authenticated, then force only the list call to fail.
    await page.goto('/login')
    await page.getByPlaceholder('Email').fill('buyer.sample@vincommerce.com')
    await page.getByPlaceholder('Password').fill('BuyerPass123!')
    await page.getByRole('button', { name: 'Masuk' }).click()
    await expect(page).toHaveURL('/', { timeout: 20_000 })

    await page.goto('/orders')
    await expect(page.getByText('Gagal memuat pesanan')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Memuat pesanan...')).toHaveCount(0)
  })
})
