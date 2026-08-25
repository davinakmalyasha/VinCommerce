import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Category, Product } from '../types'
import { ProductCard } from '../components/ProductCard'
import { BundlesStrip } from '../components/BundlesStrip'
import { useActiveFlashSale } from '../lib/flashSale'

interface RecentlyViewed extends Product {
  viewed_at: number
}

function loadRecentlyViewed(): RecentlyViewed[] {
  try {
    return JSON.parse(localStorage.getItem('vc_recent') ?? '[]')
  } catch {
    return []
  }
}

export function HomePage() {
  const { data: categories } = useQuery({
    queryKey: ['categories'],
    queryFn: async () => (await api.get<{ categories: Category[] }>('/catalog/categories')).data.categories,
  })

  const { data: recommended } = useQuery({
    queryKey: ['recommended'],
    queryFn: async () => (await api.get<{ products: Product[] }>('/recommendations?limit=10')).data.products,
  })

  const { data: flashSale } = useActiveFlashSale()

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 space-y-10">
      <section className="rounded-2xl bg-gradient-to-r from-amber-500 to-orange-600 text-white p-10 md:p-14 relative overflow-hidden">
        <h1 className="text-3xl md:text-5xl font-extrabold mb-3">Semua yang kamu butuhkan.</h1>
        <p className="text-amber-100 max-w-xl">
          Ribuan produk dari penjual terverifikasi, dilindungi pembayaran escrow yang aman.
        </p>
        <Link
          to="/search"
          className="inline-block mt-6 px-6 py-3 rounded-xl bg-white text-amber-600 font-semibold hover:bg-amber-50"
        >
          Mulai Belanja
        </Link>
      </section>

      {categories && (
        <section>
          <h2 className="text-lg font-bold mb-4">Kategori</h2>
          <div className="grid grid-cols-3 md:grid-cols-5 gap-3">
            {categories.map((c) => (
              <Link
                key={c.id}
                to={`/search?category=${c.slug}`}
                className="bg-white border border-gray-200 rounded-xl p-4 text-center hover:border-amber-400 hover:shadow-md transition-all"
              >
                <p className="font-medium text-sm">{c.name}</p>
                <p className="text-xs text-gray-400 mt-1">{c.children?.length ?? 0} sub-kategori</p>
              </Link>
            ))}
          </div>
        </section>
      )}

      {flashSale && flashSale.items.length > 0 && (
        <section className="rounded-2xl border-2 border-red-200 bg-red-50 p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-bold text-red-600">⚡ {flashSale.flash_sale.name}</h2>
            <Link to="/flash-sales" className="text-sm text-red-500 hover:underline">
              Lihat semua
            </Link>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
            {flashSale.items.slice(0, 5).map((it) => (
              <Link
                key={it.variant_id}
                to={`/product/${it.product_slug}`}
                className="bg-white rounded-xl border border-red-100 p-3 hover:shadow-md transition-all"
              >
                {it.image_url && (
                  <img src={it.image_url} alt="" className="w-full aspect-square object-cover rounded-lg bg-gray-100" />
                )}
                <p className="text-sm mt-2 line-clamp-2">{it.product_name}</p>
                <p className="text-red-600 font-bold text-sm">
                  Rp{Math.round(it.sale_price).toLocaleString('id-ID')}
                </p>
                <p className="text-xs text-gray-400 line-through">Rp{Math.round(it.regular_price).toLocaleString('id-ID')}</p>
              </Link>
            ))}
          </div>
        </section>
      )}

      <section>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-bold">Rekomendasi Untukmu</h2>
          <Link to="/search?sort=bestseller" className="text-sm text-amber-600 hover:underline">
            Lihat semua
          </Link>
        </div>
        <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
          {recommended?.map((p) => <ProductCard key={p.id} product={p} />)}
        </div>
      </section>

      <BundlesStrip />

      <RecentlyViewedStrip />
    </div>
  )
}

function RecentlyViewedStrip() {
  const recent = loadRecentlyViewed().slice(0, 10)
  if (recent.length === 0) return null

  return (
    <section>
      <h2 className="text-lg font-bold mb-4">Baru Dilihat</h2>
      <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
        {recent.map((p) => (
          <ProductCard key={p.id} product={p} />
        ))}
      </div>
    </section>
  )
}
