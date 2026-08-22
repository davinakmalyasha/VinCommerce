import { Link } from 'react-router-dom'

export function Footer() {
  return (
    <footer className="border-t border-gray-200 bg-white mt-12">
      <div className="mx-auto max-w-7xl px-4 py-10 grid grid-cols-2 md:grid-cols-4 gap-8 text-sm text-gray-600">
        <div>
          <p className="font-bold text-amber-600 mb-3 text-lg">VinCommerce</p>
          <p className="text-xs leading-relaxed">
            Platform marketplace enterprise-grade dengan escrow aman untuk pembeli dan penjual.
          </p>
        </div>
        <div>
          <p className="font-semibold text-gray-900 mb-3">Belanja</p>
          <ul className="space-y-2 text-xs">
            <li><Link to="/search" className="hover:text-amber-600">Semua Produk</Link></li>
            <li><Link to="/flash-sales" className="hover:text-amber-600">Flash Sale</Link></li>
            <li><Link to="/search?sort=bestseller" className="hover:text-amber-600">Terlaris</Link></li>
          </ul>
        </div>
        <div>
          <p className="font-semibold text-gray-900 mb-3">Jualan</p>
          <ul className="space-y-2 text-xs">
            <li><Link to="/seller" className="hover:text-amber-600">Seller Center</Link></li>
            <li><Link to="/register" className="hover:text-amber-600">Buka Toko</Link></li>
          </ul>
        </div>
        <div>
          <p className="font-semibold text-gray-900 mb-3">Akun</p>
          <ul className="space-y-2 text-xs">
            <li><Link to="/orders" className="hover:text-amber-600">Pesanan Saya</Link></li>
            <li><Link to="/wishlist" className="hover:text-amber-600">Wishlist</Link></li>
            <li><Link to="/followed-stores" className="hover:text-amber-600">Toko Diikuti</Link></li>
            <li><Link to="/account" className="hover:text-amber-600">Profil</Link></li>
          </ul>
        </div>
      </div>
    </footer>
  )
}
