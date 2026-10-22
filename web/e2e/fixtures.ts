import { expect, type APIRequestContext, type Page } from '@playwright/test'

/**
 * Shared helpers for the e2e suite.
 *
 * Everything here exists to keep the suite re-runnable. The original smoke
 * spec leaned on the seed's fixed demo data and on state left behind by earlier
 * tests, so the second run of a suite against the same database silently
 * asserted nothing: the payments test found no pending order and returned early
 * (a green test with zero assertions), and the moderation/follow tests appended
 * a new row every run until the admin queue became unreadable.
 */

export const API = 'http://localhost:8080'

/**
 * Per-run suffix. Every fixture that mutates shared state keys off this, so a
 * second run never collides with the first run's rows.
 */
export const RUN_ID = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`

export const BUYER = { email: 'buyer.sample@vincommerce.com', password: 'BuyerPass123!' }
export const SELLER = { email: 'seller.elektro@vincommerce.com', password: 'SellerPass123!' }
export const ADMIN = { email: 'admin@vincommerce.com', password: 'AdminPass123!' }

/**
 * Cached per account, briefly.
 *
 * /auth/login is limited to 10/minute per IP, and the whole suite shares one IP:
 * twenty-odd API logins plus a browser login per spec ran the limiter over, and a
 * 429 does not fail a login -- it costs a Retry-After wait that was LONGER than the
 * test timeout, so the test was killed mid-wait with no message at all.
 *
 * The TTL is 90 seconds: short enough that no token can expire underneath a
 * long-running spec, long enough to collapse a file's worth of logins into one.
 */
const LOGIN_CACHE_TTL_MS = 90_000
const loginCache = new Map<string, { token: string; at: number }>()
const registeredAccounts = new Map<string, { token: string; at: number }>()

export async function login(
  request: APIRequestContext,
  who: { email: string; password: string },
): Promise<string> {
  const hit = loginCache.get(who.email)
  if (hit && Date.now() - hit.at < LOGIN_CACHE_TTL_MS) {
    return hit.token
  }
  const res = await request.post(`${API}/api/v1/auth/login`, {
    data: { email: who.email, password: who.password },
  })
  expect(res.ok(), `login failed for ${who.email}: ${res.status()}`).toBeTruthy()
  const token = (await res.json()).access_token as string
  loginCache.set(who.email, { token, at: Date.now() })
  return token
}

/**
 * Registers a brand-new buyer and returns their access token.
 *
 * Used instead of the seeded buyer wherever a test consumes a per-user
 * resource. `coupons.per_user_limit` is 1 for every seeded coupon and
 * `coupon_usages` is never truncated, so WELCOME10 is redeemable exactly once
 * by buyer.sample — forever. A checkout test that spends it against the shared
 * account passes on the first run and fails (or, worse, asserts nothing) on
 * every one after. A throwaway account per run removes that whole class of
 * order-dependence.
 */
export async function registerBuyer(
  request: APIRequestContext,
  tag: string,
): Promise<{ token: string; email: string }> {
  const email = `e2e-${tag}-${RUN_ID}@vincommerce.test`
  const password = 'E2eFixture123!'

  // A RETRY of this spec re-runs this function, and the email is derived from the
  // tag and the run, so it is the SAME account. The users table has a unique email
  // and no ON CONFLICT clause, so re-registering is a 409. Re-use the account
  // instead: log in with the same credentials.
  const known = registeredAccounts.get(tag)
  if (known && Date.now() - known.at < LOGIN_CACHE_TTL_MS) {
    return { token: known.token, email }
  }

  // /auth/register is limited to 10/minute per IP, and that is the endpoint doing
  // its job. The suite is simply louder than a person: eight call sites, each
  // retried by Playwright when a test fails, so a single 429 becomes several more
  // registrations and the cascade is worse than the original failure.
  //
  // So wait it out rather than work around it. Bypassing the limit for tests would
  // mean the one thing this environment never exercises is the control protecting
  // account creation; waiting keeps the limiter honest and makes the suite
  // deterministic. `Retry-After` is set by the limiter itself.
  let res = await request.post(`${API}/api/v1/auth/register`, {
    data: { email, password, full_name: `E2E ${tag}`, device_name: 'playwright' },
  })
  for (let attempt = 0; attempt < 2 && res.status() === 429; attempt++) {
    const retryAfter = Number(res.headers()['retry-after'] ?? 65)
    await new Promise((resolve) => setTimeout(resolve, (retryAfter + 2) * 1000))
    res = await request.post(`${API}/api/v1/auth/register`, {
      data: { email, password, full_name: `E2E ${tag}`, device_name: 'playwright' },
    })
  }

  if (res.status() === 409) {
    // This account already exists -- a retry of this spec. Log in instead of
    // re-registering, so the retry is deterministic.
    const signIn = await request.post(`${API}/api/v1/auth/login`, {
      data: { email, password },
    })
    expect(signIn.ok(), `login for ${email} failed: ${signIn.status()}`).toBeTruthy()
    const token = (await signIn.json()).access_token as string
    registeredAccounts.set(tag, { token, at: Date.now() })
    return { token, email }
  }

  expect(res.ok(), `register failed for ${email}: ${res.status()} ${await res.text()}`).toBeTruthy()
  const token = (await res.json()).access_token as string
  registeredAccounts.set(tag, { token, at: Date.now() })
  return { token, email }
}

/** A product slug that is seeded, active and comfortably above the Rp50.000 coupon floor. */
export const BULK_SLUG = 'smartphone-aurora-x5-pro'

export interface Variant {
  id: string
  name: string
  price: number
  stock: number
  product_id: string
  product_name: string
}

export async function firstVariant(
  request: APIRequestContext,
  token: string,
  slug: string,
  minPrice = 0,
): Promise<Variant> {
  const res = await request.get(`${API}/api/v1/products/${slug}`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(res.ok(), `product ${slug} not found: ${res.status()}`).toBeTruthy()
  const product = (await res.json()).product
  // minPrice lets a test demand a unit above a coupon floor instead of hoping the
  // first in-stock variant happens to clear it.
  const v =
    product.variants.find(
      (x: { stock: number; price: number | string }) =>
        Number(x.stock) > 3 && Number(x.price) >= minPrice,
    ) ?? product.variants.find((x: { stock: number }) => x.stock > 3) ?? product.variants[0]
  return {
    id: v.id,
    name: v.name,
    price: Number(v.price),
    stock: Number(v.stock),
    product_id: product.id,
    product_name: product.name,
  }
}

const ADDRESS = {
  recipient: 'E2E Fixture',
  phone: '080000000000',
  address_line1: 'Jl. Test E2E No. 1',
  city: 'Bandung',
  province: 'Jawa Barat',
  postal_code: '40115',
  country: 'Indonesia',
}

/**
 * Drives a real checkout to a placed, `pending` order and returns it.
 *
 * This is what makes the payments test idempotent. Consuming a *seeded* pending
 * order is a one-shot operation: on the next run there is nothing left to
 * transition, and the test's `return` turns a would-be failure into a green run
 * with zero assertions. Creating the order here means the transition is always
 * exercised, on every run, against an order this test owns.
 */
export async function placeOrder(
  request: APIRequestContext,
  token: string,
  opts: { couponCode?: string; notes?: string } = {},
): Promise<{ id: string; order_number: string; total_amount: number; status: string }> {
  const auth = { Authorization: `Bearer ${token}` }
  const variant = await firstVariant(request, token, BULK_SLUG)

  // PUT is an absolute set, not a delta, so this is a deterministic starting
  // cart regardless of what earlier tests left behind.
  const put = await request.put(`${API}/api/v1/cart/items`, {
    headers: auth,
    data: { variant_id: variant.id, quantity: 1 },
  })
  expect(put.ok(), `cart update failed: ${put.status()}`).toBeTruthy()

  const body: Record<string, unknown> = {
    shipping_method_code: 'regular',
    address: ADDRESS,
  }
  if (opts.couponCode) body.coupon_code = opts.couponCode
  if (opts.notes) body.notes = opts.notes

  const res = await request.post(`${API}/api/v1/checkout/place`, {
    headers: {
      ...auth,
      // Unique per call: the API replays a stored result for a repeated key
      // instead of creating a second order, which would return
      // { replayed: true, grand_total: 0 } and assert nothing.
      'X-Idempotency-Key': `e2e-${RUN_ID}-${Math.random().toString(36).slice(2, 10)}`,
    },
    data: body,
  })
  expect(res.ok(), `checkout/place failed: ${res.status()} ${await res.text()}`).toBeTruthy()

  const placed = (await res.json()) as {
    replayed?: boolean
    orders: { id: string; order_number: string; total_amount: number; status: string }[]
  }
  expect(placed.replayed, 'checkout replayed a previous idempotency key').toBeUndefined()
  expect(placed.orders.length, 'no order was created').toBeGreaterThan(0)
  return placed.orders[0]
}

/** Removes a cart line so a test does not leak state into the next one. */
export async function clearCartLine(
  request: APIRequestContext,
  token: string,
  variantId: string,
): Promise<void> {
  await request.delete(`${API}/api/v1/cart/items/${variantId}`, {
    headers: { Authorization: `Bearer ${token}` },
  })
}

/** Signs a buyer in through the UI so the SPA holds a live session. */
export async function loginViaUi(page: Page, who: { email: string; password: string }): Promise<void> {
  await page.goto('/login')
  await page.getByLabel('Email', { exact: true }).fill(who.email)
  await page.getByLabel('Password', { exact: true }).fill(who.password)
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL('/', { timeout: 20_000 })
}
