import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Product, Store } from '../types'
import { ProductCard } from '../components/ProductCard'

export function FollowedStoresPage() {
  const { data } = useQuery({
    queryKey: ['followed-stores'],
    queryFn: async () => (await api.get<{ stores: Store[] }>('/followed-stores')).data.stores,
  })

  const { data: feed } = useQuery({
    queryKey: ['followed-feed'],
    queryFn: async () => (await api.get<{ products: Product[] }>('/followed-stores/feed?limit=12')).data.products,
  })

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 space-y-8">
      <h1 className="text-xl font-bold">Toko yang Saya Ikuti</h1>

      {feed && feed.length > 0 && (
        <section>
          <h2 className="font-bold mb-4">🆕 Produk Terbaru dari Toko Favorit</h2>
          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-4">
            {feed.map((p) => (
              <ProductCard key={p.id} product={p} />
            ))}
          </div>
        </section>
      )}

      {!data?.length ? (
        <div className="text-center py-16">
          <p className="text-gray-500 mb-4">Belum ada toko yang diikuti.</p>
          <Link to="/search" className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
            Jelajahi Toko
          </Link>
        </div>
      ) : (
        <section>
          <h2 className="font-bold mb-4">Semua Toko Diikuti</h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {data.map((s) => (
              <Link
                key={s.id}
                to={`/store/${s.slug}`}
                className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 hover:shadow-md transition-shadow"
              >
                <div className="flex items-center gap-4">
                  {s.logo_url ? (
                    <img src={s.logo_url} alt="" className="w-14 h-14 rounded-xl object-cover" />
                  ) : (
                    <div className="w-14 h-14 rounded-xl bg-amber-500 flex items-center justify-center text-xl font-extrabold text-white">
                      {s.name.slice(0, 1)}
                    </div>
                  )}
                  <div className="min-w-0">
                    <p className="font-semibold truncate">{s.name}</p>
                    <p className="text-xs text-gray-500">
                      ★ {s.rating} ({s.rating_count}) · {s.products_count} produk · {s.follower_count ?? 0} pengikut
                    </p>
                  </div>
                </div>
              </Link>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}
