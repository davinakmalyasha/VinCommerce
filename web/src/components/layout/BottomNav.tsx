import { NavLink } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import type { CartLine } from '../../types'

const ITEMS = [
  { to: '/', label: 'Beranda', icon: '🏠', end: true },
  { to: '/search', label: 'Cari', icon: '🔍', end: false },
  { to: '/cart', label: 'Keranjang', icon: '🛒', end: false },
  { to: '/orders', label: 'Pesanan', icon: '📦', end: false },
  { to: '/account', label: 'Akun', icon: '👤', end: false },
] as const

/**
 * Bottom tab bar for small screens.
 *
 * The header already collapses search to an icon below `sm`, but primary
 * destinations were still a hamburger-less header away on a phone: no tab bar
 * existed at all. This renders only below `md`, mirroring the header's
 * breakpoint thinking in reverse.
 *
 * The cart badge reuses the shared ['cart'] key, so it never fires its own
 * request when the header or cart page already populated the cache. Guests
 * get no badge: the guest cart needs an open drawer to resolve identity, and
 * a badge of 0 is worse than none.
 */
export function BottomNav() {
  const { user } = useSession()
  const { data: cartData } = useQuery({
    queryKey: ['cart'],
    queryFn: async () => (await api.get<{ lines: CartLine[] }>('/cart')).data,
    enabled: !!user,
    staleTime: 30_000,
  })
  const cartCount = (cartData?.lines ?? []).reduce((sum, l) => sum + l.quantity, 0)

  return (
    <nav
      aria-label="Navigasi utama"
      className="md:hidden fixed bottom-0 inset-x-0 z-40 bg-white dark:bg-gray-900 border-t border-gray-200 dark:border-gray-700 pb-[env(safe-area-inset-bottom)]"
    >
      <div className="grid grid-cols-5">
        {ITEMS.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className={({ isActive }) =>
              `relative flex flex-col items-center gap-0.5 py-2 text-[10px] ${
                isActive ? 'text-amber-600 font-semibold' : 'text-gray-500 dark:text-gray-400'
              }`
            }
          >
            <span aria-hidden="true" className="text-xl leading-none">
              {item.icon}
            </span>
            {item.label}
            {item.to === '/cart' && cartCount > 0 && (
              <span className="absolute top-1 right-1/2 translate-x-5 bg-red-600 text-white text-[10px] rounded-full min-w-4 h-4 flex items-center justify-center px-1">
                {cartCount}
              </span>
            )}
          </NavLink>
        ))}
      </div>
    </nav>
  )
}
