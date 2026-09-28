import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import type { CartLine } from '../../types'
import { formatIDR } from '../../lib/format'
import { NotificationBell } from './NotificationBell'
import { Modal } from '../Modal'
import { useTheme } from '../../stores/theme'

interface Suggestion {
  name: string
  slug: string
}

export function Header() {
  const { user, logout } = useSession()
  const navigate = useNavigate()
  const dark = useTheme((s) => s.dark)
  const toggleDark = useTheme((s) => s.toggle)
  const [query, setQuery] = useState('')
  const [cartOpen, setCartOpen] = useState(false)
  const [showRecent, setShowRecent] = useState(false)
  const [debounced, setDebounced] = useState('')

  useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), 250)
    return () => clearTimeout(t)
  }, [query])

  const { data: suggestions } = useQuery({
    queryKey: ['suggestions', debounced],
    queryFn: async () =>
      (
        await api.get<{ products: Suggestion[]; categories: Suggestion[]; brands: Suggestion[] }>(
          `/search/suggestions?q=${encodeURIComponent(debounced)}&limit=5`,
        )
      ).data,
    enabled: debounced.length >= 2,
    staleTime: 30_000,
  })

  const { data: cart } = useQuery({
    queryKey: ['cart'],
    queryFn: async () => {
      const res = await api.get<{ lines: CartLine[] }>('/cart')
      return res.data.lines
    },
    enabled: cartOpen || !!user,
  })

  const cartCount = (cart ?? []).reduce((sum, l) => sum + l.quantity, 0)

  const readList = (key: string): string[] => {
    try {
      return JSON.parse(localStorage.getItem(key) ?? '[]')
    } catch {
      return []
    }
  }

  const writeList = (key: string, items: string[]) => localStorage.setItem(key, JSON.stringify(items.slice(0, 8)))

  const addRecent = (q: string) => {
    const t = q.trim()
    if (!t) return
    writeList('vc_recent_search', [t, ...readList('vc_recent_search').filter((x) => x !== t)])
  }

  const submitSearch = (e: React.FormEvent) => {
    e.preventDefault()
    addRecent(query)
    setShowRecent(false)
    navigate(`/search?q=${encodeURIComponent(query)}`)
  }

  const goQuery = (q: string) => {
    setQuery(q)
    setShowRecent(false)
    navigate(`/search?q=${encodeURIComponent(q)}`)
  }

  const goProduct = (slug: string) => {
    setShowRecent(false)
    navigate(`/product/${encodeURIComponent(slug)}`)
  }

  const goCategory = (slug: string) => {
    setShowRecent(false)
    navigate(`/search?category=${encodeURIComponent(slug)}`)
  }

  const goBrand = (name: string) => {
    setQuery(name)
    setShowRecent(false)
    navigate(`/search?q=${encodeURIComponent(name)}`)
  }

  const toggleSaved = (q: string) => {
    const list = readList('vc_saved_search')
    if (list.includes(q)) writeList('vc_saved_search', list.filter((x) => x !== q))
    else writeList('vc_saved_search', [q, ...list])
  }

  const recent = readList('vc_recent_search')
  const saved = readList('vc_saved_search')

  return (
    <>
      <header className="sticky top-0 z-40 bg-amber-500 shadow-sm">
        <div className="mx-auto max-w-7xl px-4 h-16 flex items-center gap-4">
          <Link to="/" className="text-xl font-extrabold tracking-tight text-white shrink-0">
            VinCommerce
          </Link>
          {/* `min-w-0` is load-bearing: a flex item defaults to
              `min-width: auto`, so the input refused to shrink below its
              intrinsic size and the 375px viewport grew a horizontal
              scrollbar. Below `sm` the field collapses to an icon that
              navigates to /search. */}
          <form onSubmit={submitSearch} className="hidden sm:block flex-1 min-w-0 relative">
            <label htmlFor="header-search" className="sr-only">Cari produk, brand, kategori</label>
            <input
              id="header-search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onFocus={() => setShowRecent(true)}
              onBlur={() => setTimeout(() => setShowRecent(false), 150)}
              placeholder="Cari produk, brand, kategori..."
              className="w-full min-w-0 h-10 px-4 rounded-l-lg outline-none text-sm"
            />
            <button type="submit" className="h-10 px-5 bg-gray-900 text-white text-sm rounded-r-lg hover:bg-gray-800 shrink-0">
              Cari
            </button>

            {showRecent && (recent.length > 0 || saved.length > 0 || suggestions?.products.length || suggestions?.categories.length || suggestions?.brands.length) && (
              <div className="absolute left-0 top-11 w-full bg-white rounded-xl shadow-xl border border-gray-200 py-2 z-50 text-sm max-h-80 overflow-y-auto">
                {suggestions && debounced.length >= 2 && (
                  <>
                    {(suggestions.products.length > 0 ||
                      suggestions.categories.length > 0 ||
                      suggestions.brands.length > 0) && (
                      <p className="px-4 py-1 text-xs text-gray-400 uppercase">Saran</p>
                    )}
                    {suggestions.products.map((s) => (
                      <button 
                        key={`p${s.slug}`}
                        type="button"
                        onClick={() => goProduct(s.slug)}
                        className="w-full text-left px-4 py-1.5 hover:bg-gray-50 truncate"
                      >
                        📦 {s.name}
                      </button>
                    ))}
                    {suggestions.categories.map((s) => (
                      <button 
                        key={`c${s.slug}`}
                        type="button"
                        onClick={() => goCategory(s.slug)}
                        className="w-full text-left px-4 py-1.5 hover:bg-gray-50 truncate"
                      >
                        🗂 {s.name}
                      </button>
                    ))}
                    {suggestions.brands.map((s) => (
                      <button 
                        key={`b${s.slug}`}
                        type="button"
                        onClick={() => goBrand(s.name)}
                        className="w-full text-left px-4 py-1.5 hover:bg-gray-50 truncate"
                      >
                        🏷 {s.name}
                      </button>
                    ))}
                  </>
                )}
                {saved.length > 0 && (
                  <>
                    <p className="px-4 py-1 text-xs text-gray-400 uppercase">Tersimpan</p>
                    {saved.map((q) => (
                      <div key={`s${q}`} className="flex items-center justify-between px-4 py-1.5 hover:bg-gray-50">
                        <button type="button" onClick={() => goQuery(q)} className="flex-1 text-left truncate">
                          ⭐ {q}
                        </button>
                        <button type="button" aria-label={`Hapus ${q} dari tersimpan`} onClick={() => toggleSaved(q)} className="text-gray-300 hover:text-red-500 px-2">
                          ✕
                        </button>
                      </div>
                    ))}
                  </>
                )}
                {recent.length > 0 && (
                  <>
                    <p className="px-4 py-1 text-xs text-gray-400 uppercase mt-1">Riwayat</p>
                    {recent.map((q) => (
                      <div key={`r${q}`} className="flex items-center justify-between px-4 py-1.5 hover:bg-gray-50">
                        <button type="button" onClick={() => goQuery(q)} className="flex-1 text-left truncate">
                          🕐 {q}
                        </button>
                        <button type="button" aria-label={`Simpan ${q}`} onClick={() => toggleSaved(q)} className="text-gray-300 hover:text-amber-500 px-2" title="Simpan">
                          ☆
                        </button>
                      </div>
                    ))}
                  </>
                )}
              </div>
            )}
          </form>
          <nav className="flex items-center gap-2 text-sm text-white shrink-0">
            {/* Mobile stand-in for the inline search field, which is hidden
                below `sm` so the row can actually fit at 375px. */}
            <Link
              to="/search"
              className="px-2.5 py-2 rounded-lg hover:bg-amber-600 sm:hidden"
              title="Cari produk"
              aria-label="Cari produk"
            >
              🔍
            </Link>
            <Link to="/live" className="px-3 py-2 rounded-lg hover:bg-amber-600 hidden md:flex items-center gap-1">
              <span className="w-1.5 h-1.5 rounded-full bg-red-500 animate-pulse" /> Live
            </Link>
            <Link to="/discover" className="px-3 py-2 rounded-lg hover:bg-amber-600 hidden md:block">
              Temukan
            </Link>
            <Link to="/help" className="px-3 py-2 rounded-lg hover:bg-amber-600 hidden md:block">
              Bantuan
            </Link>
            <button
              type="button"
              onClick={toggleDark}
              className="px-2.5 py-2 rounded-lg hover:bg-amber-600"
              title={dark ? 'Mode terang' : 'Mode gelap'}
              aria-label={dark ? 'Aktifkan mode terang' : 'Aktifkan mode gelap'}
            >
              {dark ? '☀️' : '🌙'}
            </button>
            <button
              type="button"
              onClick={() => setCartOpen(true)}
              className="px-3 py-2 rounded-lg hover:bg-amber-600 relative"
              aria-haspopup="dialog"
            >
              Keranjang
              {cartCount > 0 && (
                <span className="absolute -top-1 -right-1 bg-red-600 text-white text-xs rounded-full min-w-5 h-5 flex items-center justify-center px-1">
                  {cartCount}
                </span>
              )}
            </button>

            {user ? (
              <div className="flex items-center gap-2">
                <NotificationBell />
                {user.roles.includes('seller') || user.roles.includes('admin') ? (
                  <Link to="/seller" className="px-3 py-2 rounded-lg hover:bg-amber-600">
                    Seller
                  </Link>
                ) : null}
                {user.roles.includes('admin') && (
                  <Link to="/admin" className="px-3 py-2 rounded-lg hover:bg-amber-600">
                    Admin
                  </Link>
                )}
                {user.roles.includes('support') && (
                  <Link to="/support" className="px-3 py-2 rounded-lg hover:bg-amber-600">
                    Support
                  </Link>
                )}
                <Link to="/account" className="px-3 py-2 rounded-lg hover:bg-amber-600 flex items-center gap-2 max-w-32 sm:max-w-none">
                  {user.avatar_url ? (
                    <img src={user.avatar_url} alt="" className="w-6 h-6 rounded-full object-cover bg-white/20" />
                  ) : null}
                  <span className="truncate">{user.full_name.split(' ')[0]}</span>
                </Link>
                <button
                  type="button"
                  onClick={async () => {
                    await logout()
                    navigate('/')
                  }}
                  className="px-3 py-2 rounded-lg hover:bg-amber-600"
                >
                  Keluar
                </button>

              </div>
            ) : (
              <div className="flex items-center gap-2">
                <Link to="/login" className="px-3 py-2 rounded-lg hover:bg-amber-600">
                  Masuk
                </Link>
                <Link to="/register" className="px-4 py-2 rounded-lg bg-gray-900 hover:bg-gray-800 font-medium">
                  Daftar
                </Link>
              </div>
            )}
          </nav>
        </div>
      </header>

      {cartOpen && (
        <CartDrawer
          lines={cart ?? []}
          onClose={() => setCartOpen(false)}
        />
      )}
    </>
  )
}

function CartDrawer({ lines, onClose }: { lines: CartLine[]; onClose: () => void }) {
  const total = lines.reduce((s, l) => s + l.subtotal, 0)
  return (
    <Modal
      open
      onClose={onClose}
      title={`Keranjang (${lines.length})`}
      variant="right"
      hideCloseButton
      panelClassName="!p-0"
    >
      <div className="h-full flex flex-col">
        <div className="flex flex-1 flex-col overflow-hidden">
          <div className="flex-1 overflow-y-auto p-4 space-y-3">
            {lines.length === 0 && <p className="text-sm text-gray-500 text-center mt-10">Keranjang kosong</p>}
            {lines.map((l) => (
              <div key={l.variant_id} className="flex gap-3">
                {l.image_url ? (
                  <img src={l.image_url} alt="" loading="lazy" className="w-16 h-16 rounded-lg object-cover bg-gray-100" />
                ) : (
                  <div className="w-16 h-16 rounded-lg bg-gray-100 flex items-center justify-center text-xs text-gray-400">
                    {l.product_name.slice(0, 8)}
                  </div>
                )}
                <div className="flex-1 text-sm min-w-0">
                  <p className="line-clamp-1">{l.product_name}</p>
                  <p className="text-gray-500 text-xs">
                    {l.variant_name} × {l.quantity}
                  </p>
                  <p className="text-amber-600 font-medium">{formatIDR(l.subtotal)}</p>
                </div>
              </div>
            ))}
          </div>
          <div className="p-4 border-t space-y-3">
            <div className="flex justify-between font-bold">
              <span>Total</span>
              <span className="text-amber-600">{formatIDR(total)}</span>
            </div>
            <Link
              to="/checkout"
              onClick={onClose}
              className="block text-center py-3 rounded-lg bg-amber-500 text-white font-medium hover:bg-amber-600"
            >
              Checkout
            </Link>
            <Link
              to="/cart"
              onClick={onClose}
              className="block text-center py-3 rounded-lg border border-gray-300 hover:bg-gray-50"
            >
              Lihat Keranjang
            </Link>
          </div>
        </div>
      </div>
    </Modal>
  )
}


