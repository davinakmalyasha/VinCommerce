import { test, expect } from '@playwright/test'
import { ADMIN, BUYER, loginViaUi, registerBuyer } from './fixtures'

/**
 * Authorization at the route level.
 *
 * A client-side role check is UX, not security — the API must reject the call
 * regardless. The two halves are asserted separately, because the failure mode
 * that matters is a page that renders admin DATA to a buyer, which requires both
 * a broken role gate on the client and a broken check on the server.
 */
test.describe('authorization', () => {
  test('an admin-only route renders a denied state for a logged-in buyer', async ({ page }) => {
    await loginViaUi(page, BUYER)
    await page.goto('/admin/reports')

    // A real denial, not a crash and not an empty page.
    await expect(page.getByRole('heading', { name: 'Akses Ditolak' })).toBeVisible({
      timeout: 20_000,
    })
    await expect(page.getByText('Halaman ini khusus administrator.')).toBeVisible()

    // Nothing from the admin surface leaked into the DOM.
    await expect(page.getByRole('link', { name: 'Konsol Admin' })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'Audit Log' })).toHaveCount(0)
  })

  test('every admin route denies a buyer, not just the one that was checked', async ({ page }) => {
    await loginViaUi(page, BUYER)
    for (const path of ['/admin', '/admin/users', '/admin/payouts', '/admin/audit']) {
      await page.goto(path)
      await expect(
        page.getByRole('heading', { name: 'Akses Ditolak' }),
        `${path} rendered something other than a denial for a buyer`,
      ).toBeVisible({ timeout: 20_000 })
    }
  })

  test('an anonymous visitor is denied, not shown the admin surface', async ({ page }) => {
    await page.goto('/admin/reports')
    // AdminLayout gates on `!user?.roles.includes('admin')`, so an anonymous
    // visitor gets the same denial as a buyer — the point is that it is a
    // denial, not a redirect loop and not a half-rendered admin shell.
    await expect(page.getByRole('heading', { name: 'Akses Ditolak' })).toBeVisible({
      timeout: 20_000,
    })
  })

  test('a freshly registered buyer is refused by the admin API (401/403, never 200)', async ({
    request,
  }) => {
    const { token } = await registerBuyer(request, 'authz')
    for (const p of [
      '/api/v1/admin/reports',
      '/api/v1/admin/users',
      '/api/v1/admin/analytics',
      '/api/v1/admin/commission',
    ]) {
      const res = await request.get(`http://localhost:8080${p}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      expect(
        [401, 403],
        `${p} returned ${res.status()} to a non-admin buyer`,
      ).toContain(res.status())
    }
  })

  test('an admin can reach the same route the buyer was denied', async ({ page }) => {
    // Confirms the denial above is about the role and not about the route
    // itself being broken.
    await loginViaUi(page, ADMIN)
    await page.goto('/admin/reports')
    await expect(page.getByRole('heading', { name: 'Akses Ditolak' })).toHaveCount(0)
    await expect(page.getByText('Konsol Admin')).toBeVisible({ timeout: 20_000 })
  })
})
