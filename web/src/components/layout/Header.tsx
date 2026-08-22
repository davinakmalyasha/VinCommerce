import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import type { CartLine } from '../../types'
import { formatIDR } from '../../lib/format'
import { NotificationBell } from './NotificationBell'
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
          <form onSubmit={submitSearch} className="flex-1 flex relative">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onFocus={() => setShowRecent(true)}
              onBlur={() => setTimeout(() => setShowRecent(false), 150)}
              placeholder="Cari produk, brand, kategori..."
              className="flex-1 h-10 px-4 rounded-l-lg outline-none text-sm"
            />
            <button className="h-10 px-5 bg-gray-900 text-white text-sm rounded-r-lg hover:bg-gray-800">
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
                        onClick={() => goProduct(s.slug)}
                        className="w-full text-left px-4 py-1.5 hover:bg-gray-50 truncate"
                      >
                        📦 {s.name}
                      </button>
                    ))}
                    {suggestions.categories.map((s) => (
                      <button
                        key={`c${s.slug}`}
                        onClick={() => goCategory(s.slug)}
                        className="w-full text-left px-4 py-1.5 hover:bg-gray-50 truncate"
                      >
                        🗂 {s.name}
                      </button>
                    ))}
                    {suggestions.brands.map((s) => (
                      <button
                        key={`b${s.slug}`}
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
                        <button onClick={() => goQuery(q)} className="flex-1 text-left truncate">
                          ⭐ {q}
                        </button>
                        <button onClick={() => toggleSaved(q)} className="text-gray-300 hover:text-red-500 px-2">
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
                        <button onClick={() => goQuery(q)} className="flex-1 text-left truncate">
                          🕐 {q}
                        </button>
                        <button onClick={() => toggleSaved(q)} className="text-gray-300 hover:text-amber-500 px-2" title="Simpan">
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
            <Link to="/help" className="px-3 py-2 rounded-lg hover:bg-amber-600 hidden md:block">
              Bantuan
            </Link>
            <button
              onClick={toggleDark}
              className="px-2.5 py-2 rounded-lg hover:bg-amber-600"
              title={dark ? 'Mode terang' : 'Mode gelap'}
            >
              {dark ? '☀️' : '🌙'}
            </button>
            <button
              onClick={() => setCartOpen(true)}
              className="px-3 py-2 rounded-lg hover:bg-amber-600 relative"
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
                {user.roles.includes('seller') && (
                  <Link to="/seller" className="px-3 py-2 rounded-lg hover:bg-amber-600">
                    Seller
                  </Link>
                )}
                {user.roles.includes('admin') && (
                  <Link to="/admin" className="px-3 py-2 rounded-lg hover:bg-amber-600">
                    Admin
                  </Link>
                )}
                <Link to="/account" className="px-3 py-2 rounded-lg hover:bg-amber-600 flex items-center gap-2">
                  {user.avatar_url ? (
                    <img src={user.avatar_url} alt="" className="w-6 h-6 rounded-full object-cover bg-white/20" />
                  ) : null}
                  {user.full_name.split(' ')[0]}
                </Link>
                <button
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
    <div className="fixed inset-0 z-50">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="absolute right-0 top-0 h-full w-96 bg-white shadow-xl flex flex-col">
        <div className="p-4 border-b flex justify-between items-center">
          <h2 className="font-bold">Keranjang ({lines.length})</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-700">
            âœ•
          </button>
        </div>
        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {lines.length === 0 && <p className="text-sm text-gray-500 text-center mt-10">Keranjang kosong</p>}
          {lines.map((l) => (
            <div key={l.variant_id} className="flex gap-3">
              {l.image_url ? (
                <img src={l.image_url} alt="" className="w-16 h-16 rounded-lg object-cover bg-gray-100" />
              ) : (
                <div className="w-16 h-16 rounded-lg bg-gray-100 flex items-center justify-center text-xs text-gray-400">
                  {l.product_name.slice(0, 8)}
                </div>
              )}
              <div className="flex-1 text-sm">
                <p className="line-clamp-1">{l.product_name}</p>
                <p className="text-gray-500 text-xs">
                  {l.variant_name} Ã— {l.quantity}
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
  )
}

