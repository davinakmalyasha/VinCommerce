import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }))

vi.mock('../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api')>()
  actual.api.get = apiGet as never
  return { ...actual, refreshAccessToken: vi.fn(async () => 'admin-token') }
})

import { bindQueryCacheClear, useSession } from '../stores/session'

/**
 * Entering and leaving impersonation are IDENTITY CHANGES, and they must purge the
 * per-user caches exactly as login and logout do.
 *
 * `login`, `register` and `logout` all call the bound clearer; the two impersonation
 * transitions did not. So an admin who entered impersonation kept every cached
 * response belonging to the ADMIN while the banner announced they were acting as
 * someone else -- cart, orders, notifications and admin tables all rendering the
 * wrong person's data until each query happened to refetch. That is the same class
 * of lie the banner (and its silent-revert detection) exists to prevent.
 *
 * The assertion is deliberately on the ORDERING-free fact that a purge happened on
 * each transition: deleting either call makes this file fail.
 */
describe('impersonation transitions purge cached user data', () => {
  beforeEach(() => {
    apiGet.mockReset()
  })

  it('purges on the way in and on the way out', async () => {
    const purge = vi.fn()
    bindQueryCacheClear(purge)

    useSession
      .getState()
      .startImpersonation('impersonated-token', {
        id: 'u2',
        email: 'buyer@vincommerce.test',
        full_name: 'Buyer',
      } as never)
    expect(purge, 'entering impersonation left the admin’s cached data in place').toHaveBeenCalledTimes(1)

    apiGet.mockResolvedValueOnce({
      data: { user: { id: 'a1', email: 'admin@vincommerce.test', full_name: 'Admin' } },
    })
    await useSession.getState().stopImpersonation()
    expect(purge, 'leaving impersonation left the impersonated data in place').toHaveBeenCalledTimes(2)
  })
})
