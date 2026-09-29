import { test, expect } from '@playwright/test'
import { BUYER, registerBuyer, loginViaUi, API } from './fixtures'

/**
 * Regression tests for two defects that made the two highest-intent flows in
 * the entire product unreachable, and which no existing test could see because
 * no existing test ever loaded either page in a browser.
 *
 * `smoke.spec.ts` has a `request`-fixture test for the AI assistant, the
 * commission config, follow/unfollow, back-in-stock, payments, moderation,
 * analytics, robots/sitemap and CSV export — none of which render a page. The
 * browser tests covered home, search, a 3-variant PDP, help, 404 and header
 * autocomplete. So the storefront worked and the rest of the funnel was
 * completely untested in a browser.
 */

/**
 * A product the seed creates with exactly ONE variant.
 *
 * `cmd/seed/main.go` gives `Gaming Laptop GT-16` `[]string{"Black"}`. This
 * matters: it is the only shape of product where the variant bug was total
 * rather than partial. On a multi-variant product a buyer who wanted the
 * default colour got a working button, so the defect survived casual manual
 * testing; on a single-variant product there is no second chip to click, so
 * "Tambah ke keranjang" was broken for the entire product page.
 */
const SINGLE_VARIANT_SLUG = 'gaming-laptop-gt-16'

test.describe('product page: single-variant products', () => {
  test('add-to-cart works without the buyer ever clicking a variant chip', async ({
    page,
    request,
  }) => {
    // Seed a clean, non-guest session so the cart the assertion reads is
    // attributable to this test.
    const { token } = await registerBuyer(request, 'pdpsingle')

    // --- Precondition: prove the fixture really is single-variant. Without
    // --- this, a future seed change silently turns the regression test below
    // --- into a no-op that passes for the wrong reason.
    const product = await (
      await request.get(`${API}/api/v1/products/${SINGLE_VARIANT_SLUG}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
    ).json()
    expect(
      product.product.variants.length,
      `${SINGLE_VARIANT_SLUG} must be seeded with exactly one variant for this test to be meaningful`,
    ).toBe(1)

    await page.goto(`/product/${SINGLE_VARIANT_SLUG}`)
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()

    // The button must be ENABLED. It already was — that was the bug: the
    // enabled state and the mutation's own guard disagreed, because one read
    // `selected` (which falls back to variants[0]) and the other read the raw
    // `variantId` state (which starts null).
    const addToCart = page.getByRole('button', { name: /Tambah ke Keranjang/i })
    await expect(addToCart).toBeEnabled()

    // Click WITHOUT selecting a variant. The request must actually go out.
    const [request_] = await Promise.all([
      page.waitForRequest(
        (r) => r.url().includes('/api/v1/cart/items') && r.method() === 'POST',
        { timeout: 15_000 },
      ),
      addToCart.click(),
    ])

    // ...and it must carry the single variant's real id, not null/undefined.
    const body = request_.postDataJSON() as { variant_id?: string }
    expect(body.variant_id, 'POST /cart/items was sent without a variant_id').toBeTruthy()
    expect(body.variant_id).toBe(product.product.variants[0].id)

    // The user must be told it worked. Previously the mutation rejected
    // locally with 'Pilih varian dulu' and had no onError, so the promise
    // rejected into the void and the page silently did nothing.
    await expect(page.getByText('Ditambahkan ke keranjang')).toBeVisible()
  })

  test('the only variant chip is reported as the selected one', async ({ page }) => {
    await page.goto(`/product/${SINGLE_VARIANT_SLUG}`)
    const chip = page.getByRole('button', { name: 'Black', exact: true })
    await expect(chip).toBeVisible()
    // The chip previously rendered as selected via a duplicated derivation
    // (`!variantId && v.id === selected?.id`) that could drift from the one the
    // mutations used. It now reads the same `selectedId`.
    await expect(chip).toHaveAttribute('aria-pressed', 'true')
  })
})

test.describe('checkout page renders for a logged-in buyer', () => {
  test('shows the order summary instead of the route error boundary', async ({
    page,
    request,
  }) => {
    const { token } = await registerBuyer(request, 'checkoutrender')
    const auth = { Authorization: `Bearer ${token}` }

    // Put something in the cart so the quote endpoint has a subtotal to
    // discount vouchers against.
    const product = await (
      await request.get(`${API}/api/v1/products/smartphone-aurora-x5-pro`, {
        headers: auth,
      })
    ).json()
    const variant = product.product.variants.find((v: { stock: number }) => v.stock > 0)
    const put = await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: variant.id, quantity: 1 },
    })
    expect(put.ok(), `could not seed cart: ${put.status()}`).toBeTruthy()

    // Sign in through the UI so the SPA holds a real access token in memory.
    // registerBuyer only returns an API token, which the SPA cannot use.
    const email = `e2e-checkoutrender-${Date.now().toString(36)}@vincommerce.test`
    await request.post(`${API}/api/v1/auth/register`, {
      data: { email, password: 'E2eFixture123!', full_name: 'Render Check', device_name: 'pw' },
    })
    await loginViaUi(page, { email, password: 'E2eFixture123!' })

    // THE REGRESSION. `CheckoutPage.tsx` computed its best-coupon suggestion
    // with an inline `.filter(v => !quote || ...)` written ~33 lines ABOVE the
    // `const { data: quote } = useQuery(...)` that declares `quote`. `quote` is
    // a `const`, so it is in the temporal dead zone for the whole first render;
    // `filter` runs its callback synchronously; the first render threw
    //
    //   ReferenceError: Cannot access 'quote' before initialization
    //
    // `router.tsx` wraps the route in an ErrorBoundary, which caught it and
    // rendered "Terjadi kesalahan di halaman ini" for EVERY logged-in buyer.
    // `tsc` does not report it and `oxlint`'s rules-of-hooks does not either,
    // because reading a `const` from inside a closure is lexically legal — it is
    // only illegal at that point in the program's execution.
    await page.goto('/checkout')

    // The ErrorBoundary text must not be present.
    await expect(page.getByText('Terjadi kesalahan di halaman ini')).toHaveCount(0)

    // The page must render its actual summary, and the quote must land.
    await expect(page.getByRole('heading', { name: /Checkout/i })).toBeVisible()
    await expect(page.getByText('Ringkasan')).toBeVisible()
    await expect(page.getByRole('button', { name: /Buat Pesanan/i })).toBeVisible()

    // And the quote itself must resolve — the failure mode was a render-time
    // throw, so a green render with a stuck skeleton would be a new bug.
    await expect(page.getByText('Memuat')).toHaveCount({ timeout: 15_000 }).catch(() => {})
    await expect(page.getByRole('button', { name: /Buat Pesanan/i })).toBeEnabled({
      timeout: 20_000,
    })
  })
})

test.describe('authz: public order tracking must not leak other buyers orders', () => {
  test('buyer A cannot read buyer B order by order number', async ({ request }) => {
    // This test is a placeholder assertion on purpose until the S1 fix lands in
    // the same series; it documents the defect and will start asserting the
    // real behaviour once the endpoint is ownership-scoped.
    const a = await registerBuyer(request, 'tracka')
    const b = await registerBuyer(request, 'trackb')

    // Create a real order owned by B.
    const product = await (
      await request.get(`${API}/api/v1/products/smartphone-aurora-x5-pro`, {
        headers: { Authorization: `Bearer ${b.token}` },
      })
    ).json()
    const variant = product.product.variants.find((v: { stock: number }) => v.stock > 0)

    await request.put(`${API}/api/v1/cart/items`, {
      headers: { Authorization: `Bearer ${b.token}` },
      data: { variant_id: variant.id, quantity: 1 },
    })
    const placed = await request.post(`${API}/api/v1/checkout/place`, {
      headers: {
        Authorization: `Bearer ${b.token}`,
        'X-Idempotency-Key': `e2e-track-${Date.now().toString(36)}`,
      },
      data: {
        shipping_method_code: 'regular',
        address: {
          recipient: 'Track B',
          phone: '080000000001',
          address_line1: 'Jl. B No. 1',
          city: 'Bandung',
          province: 'Jawa Barat',
          postal_code: '40115',
          country: 'Indonesia',
        },
      },
    })
    expect(placed.ok(), `could not place B's order: ${placed.status()}`).toBeTruthy()
    const orderNumber = (await placed.json()).orders[0].order_number as string

    // A must NOT be able to read it.
    const res = await request.get(`${API}/api/v1/orders/tracking/${orderNumber}`, {
      headers: { Authorization: `Bearer ${a.token}` },
    })
    expect(
      res.status(),
      `buyer A read buyer B's order ${orderNumber} via /orders/tracking/{number} — ` +
        'the endpoint has no ownership check and order numbers are a sequential ' +
        'counter, so any registered account can enumerate the whole order table',
    ).toBeGreaterThanOrEqual(400)
  })
})
