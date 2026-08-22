import { Link } from 'react-router-dom'
import { Seo } from '../components/Seo'

export function NotFoundPage() {
  return (
    <div className="mx-auto max-w-md px-4 py-24 text-center">
      <Seo title="Halaman tidak ditemukan — VinCommerce" />
      <p className="text-7xl font-extrabold text-amber-500">404</p>
      <h1 className="text-xl font-bold mt-4">Halaman tidak ditemukan</h1>
      <p className="text-gray-500 text-sm mt-2">Halaman yang kamu cari tidak ada atau sudah dipindahkan.</p>
      <Link
        to="/"
        className="inline-block mt-6 px-6 py-3 rounded-xl bg-amber-500 text-white font-medium hover:bg-amber-600"
      >
        Kembali ke Beranda
      </Link>
    </div>
  )
}
