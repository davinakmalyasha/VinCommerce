import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import { formatIDR } from '../lib/format'
import { QueryState } from '../components/QueryState'
import { Seo } from '../components/Seo'

interface ViewedProduct {
  product_id: string
  slug: string
  name: string
  status: string
  image_url?: string
  price: number
  viewed_at: string
}

interface LocalRecent {
  id: string
  name: string
  slug: string
  viewed_at: number
}

function loadLocalRecent(): LocalRecent[] {
  try {
    return JSON.parse(localStorage.getItem('vc_recent') ?? '[]')
  } catch {
    return []
  }
}

export function HistoryPage() {
  const { user } = useSession()

  const historyQuery = useQuery({
    queryKey: ['browsing-history'],
    queryFn: async () => (await api.get<{ products: ViewedProduct[] }>('/history?limit=30')).data.products,
    enabled: !!user,
  })
  const serverItems = historyQuery.data
  const localItems = !user ? loadLocalRecent() : []

  return (
    <>
      <Seo title="Terakhir Dilihat — VinCommerce" path="/history" />
      <div className="mx-auto max-w-7xl px-4 py-6">
        <h1 className="text-xl font-bold mb-1">Terakhir Dilihat</h1>
        <p className="text-sm text-gray-500 mb-6">
          {user ? 'Produk yang kamu buka di perangkat mana pun.' : 'Masuk untuk melihat riwayat dari semua perangkat.'}
        </p>
        {user ? (
          <QueryState query={historyQuery} label="riwayat" className="!text-left">
            <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-3">
              {serverItems?.length === 0 && (
                <p className="text-sm text-gray-500 col-span-full">Belum ada produk dilihat.</p>
              )}
              {serverItems?.map((p) => (
                <Link
                  key={p.product_id}
                  to={`/product/${p.slug}`}
                  className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden hover:shadow-md transition-shadow"
                >
                  <div className="aspect-square bg-gray-100 dark:bg-gray-800">
                    {p.image_url ? (
                      <img src={p.image_url} alt={p.name} className="w-full h-full object-cover" />
                    ) : (
                      <div className="w-full h-full flex items-center justify-center text-3xl">📦</div>
                    )}
                  </div>
                  <div className="p-3">
                    <p className="text-sm line-clamp-1">{p.name}</p>
                    <p className="text-sm font-bold text-amber-600 mt-0.5">{formatIDR(p.price)}</p>
                    {p.status !== 'active' && (
                      <p className="text-xs text-gray-400 mt-0.5">Tidak aktif</p>
                    )}
                  </div>
                </Link>
              ))}
            </div>
          </QueryState>
        ) : (
          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-3">
            {localItems.length === 0 && (
              <p className="text-sm text-gray-500 col-span-full">
                Belum ada produk dilihat di perangkat ini.{' '}
                <Link to="/login" className="text-amber-600 hover:underline">Masuk</Link> untuk sinkronisasi.
              </p>
            )}
            {localItems.map((p) => (
              <Link
                key={p.id}
                to={`/product/${p.slug}`}
                className="bg-white border border-gray-200 rounded-xl p-3 hover:shadow-md transition-shadow"
              >
                <p className="text-sm line-clamp-1">{p.name}</p>
              </Link>
            ))}
          </div>
        )}
      </div>
    </>
  )
}
