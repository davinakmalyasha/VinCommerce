import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { Product, Store } from '../types'
import { ProductCard, CompareBar } from '../components/ProductCard'
import { Seo } from '../components/Seo'
import { QueryState } from '../components/QueryState'
import { useSession } from '../stores/session'

export function StorePage() {
  const { slug } = useParams()
  const { user } = useSession()
  const queryClient = useQueryClient()

  const storeQuery = useQuery({
    queryKey: ['store', slug],
    queryFn: async () => (await api.get<{ store: Store; products: Product[] }>(`/stores/${slug}`)).data,
  })
  const data = storeQuery.data

  const follow = useMutation({
    mutationFn: async () => {
      if (data!.store.is_following) {
        await api.delete(`/stores/${data!.store.id}/follow`)
      } else {
        await api.post(`/stores/${data!.store.id}/follow`)
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['store', slug] }),
    onError: (e: Error) => setError(e.message),
  })
  const [error, setError] = useState('')

  if (!data) {
    return <QueryState query={storeQuery} label="toko" />
  }

  const store = data.store

  return (
    <>
      <Seo
        title={`${store.name} — Toko di VinCommerce`}
        description={store.description?.slice(0, 160)}
        path={`/store/${store.slug}`}
      />
      <div className="mx-auto max-w-7xl px-4 py-6 space-y-8">
      <div
        className="bg-gradient-to-r from-gray-900 to-gray-700 text-white rounded-2xl p-8 flex items-center gap-6 bg-cover bg-center"
        style={store.banner_url ? { backgroundImage: `linear-gradient(rgba(17,24,39,.72), rgba(17,24,39,.72)), url(${store.banner_url})` } : undefined}
      >
        {store.logo_url ? (
          <img src={store.logo_url} alt="" className="w-20 h-20 rounded-2xl object-cover" />
        ) : (
          <div className="w-20 h-20 rounded-2xl bg-amber-500 flex items-center justify-center text-2xl font-extrabold">
            {store.name.slice(0, 1)}
          </div>
        )}
        <div className="flex-1">
          <h1 className="text-2xl font-extrabold flex items-center gap-2 flex-wrap">
            <span className="flex items-center gap-2">
              {store.online && (
                <span className="w-2.5 h-2.5 rounded-full bg-green-400 animate-pulse" title="Penjual sedang online" />
              )}
              {store.name}
            </span>
            {store.is_verified && (
              <span
                className="inline-flex items-center gap-1 px-2 py-0.5 rounded-lg bg-amber-400 text-gray-900 text-xs font-bold"
                title="Toko resmi terverifikasi"
              >
                ✓ Toko Resmi
              </span>
            )}
            {store.is_power_seller && (
              <span className="px-2 py-0.5 rounded-lg bg-purple-500 text-white text-xs font-bold" title="Skor respons & rating tinggi">
                ⭐ Power Seller
              </span>
            )}
          </h1>
          <p className="text-sm text-gray-300 mt-1 line-clamp-2">{store.description || 'Toko resmi di VinCommerce.'}</p>
          {(store.response_rate_pct != null || store.online) && (
            <p className="text-xs text-gray-300 mt-1.5">
              {store.online ? '🟢 Online' : ''}
              {store.response_rate_pct != null && (
                <> · Membalas ±{store.avg_reply_minutes ?? 60} mnt · {store.response_rate_pct}% pesan dibalas</>
              )}
            </p>
          )}
          <div className="flex gap-6 mt-3 text-sm text-gray-300">
            <span>★ {store.rating} ({store.rating_count})</span>
            <span>{data.products.length} produk</span>
            <span>{store.follower_count ?? 0} pengikut</span>
            <span>Bergabung {new Date(store.joined_at).toLocaleDateString('id-ID')}</span>
          </div>
        </div>
        {user && user.id !== store.owner_id && (
          <button
            type="button"
            onClick={() => follow.mutate()}
            disabled={follow.isPending}
            className={`px-5 py-2.5 rounded-xl font-medium text-sm ${
              store.is_following
                ? 'bg-white/10 border border-white/40 text-white hover:bg-white/20'
                : 'bg-amber-500 text-white hover:bg-amber-600'
            }`}
          >
            {store.is_following ? '✓ Mengikuti' : '+ Ikuti Toko'}
          </button>
        )}
      </div>

      {error && (
        <p role="alert" className="text-sm text-red-600 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">{error}</p>
      )}

      <section>
        <h2 className="font-bold mb-4">Produk Toko</h2>
        {data.products.length === 0 ? (
          <p className="text-gray-500 text-sm">Toko ini belum memiliki produk aktif.</p>
        ) : (
          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
            {data.products.map((p) => (
              <ProductCard key={p.id} product={p} />
            ))}
          </div>
        )}
      </section>

      <Link to="/search" className="text-sm text-amber-600 hover:underline">
        ← Kembali ke katalog
      </Link>
    </div>
    <CompareBar />
    </>
  )
}
