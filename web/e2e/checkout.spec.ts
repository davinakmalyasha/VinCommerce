import { test, expect } from '@playwright/test'
import { API, BUYER, firstVariant, registerBuyer } from './fixtures'

/**
 * Coupon happy path.
 *
 * The gap this closes: the suite had no test that placed an order with a coupon
 * at all, so the whole discount path — quote, per-user redemption limit, the
 * grand total actually being reduced — was unverified. `WELCOME10` is 10% off
 * with a Rp50.000 floor, and `per_user_limit` is 1 for every seeded coupon with
 * no truncation of `coupon_usages`, so this runs against a throwaway account
 * created per run. Against the shared buyer it would pass once and then be
 * unsatisfiable forever.
 */
test.describe('checkout: coupon happy path', () => {
  test('WELCOME10 discounts the quote and the placed order', async ({ request }) => {
    const { token } = await registerBuyer(request, 'coupon')
    const auth = { Authorization: `Bearer ${token}` }

    const variant = await firstVariant(request, token, 'smartphone-aurora-x5-pro')
    // Guard the premise rather than letting a fixture regression show up as a
    // confusing "discount is zero" failure.
    expect(
      variant.price,
      'WELCOME10 has a Rp50.000 floor, so the fixture must be a single unit above it',
    ).toBeGreaterThan(50_000)

    const add = await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: variant.id, quantity: 1 },
    })
    expect(add.ok()).toBeTruthy()

    // ── Quote: the server, not the client, decides the money ───────────────
    const quoted = await request.post(`${API}/api/v1/checkout/quote`, {
      headers: auth,
      data: { coupon_code: 'WELCOME10', shipping_method_code: 'regular' },
    })
    expect(quoted.ok(), `quote failed: ${quoted.status()} ${await quoted.text()}`).toBeTruthy()
    const quote = (await quoted.json()) as {
      subtotal: number
      discount_amount: number
      total: number
      coupon_code?: string
    }

    expect(quote.coupon_code).toBe('WELCOME10')
    expect(quote.discount_amount).toBeGreaterThan(0)
    // 10% off, and NOT more than 10% off — a discount larger than the coupon's
    // own rate is how a buyer gets charged less than quoted.
    expect(quote.discount_amount).toBeCloseTo(Math.round(quote.subtotal * 0.1), 0)
    expect(quote.total).toBeCloseTo(quote.subtotal - quote.discount_amount, 0)

    // ── Place: the placed order must carry the same discount ───────────────
    const placed = await request.post(`${API}/api/v1/checkout/place`, {
      headers: { ...auth, 'X-Idempotency-Key': `coupon-${Date.now()}` },
      data: {
        coupon_code: 'WELCOME10',
        shipping_method_code: 'regular',
        address: {
          recipient: 'E2E Coupon',
          phone: '080000000000',
          address_line1: 'Jl. Kupon No. 1',
          city: 'Bandung',
          province: 'Jawa Barat',
          postal_code: '40115',
          country: 'Indonesia',
        },
      },
    })
    expect(placed.ok(), `place failed: ${placed.status()} ${await placed.text()}`).toBeTruthy()
    const body = (await placed.json()) as { orders: { id: string; total_amount: number }[] }
    expect(body.orders.length).toBeGreaterThan(0)
    // Shipping may be added on top, but the merchandise must not exceed the
    // undiscounted subtotal.
    expect(body.orders[0].total_amount).toBeLessThanOrEqual(quote.subtotal)

    const order = await request.get(`${API}/api/v1/orders/${body.orders[0].id}`, {
      headers: auth,
    })
    expect(order.ok()).toBeTruthy()
    const detail = (await order.json()) as {
      order: { discount_amount: number; coupon_code?: string; status: string }
    }
    expect(detail.order.coupon_code).toBe('WELCOME10')
    expect(detail.order.discount_amount).toBeGreaterThan(0)
    expect(detail.order.status).toBe('pending')
  })

  test('WELCOME10 is refused a second time for the same buyer (per_user_limit)', async ({
    request,
  }) => {
    // The limit is the other half of the coupon contract: without it a single
    // code could be drained repeatedly. Fresh account, so this is a clean slate.
    const { token } = await registerBuyer(request, 'coupon-limit')
    const auth = { Authorization: `Bearer ${token}` }
    const variant = await firstVariant(request, token, 'smartphone-aurora-x5-pro')

    await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: variant.id, quantity: 1 },
    })

    const first = await request.post(`${API}/api/v1/checkout/place`, {
      headers: { ...auth, 'X-Idempotency-Key': `limit-1-${Date.now()}` },
      data: {
        coupon_code: 'WELCOME10',
        shipping_method_code: 'regular',
        address: {
          recipient: 'E2E Limit',
          phone: '080000000000',
          address_line1: 'Jl. Batas No. 1',
          city: 'Bandung',
          province: 'Jawa Barat',
          postal_code: '40115',
          country: 'Indonesia',
        },
      },
    })
    expect(first.ok(), await first.text()).toBeTruthy()

    // Second cart, second order, same buyer, same coupon.
    await request.put(`${API}/api/v1/cart/items`, {
      headers: auth,
      data: { variant_id: variant.id, quantity: 1 },
    })
    const second = await request.post(`${API}/api/v1/checkout/place`, {
      headers: { ...auth, 'X-Idempotency-Key': `limit-2-${Date.now()}` },
      data: {
        coupon_code: 'WELCOME10',
        shipping_method_code: 'regular',
        address: {
          recipient: 'E2E Limit',
          phone: '080000000000',
          address_line1: 'Jl. Batas No. 1',
          city: 'Bandung',
          province: 'Jawa Barat',
          postal_code: '40115',
          country: 'Indonesia',
        },
      },
    })
    expect(second.status(), 'per_user_limit=1 was not enforced on a second redemption').toBe(409)
  })

  test('seeded buyer login still works (fixture sanity)', async ({ request }) => {
    // Cheap guard so a broken seed fails here with a clear message rather than
    // as an unexplained 401 somewhere in the middle of the suite.
    const res = await request.post(`${API}/api/v1/auth/login`, {
      data: { email: BUYER.email, password: BUYER.password },
    })
    expect(res.ok(), `seeded buyer could not log in: ${res.status()}`).toBeTruthy()
  })
})
