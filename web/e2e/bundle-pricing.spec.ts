import { test, expect } from '@playwright/test'
import { API, registerBuyer, loginViaUi } from './fixtures'
import { attachDiagnostics } from './diagnostics'

/**
 * Bundle pricing: STRICT MATCH, actually charged, and explained to the buyer.
 *
 * `bundles.price` existed in the schema and nothing read it, so the storefront could
 * advertise a bundle price the quote ignored. The seeded "Aurora X5 Pro Duo" is two
 * units of the Black variant at 10% off.
 *
 * The rule under test is the one the schema implies: `bundle_items` is keyed on
 * (bundle_id, variant_id) with a quantity, so a bundle is a FIXED SET. At exactly
 * those quantities the discount applies; one unit more and the bundle no longer
 * describes the cart, so it must not.
 *
 * ASSERTING THE DELTA, not a hardcoded price: the test reads the same quote the
 * storefront reads, so it cannot drift from the seeded data the way a literal would.
 */
test('checkout: a strict-match bundle applies, and stops when the cart differs', async ({
  page,
  request,
}) => {
  attachDiagnostics(page)
  const { token, email } = await registerBuyer(request, 'bundle')
  const auth = { Authorization: `Bearer ${token}` }

  const product = await (
    await request.get(`${API}/api/v1/products/smartphone-aurora-x5-pro`, { headers: auth })
  ).json()
  const black = product.product.variants.find((v: { name: string }) => v.name === 'Black')
  expect(black, 'the seeded Black variant is missing').toBeTruthy()

  const setQty = (quantity: number) =>
    request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: black.id, quantity },
    })

  const quote = async () =>
    (
      await request.post(`${API}/api/v1/checkout/quote`, {
        headers: auth,
        data: { shipping_method_code: 'regular' },
      })
    ).json() as Promise<{
      subtotal: number
      discount_amount: number
      total: number
      bundles?: { id: string; name: string; discount: number }[]
    }>

  // Exactly the bundle's quantities: 2 x Black.
  expect((await setQty(2)).ok()).toBeTruthy()
  const exact = await quote()
  expect(exact.bundles?.length, 'the bundle did not apply at its exact quantity').toBe(1)
  const applied = exact.bundles![0]
  expect(applied.name, 'the quote must name the bundle for the summary').toBeTruthy()
  expect(applied.discount).toBeGreaterThan(0)
  // 10% off two units: a tenth of the subtotal, not of one unit.
  expect(exact.subtotal).toBeCloseTo(Number(black.price) * 2, 0)
  expect(exact.discount_amount).toBeCloseTo(Math.round(exact.subtotal * 0.1), 0)

  // The buyer must be TOLD. The summary renders "Diskon <coupon code>", so a bundle
  // that wins the best-of comparison without a name produced a bare "Diskon ".
  await loginViaUi(page, { email, password: 'E2eFixture123!' })
  await page.goto('/checkout')
  await expect(page.getByText(`Bundle ${applied.name}`)).toBeVisible({ timeout: 20_000 })

  // STRICT: one unit more and the bundle must not describe the cart any more.
  expect((await setQty(3)).ok()).toBeTruthy()
  const strict = await quote()
  expect(strict.bundles ?? [], 'a bundle applied to a cart it does not describe').toHaveLength(0)
  expect(strict.discount_amount, 'a discount survived the bundle not matching').toBe(0)
})
