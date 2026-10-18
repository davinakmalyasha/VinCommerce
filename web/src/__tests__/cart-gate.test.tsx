import { act } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { render } from '../test/render'
import { CartPage } from '../pages/CartPage'
import { useSession } from '../stores/session'

/**
 * The cart query must not run until `restore()` has decided who the caller is.
 *
 * The access token lives in memory only, so a full page load begins with none and
 * `session.restore()` has to rotate the httpOnly refresh cookie asynchronously. A
 * cart request issued in that window carries only `X-Session-Key`; the handler then
 * takes the guest branch and answers with the EMPTY GUEST CART, which react-query
 * caches under the shared CART_KEY. A logged-in customer reloading /cart was shown
 * "Keranjangmu masih kosong."
 *
 * This pins the ORDERING rather than the rendered output: the assertion is that no
 * cart request is made while `loading` is true, and that one is made once the store
 * says otherwise. Deleting `enabled: !sessionLoading` makes the first assertion
 * fail, which is the mutation this file exists to catch.
 */

// `vi.mock` is hoisted above every top-level declaration, so the spy has to be
// created inside `vi.hoisted` or the factory closes over an uninitialised binding.
const { cartGet } = vi.hoisted(() => ({
  cartGet: vi.fn(async () => ({ data: { lines: [], free_shipping: [] } })),
}))

vi.mock('../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api')>()
  // Only the transport call is replaced, so every other export (token helpers the
  // session store needs) stays real.
  actual.api.get = cartGet as never
  return actual
})

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )
}

/** Flush microtasks and React work until `check` stops throwing, or give up. */
async function until(check: () => void, budgetMs = 2000) {
  const deadline = Date.now() + budgetMs
  for (;;) {
    try {
      check()
      return
    } catch (err) {
      if (Date.now() > deadline) throw err
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10))
      })
    }
  }
}

describe('cart query waits for session restore', () => {
  it('asks as the real owner once restore finishes, and not before', async () => {
    cartGet.mockClear()
    useSession.setState({ loading: true })

    const r = await render(wrap(<CartPage />))

    // The whole point: no identity is known yet, so no request may be sent. Sent
    // now, it would be answered with the guest cart and cached as the user's.
    expect(cartGet).not.toHaveBeenCalled()

    await act(async () => {
      useSession.setState({ loading: false })
    })
    await until(() => expect(cartGet).toHaveBeenCalledWith('/cart'))

    await r.unmount()
  })
})
