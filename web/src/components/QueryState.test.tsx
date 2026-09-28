import { describe, expect, it, vi } from 'vitest'
import { QueryState, type QueryStateLike } from './QueryState'
import { render } from '../test/render'

const query = (over: Partial<QueryStateLike> = {}): QueryStateLike => ({
  isLoading: false,
  isError: false,
  error: null,
  refetch: () => Promise.resolve(),
  ...over,
})

describe('QueryState', () => {
  it('renders a live skeleton while the query is loading', async () => {
    const view = await render(
      <QueryState query={query({ isLoading: true })} label="produk">
        <p>isi</p>
      </QueryState>,
    )
    const box = view.find('[role="status"]')
    expect(box).not.toBeNull()
    expect(box?.getAttribute('aria-busy')).toBe('true')
    expect(view.text()).toContain('Memuat produk...')
    expect(view.text()).not.toContain('isi')
    await view.unmount()
  })

  it('renders an accessible error block with the message and a retry button', async () => {
    const refetch = vi.fn(() => Promise.resolve())
    const view = await render(
      <QueryState
        query={query({ isError: true, error: new Error('Network Error'), refetch })}
        label="keranjang"
      >
        <p>isi</p>
      </QueryState>,
    )
    const alert = view.find('[role="alert"]')
    expect(alert).not.toBeNull()
    expect(alert?.getAttribute('aria-live')).toBe('assertive')
    expect(view.text()).toContain('Gagal memuat keranjang')
    expect(view.text()).toContain('Network Error')
    expect(view.text()).not.toContain('isi')

    await view.click(view.find('button'))
    expect(refetch).toHaveBeenCalledTimes(1)
    await view.unmount()
  })

  it('falls back to a generic message for a non-Error rejection', async () => {
    const view = await render(
      <QueryState query={query({ isError: true, error: { code: 'X' } })} label="pesanan" />,
    )
    expect(view.text()).toContain('Gagal memuat pesanan.')
    await view.unmount()
  })

  it('passes a string rejection through as the message', async () => {
    const view = await render(
      <QueryState query={query({ isError: true, error: 'Server sedang maintenance' })} label="notifikasi" />,
    )
    expect(view.text()).toContain('Server sedang maintenance')
    await view.unmount()
  })

  it('renders children when the query has settled successfully', async () => {
    const view = await render(
      <QueryState query={query()} label="produk">
        <p>hasil</p>
      </QueryState>,
    )
    expect(view.text()).toBe('hasil')
    expect(view.find('[role="alert"]')).toBeNull()
    expect(view.find('[role="status"]')).toBeNull()
    await view.unmount()
  })

  it('prefers the error over children once the query fails after having loaded', async () => {
    const view = await render(
      <QueryState query={query()} label="tokonya">
        <p>hasil</p>
      </QueryState>,
    )
    await view.rerender(
      <QueryState query={query({ isError: true, error: new Error('500') })} label="tokonya">
        <p>hasil</p>
      </QueryState>,
    )
    expect(view.text()).toContain('Gagal memuat tokonya')
    expect(view.text()).not.toContain('hasil')
    await view.unmount()
  })
})
