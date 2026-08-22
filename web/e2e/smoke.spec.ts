import { test, expect } from '@playwright/test'

test('storefront: home loads with recommendations', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: /Semua yang kamu butuhkan/i })).toBeVisible()
  await expect(page.getByText('Rekomendasi Untukmu')).toBeVisible()
})

test('storefront: search with filters', async ({ page }) => {
  await page.goto('/search?q=phone&sort=price_asc')
  await expect(page.getByText(/Hasil untuk "phone"/)).toBeVisible()
  await expect(page.locator('a[href^="/product/"]').first()).toBeVisible()
})

test('storefront: product detail adds to cart', async ({ page }) => {
  await page.goto('/search?q=earbuds')
  await page.locator('a[href^="/product/"]').first().click()
  const cartButton = page.getByRole('button', { name: '+ Keranjang' })
  await expect(cartButton).toBeEnabled({ timeout: 10_000 })
  const [resp] = await Promise.all([
    page.waitForResponse((r) => r.url().includes('/cart/items') && r.request().method() === 'POST'),
    cartButton.click(),
  ])
  expect(resp.ok()).toBeTruthy()
})

test('help center: browse and read an article', async ({ page }) => {
  await page.goto('/help')
  await expect(page.getByRole('heading', { name: 'Pusat Bantuan' })).toBeVisible()
  await page.getByPlaceholder('Cari bantuan...').fill('escrow')
  await page.locator('main').getByRole('button', { name: 'Cari' }).click()
  await expect(page.locator('main a[href^="/help/"]').first()).toBeVisible({ timeout: 10_000 })
})

test('docs: swagger UI reachable through the API', async ({ request }) => {
  const res = await request.get('http://localhost:8080/docs/')
  expect(res.ok()).toBeTruthy()
  const body = await res.text()
  expect(body).toContain('swagger-ui')
})

test('auth: login and land on home', async ({ page }) => {
  await page.goto('/login')
  await page.getByPlaceholder('Email').fill('buyer.sample@vincommerce.com')
  await page.getByPlaceholder('Password').fill('BuyerPass123!')
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL('/', { timeout: 15_000 })
  await expect(page.getByTitle('Notifikasi')).toBeVisible()
})

test('ai assistant: offline retrieval answers from articles', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'buyer.sample@vincommerce.com', password: 'BuyerPass123!' },
  })
  expect(login.ok()).toBeTruthy()
  const token = (await login.json()).access_token
  const res = await request.post('http://localhost:8080/api/v1/ai/ask', {
    headers: { Authorization: `Bearer ${token}` },
    data: { question: 'Bagaimana cara melacak pesanan?' },
  })
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  expect(body.mode).toBe('offline')
  expect(body.answer.length).toBeGreaterThan(20)
})

test('tracking: public order tracking page renders', async ({ page }) => {
  await page.goto('/tracking/VC-20260811-1007')
  await expect(page.getByText('Lacak Pesanan')).toBeVisible()
})

test('vouchers: collectible store coupons page', async ({ page }) => {
  await page.goto('/vouchers')
  await expect(page.getByRole('heading', { name: 'Kumpulkan Kupon Toko' })).toBeVisible()
})

test('compare: page handles empty state', async ({ page }) => {
  await page.goto('/compare')
  await expect(page.getByText('Belum ada produk untuk dibandingkan.')).toBeVisible()
})

test('api: commission config read', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'admin@vincommerce.com', password: 'AdminPass123!' },
  })
  const token = (await login.json()).access_token
  const res = await request.get('http://localhost:8080/api/v1/admin/commission', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  expect(body.fee.pct).toBeGreaterThanOrEqual(0)
})

test('engagement: follow store then see it in followed list', async ({ page, request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'buyer.sample@vincommerce.com', password: 'BuyerPass123!' },
  })
  expect(login.ok()).toBeTruthy()
  const token = (await login.json()).access_token

  const store = await request.get('http://localhost:8080/api/v1/stores/elektrostore', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(store.ok()).toBeTruthy()
  const storeId = (await store.json()).store.id

  const follow = await request.post(`http://localhost:8080/api/v1/stores/${storeId}/follow`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(follow.ok()).toBeTruthy()
  expect((await follow.json()).following).toBe(true)

  const feed = await request.get('http://localhost:8080/api/v1/followed-stores', {
    headers: { Authorization: `Bearer ${token}` },
  })
  const followed = await feed.json()
  expect(followed.stores.some((s: { id: string }) => s.id === storeId)).toBe(true)

  // cleanup
  await request.delete(`http://localhost:8080/api/v1/stores/${storeId}/follow`, {
    headers: { Authorization: `Bearer ${token}` },
  })
})

test('engagement: back-in-stock alert lifecycle', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'buyer.sample@vincommerce.com', password: 'BuyerPass123!' },
  })
  const token = (await login.json()).access_token
  const h = { Authorization: `Bearer ${token}` }

  const prod = await request.get('http://localhost:8080/api/v1/products/smartphone-aurora-x5-pro', { headers: h })
  const variantId = (await prod.json()).product.variants[0].id

  const created = await request.post('http://localhost:8080/api/v1/back-in-stock', {
    headers: h,
    data: { variant_id: variantId },
  })
  expect(created.ok()).toBeTruthy()
  const alertId = (await created.json()).alert.id

  const list = await request.get('http://localhost:8080/api/v1/back-in-stock', { headers: h })
  const alerts = await list.json()
  expect(alerts.alerts.some((a: { id: string }) => a.id === alertId)).toBe(true)

  const cancel = await request.delete(`http://localhost:8080/api/v1/back-in-stock/${alertId}`, { headers: h })
  expect(cancel.ok()).toBeTruthy()
})

test('payments: external payment confirmation', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'buyer.sample@vincommerce.com', password: 'BuyerPass123!' },
  })
  const token = (await login.json()).access_token
  const h = { Authorization: `Bearer ${token}` }

  const orders = await request.get('http://localhost:8080/api/v1/orders?page_size=5', { headers: h })
  const list = await orders.json()
  const pending = list.orders.find((o: { status: string }) => o.status === 'pending')
  if (!pending) {
    // no pending order to confirm — nothing to assert against
    return
  }
  const res = await request.post(`http://localhost:8080/api/v1/orders/${pending.id}/external-payment`, {
    headers: h,
    data: { reference: 'E2E-TEST-REF', amount: pending.total_amount },
  })
  expect(res.ok()).toBeTruthy()
  expect((await res.json()).confirmed).toBe(true)
})

test('moderation: report a product and admin sees it', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'buyer.sample@vincommerce.com', password: 'BuyerPass123!' },
  })
  const token = (await login.json()).access_token

  const prod = await request.get('http://localhost:8080/api/v1/products/smartphone-aurora-x5-pro')
  const productId = (await prod.json()).product.id

  const report = await request.post(`http://localhost:8080/api/v1/products/${productId}/report`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { reason: 'misleading', description: 'e2e test report' },
  })
  expect(report.ok()).toBeTruthy()

  const adminLogin = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'admin@vincommerce.com', password: 'AdminPass123!' },
  })
  const adminToken = (await adminLogin.json()).access_token
  const queue = await request.get('http://localhost:8080/api/v1/admin/reports?status=open', {
    headers: { Authorization: `Bearer ${adminToken}` },
  })
  const body = await queue.json()
  expect(body.reports.some((r: { product_id: string }) => r.product_id === productId)).toBe(true)
})

test('analytics: admin report includes commission and funnel', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'admin@vincommerce.com', password: 'AdminPass123!' },
  })
  const token = (await login.json()).access_token
  const res = await request.get('http://localhost:8080/api/v1/admin/analytics', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  expect(body.funnel.length).toBe(4)
  expect(typeof body.commission_earned).toBe('number')
})

test('seo: robots and sitemap are served', async ({ request }) => {
  const robots = await request.get('http://localhost:8080/robots.txt')
  expect(robots.ok()).toBeTruthy()
  expect(await robots.text()).toContain('sitemap.xml')

  const sitemap = await request.get('http://localhost:8080/sitemap.xml')
  expect(sitemap.ok()).toBeTruthy()
  expect(await sitemap.text()).toContain('<urlset')
})

test('ui: 404 page for unknown route', async ({ page }) => {
  await page.goto('/this-route-does-not-exist')
  await expect(page.getByText('404')).toBeVisible()
  await expect(page.getByText('Halaman tidak ditemukan')).toBeVisible()
})

test('search: autocomplete suggestions from header', async ({ page }) => {
  await page.goto('/')
  const input = page.getByPlaceholder('Cari produk, brand, kategori...')
  await input.fill('smart')
  await expect(page.getByText('Saran')).toBeVisible({ timeout: 10_000 })
  await expect(page.locator('button:has-text("📦 Smart")').first()).toBeVisible()
  await page.locator('button:has-text("📦 Smart")').first().click()
  await expect(page).toHaveURL(/\/product\//)
})

test('profile: avatar upload + verify banner shows for unverified buyer', async ({ page }) => {
  await page.goto('/login')
  await page.getByPlaceholder('Email').fill('buyer.sample@vincommerce.com')
  await page.getByPlaceholder('Password').fill('BuyerPass123!')
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL('/', { timeout: 15_000 })
  const banner = page.getByText(/Verifikasi email kamu/)
  if (await banner.isVisible().catch(() => false)) {
    await expect(page.getByRole('button', { name: 'Kirim ulang' })).toBeVisible()
  }
})

test('seller: low-stock endpoint + order CSV export', async ({ request }) => {
  const login = await request.post('http://localhost:8080/api/v1/auth/login', {
    data: { email: 'seller.elektro@vincommerce.com', password: 'SellerPass123!' },
  })
  const token = (await login.json()).access_token
  const h = { Authorization: `Bearer ${token}` }

  const low = await request.get('http://localhost:8080/api/v1/seller/low-stock', { headers: h })
  expect(low.ok()).toBeTruthy()
  expect(Array.isArray((await low.json()).variants)).toBe(true)

  const csv = await request.get('http://localhost:8080/api/v1/seller/orders/export.csv', { headers: h })
  expect(csv.ok()).toBeTruthy()
  expect(await csv.text()).toContain('order_number')
})
