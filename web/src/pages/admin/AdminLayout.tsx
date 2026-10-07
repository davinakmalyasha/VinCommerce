import { NavLink, Outlet } from 'react-router-dom'
import { useSession } from '../../stores/session'

export function AdminLayout() {
  const { user } = useSession()

  if (!user?.roles.includes('admin')) {
    return (
      <div className="mx-auto max-w-md px-4 py-20 text-center">
        <h1 className="text-xl font-bold mb-2">Akses Ditolak</h1>
        <p className="text-gray-500 text-sm">Halaman ini khusus administrator.</p>
      </div>
    )
  }

  const nav = [
    { to: '/admin', label: 'Ringkasan', end: true },
    { to: '/admin/stores', label: 'Persetujuan Toko' },
    { to: '/admin/orders', label: 'Pesanan' },
    { to: '/admin/tickets', label: 'Tiket Dukungan' },
    { to: '/admin/reviews', label: 'Moderasi Ulasan' },
    { to: '/admin/reports', label: 'Moderasi Produk' },
    { to: '/admin/returns', label: 'Antrian Retur' },
    { to: '/admin/disputes', label: 'Sengketa' },
    { to: '/admin/coupons', label: 'Kupon' },
    { to: '/admin/flash-sales', label: 'Flash Sale' },
    { to: '/admin/shipping', label: 'Kurir' },
    { to: '/admin/catalog', label: 'Katalog' },
    { to: '/admin/users', label: 'Pengguna' },
    { to: '/admin/articles', label: 'Artikel' },
    { to: '/admin/commission', label: 'Komisi' },
    { to: '/admin/payouts', label: 'Penarikan Dana' },
    { to: '/admin/payout-batches', label: 'Batch Pencairan' },
    { to: '/admin/audit', label: 'Audit Log' },
    { to: '/admin/flags', label: 'Feature Flags' },
    { to: '/admin/analytics', label: 'Analitik' },
  ]

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 md:grid-cols-6 gap-6">
      <aside className="md:col-span-1 space-y-1">
        <div className="bg-white border border-gray-200 rounded-xl p-4 mb-3">
          <p className="font-bold text-sm">Konsol Admin</p>
          <p className="text-xs text-gray-500">VinCommerce Platform</p>
        </div>
        {nav.map((n) => (
          <NavLink
            key={n.to}
            to={n.to}
            end={n.end}
            className={({ isActive }) =>
              `block px-4 py-2.5 rounded-lg text-sm ${
                isActive ? 'bg-gray-900 text-white font-medium' : 'hover:bg-gray-100 text-gray-700'
              }`
            }
          >
            {n.label}
          </NavLink>
        ))}
        <NavLink to="/" className="block px-4 py-2.5 rounded-lg text-sm text-gray-400 hover:bg-gray-100">
          ← Toko
        </NavLink>
      </aside>
      <main className="md:col-span-5">
        <Outlet />
      </main>
    </div>
  )
}
