import { test, expect } from '@playwright/test'
import { API, registerBuyer } from './fixtures'

/**
 * Bundle pricing: STRICT MATCH, and it is actually charged.
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
  request,
}) => {
  const { token } = await registerBuyer(request, 'bundle')
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
      bundle_ids?: string[]
    }>

  // Exactly the bundle's quantities: 2 x Black.
  expect((await setQty(2)).ok()).toBeTruthy()
  const exact = await quote()
  expect(
    exact.bundle_ids?.length,
    'the bundle did not apply at its exact quantity',
  ).toBe(1)
  expect(exact.discount_amount).toBeGreaterThan(0)
  // 10% off two units: the discount is a tenth of the subtotal, not of one unit.
  expect(exact.subtotal).toBeCloseTo(Number(black.price) * 2, 0)
  expect(exact.discount_amount).toBeCloseTo(Math.round(exact.subtotal * 0.1), 0)

  // STRICT: one unit more and the bundle must not describe the cart any more.
  expect((await setQty(3)).ok()).toBeTruthy()
  const strict = await quote()
  expect(strict.bundle_ids ?? [], 'a bundle applied to a cart it does not describe').toHaveLength(0)
  expect(strict.discount_amount, 'a discount survived the bundle not matching').toBe(0)
})
