import { test, expect } from '@playwright/test'
import { API, SELLER, login, loginViaUi } from './fixtures'
import { attachDiagnostics } from './diagnostics'

/**
 * The seller product list rendered Rp 0, 0 stok and "0 varian" for EVERY product.
 *
 * GET /seller/products returns products without an embedded `variants` array, so the
 * table's `variants[0].price`, `variants[0].stock` and `variants.length` were all
 * undefined and fell back to 0. The query already computed the stock sum and the
 * variant count and then discarded them (`_ = stockSum`), so the data was sitting
 * there and simply never reached the client.
 *
 * The first test pins the payload; the second pins what a seller actually sees.
 */
test.describe('seller product list', () => {
  test.beforeEach(async ({ page }) => {
    attachDiagnostics(page)
  })

  test('reports each product’s price, stock and variant count', async ({ request }) => {
    const token = await login(request, SELLER)
    const res = await request.get(`${API}/api/v1/seller/products?page=1&page_size=50`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.ok(), `seller products -> ${res.status()}`).toBeTruthy()
    const body = (await res.json()) as {
      products: { name: string; stock?: number; variant_count?: number; min_price?: number }[]
    }
    expect(body.products.length).toBeGreaterThan(0)

    const withStock = body.products.filter((p) => (p.stock ?? 0) > 0)
    expect(
      withStock.length,
      'no product reports stock > 0, so the list can only render 0',
    ).toBeGreaterThan(0)

    for (const p of withStock) {
      expect(p.variant_count ?? 0, `${p.name}: variant count missing`).toBeGreaterThan(0)
      expect(p.min_price ?? 0, `${p.name}: price missing`).toBeGreaterThan(0)
    }
  })

  test('renders a non-zero price and stock for a seeded product', async ({ page }) => {
    await loginViaUi(page, SELLER)
    await page.goto('/seller/products')

    const row = page.getByRole('row', { name: /Smartphone Aurora X5/ })
    await expect(row).toBeVisible({ timeout: 20_000 })

    // Produk | Harga | Stok | Terjual | Status | Aksi
    const cells = row.getByRole('cell')
    await expect(cells.nth(1), 'price cell must not be Rp 0').not.toHaveText(/Rp\s*0\b/)
    await expect(cells.nth(2), 'stock cell must not be 0').not.toHaveText(/^0$/)
  })
})
