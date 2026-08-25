import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { formatIDR } from '../lib/format'
import { useSession } from '../stores/session'

interface BundleItem {
  variant_id: string
  quantity: number
  product_name?: string
  variant_name?: string
  image_url?: string
  unit_price?: number
  product_slug?: string
}

interface Bundle {
  id: string
  seller_id: string
  name: string
  description?: string
  price: number
  items: BundleItem[]
}

// BundlesStrip renders active seller bundles on the storefront with a
// one-click "add everything" action.
export function BundlesStrip({ limit = 6 }: { limit?: number }) {
  const { user } = useSession()
  const queryClient = useQueryClient()
  const [addedId, setAddedId] = useState<string | null>(null)

  const { data: bundles } = useQuery({
    queryKey: ['bundles'],
    queryFn: async () => (await api.get<{ bundles: Bundle[] }>('/bundles')).data.bundles,
  })

  const addAll = useMutation({
    mutationFn: async (b: Bundle) => {
      for (const item of b.items) {
        await api.post('/cart/items', {
          variant_id: item.variant_id,
          quantity: Math.max(1, item.quantity),
        })
      }
      return b.id
    },
    onSuccess: (id) => {
      setAddedId(id)
      queryClient.invalidateQueries({ queryKey: ['cart'] })
      setTimeout(() => setAddedId(null), 2500)
    },
  })

  if (!bundles?.length) return null

  return (
    <section className="mx-auto max-w-7xl px-4 py-8">
      <h2 className="text-xl font-extrabold mb-1">📦 Bundling Hemat</h2>
      <p className="text-sm text-gray-500 mb-4">Paket produk dari penjual dengan harga spesial.</p>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        {bundles.slice(0, limit).map((b) => {
          const itemsTotal = b.items.reduce((s, it) => s + (it.unit_price ?? 0) * Math.max(1, it.quantity), 0)
          const save = itemsTotal > b.price ? Math.round((1 - b.price / itemsTotal) * 100) : 0
          return (
            <div key={b.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-5 flex flex-col">
              <p className="font-bold">{b.name}</p>
              {b.description && <p className="text-xs text-gray-500 mt-0.5 line-clamp-2">{b.description}</p>}
              <div className="flex gap-2 my-3">
                {b.items.slice(0, 4).map((it) =>
                  it.image_url ? (
                    <img
                      key={it.variant_id}
                      src={it.image_url}
                      alt={it.product_name ?? ''}
                      loading="lazy"
                      className="w-14 h-14 rounded-lg object-cover border border-gray-100 dark:border-gray-800"
                    />
                  ) : (
                    <div key={it.variant_id} className="w-14 h-14 rounded-lg bg-gray-100 dark:bg-gray-800" />
                  ),
                )}
                {b.items.length > 4 && (
                  <div className="w-14 h-14 rounded-lg bg-gray-100 dark:bg-gray-800 flex items-center justify-center text-xs text-gray-400">
                    +{b.items.length - 4}
                  </div>
                )}
              </div>
              <ul className="text-xs text-gray-500 space-y-0.5 mb-3 flex-1">
                {b.items.map((it) => (
                  <li key={it.variant_id} className="line-clamp-1">
                    {it.quantity}×{' '}
                    {it.product_slug ? (
                      <Link to={`/product/${it.product_slug}`} className="hover:text-amber-600">
                        {it.product_name}
                      </Link>
                    ) : (
                      it.product_name ?? 'Produk'
                    )}
                  </li>
                ))}
              </ul>
              <div className="flex items-baseline gap-2">
                <span className="font-extrabold text-amber-600">{formatIDR(b.price)}</span>
                {save > 0 && (
                  <>
                    <span className="text-xs text-gray-400 line-through">{formatIDR(itemsTotal)}</span>
                    <span className="px-1.5 py-0.5 rounded bg-red-600 text-white text-[10px] font-bold">-{save}%</span>
                  </>
                )}
              </div>
              <button
                type="button"
                onClick={() => addAll.mutate(b)}
                disabled={addAll.isPending || !user}
                title={!user ? 'Masuk untuk membeli' : undefined}
                className="mt-3 w-full py-2.5 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
              >
                {addedId === b.id ? '✓ Masuk keranjang!' : addAll.isPending ? 'Menambahkan...' : 'Ambil Paket'}
              </button>
            </div>
          )
        })}
      </div>
    </section>
  )
}
