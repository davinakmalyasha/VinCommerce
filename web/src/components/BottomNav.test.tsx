import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { render } from '../test/render'
import { BottomNav } from './layout/BottomNav'

function shell(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <BottomNav />
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('BottomNav', () => {
  it('renders the five primary destinations', async () => {
    const r = await render(shell('/'))
    const nav = r.find('nav[aria-label="Navigasi utama"]')
    expect(nav).not.toBeNull()
    for (const label of ['Beranda', 'Cari', 'Keranjang', 'Pesanan', 'Akun']) {
      expect(nav!.textContent).toContain(label)
    }
    await r.unmount()
  })

  it('marks only the current route active', async () => {
    const r = await render(shell('/orders'))
    const active = r.findAll('a[aria-current="page"]')
    expect(active).toHaveLength(1)
    expect(active[0].textContent).toContain('Pesanan')
    await r.unmount()
  })

  it('is hidden on desktop widths', async () => {
    const r = await render(shell('/'))
    const nav = r.find('nav[aria-label="Navigasi utama"]')
    expect(nav!.className).toContain('md:hidden')
    await r.unmount()
  })
})
