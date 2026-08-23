import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, NavLink, Outlet } from 'react-router-dom'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import { formatIDR } from '../../lib/format'

export function SellerLayout() {
  const { user } = useSession()
  const [showOnboard, setShowOnboard] = useState(false)

  const { data: store } = useQuery({
    queryKey: ['my-store'],
    queryFn: async () => {
      try {
        return (await api.get<{ store: { id: string; name: string; status: string } }>('/seller/store')).data.store
      } catch {
        return null
      }
    },
    enabled: !!user,
  })

  if (!store) {
    return (
      <div className="mx-auto max-w-xl px-4 py-20 text-center">
        <h1 className="text-2xl font-bold mb-2">Mulai Jualan di VinCommerce</h1>
        <p className="text-gray-500 text-sm mb-8">
          Buka toko gratis, jangkau jutaan pembeli, terima pembayaran escrow yang aman.
        </p>
        <button
          onClick={async () => {
            const name = prompt('Nama toko:')
            if (!name) return
            setShowOnboard(true)
            try {
              await api.post('/seller/store', { name })
              window.location.reload()
            } finally {
              setShowOnboard(false)
            }
          }}
          disabled={showOnboard}
          className="px-8 py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
        >
          {showOnboard ? 'Membuat...' : 'Buka Toko Sekarang'}
        </button>
      </div>
    )
  }

  const nav = [
    { to: '/seller', label: 'Dashboard', end: true },
    { to: '/seller/products', label: 'Produk' },
    { to: '/seller/orders', label: 'Pesanan' },
    { to: '/seller/returns', label: 'Retur' },
    { to: '/seller/wallet', label: 'Dompet' },
    { to: '/seller/analytics', label: 'Analitik' },
    { to: '/seller/coupons', label: 'Kupon Toko' },
{ to: '/seller/live', label: '📺 Live Studio' },
    { to: '/seller/settings', label: 'Pengaturan' },
  ]

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 md:grid-cols-6 gap-6">
      <aside className="md:col-span-1 space-y-1">
        <div className="bg-white border border-gray-200 rounded-xl p-4 mb-3">
          <p className="font-bold text-sm">{store.name}</p>
          <p className="text-xs text-gray-500">{store.status}</p>
        </div>
        {nav.map((n) => (
          <NavLink
            key={n.to}
            to={n.to}
            end={n.end}
            className={({ isActive }) =>
              `block px-4 py-2.5 rounded-lg text-sm ${
                isActive ? 'bg-amber-500 text-white font-medium' : 'hover:bg-gray-100 text-gray-700'
              }`
            }
          >
            {n.label}
          </NavLink>
        ))}
        <Link to="/" className="block px-4 py-2.5 rounded-lg text-sm text-gray-400 hover:bg-gray-100">
          â† Kembali ke toko
        </Link>
      </aside>
      <main className="md:col-span-5">
        <Outlet />
      </main>
    </div>
  )
}

export function SellerHome() {
  const { data } = useQuery({
    queryKey: ['seller-dashboard'],
    queryFn: async () =>
      (
        await api.get<{
          total_sales: number
          order_count: number
          pending_orders: number
          products_active: number
          products_draft: number
          wallet_balance: number
          avg_order_value: number
        }>('/seller/dashboard')
      ).data,
  })

  const { data: lowStock } = useQuery({
    queryKey: ['seller-low-stock'],
    queryFn: async () =>
      (
        await api.get<{
          variants: { variant_id: string; product_name: string; variant_name: string; stock: number }[]
        }>('/seller/low-stock')
      ).data,
  })

  const cards = [
    { label: 'Total Penjualan', value: formatIDR(data?.total_sales ?? 0), color: 'text-amber-600' },
    { label: 'Pesanan', value: String(data?.order_count ?? 0), color: 'text-gray-900' },
    { label: 'Menunggu Proses', value: String(data?.pending_orders ?? 0), color: 'text-blue-600' },
    { label: 'Produk Aktif', value: String(data?.products_active ?? 0), color: 'text-green-600' },
    { label: 'Saldo Dompet', value: formatIDR(data?.wallet_balance ?? 0), color: 'text-amber-600' },
    { label: 'Rata-rata Pesanan', value: formatIDR(data?.avg_order_value ?? 0), color: 'text-gray-900' },
  ]

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Ringkasan Toko</h1>
      <div className="grid grid-cols-2 lg:grid-cols-3 gap-4">
        {cards.map((c) => (
          <div key={c.label} className="bg-white border border-gray-200 rounded-xl p-5">
            <p className="text-xs text-gray-500">{c.label}</p>
            <p className={`text-xl font-bold mt-1 ${c.color}`}>{c.value}</p>
          </div>
        ))}
      </div>

      {lowStock && lowStock.variants.length > 0 && (
        <div className="bg-red-50 dark:bg-red-950/40 border border-red-200 dark:border-red-800 rounded-xl p-5">
          <h2 className="font-bold text-sm text-red-700 dark:text-red-300 mb-3">⚠️ Stok Menipis ({lowStock.variants.length})</h2>
          <div className="space-y-2">
            {lowStock.variants.map((v) => (
              <div key={v.variant_id} className="flex items-center justify-between text-sm">
                <p className="text-red-800 dark:text-red-200 line-clamp-1">
                  {v.product_name} <span className="text-red-400">({v.variant_name})</span>
                </p>
                <p className="font-bold text-red-600">sisa {v.stock}</p>
              </div>
            ))}
          </div>
          <Link
            to="/seller/products"
            className="inline-block mt-3 text-xs text-red-600 dark:text-red-300 underline"
          >
            Kelola stok →
          </Link>
        </div>
      )}
    </div>
  )
}

