import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { Product } from '../../types'
import { formatIDR } from '../../lib/format'
import { useSession } from '../../stores/session'

interface BundleItem {
  variant_id: string
  quantity: number
  product_name?: string
  variant_name?: string
  unit_price?: number
}

interface Bundle {
  id: string
  name: string
  description?: string
  price: number
  is_active: boolean
  items: BundleItem[]
}

// SellerBundles lets a seller compose fixed-price product packages.
export function SellerBundles() {
  const { user } = useSession()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [price, setPrice] = useState('')
  const [picked, setPicked] = useState<Record<string, { price: number }>>({})

  const { data } = useQuery({
    // NOT ['seller-products', 1].
    //
    // SellerProducts.tsx uses ['seller-products', page] for the paginated product list,
    // and page 1 is 1. This is a DIFFERENT request with a different page_size (50 rather
    // than the list page size) and, on a seller with a short product list, a different
    // shape -- no `total`, no page envelope.
    //
    // A TanStack Query key must identify a request uniquely, not a topic. Sharing this
    // key meant whichever of the two components mounted first populated the cache and
    // the other was served the wrong response: a seller who opened Products then
    // navigated to Bundles got the paginated page in the variant picker, and a seller
    // who opened Bundles first got a 50-item response with no pagination metadata in
    // their product list. Neither is a crash, so it surfaces as quietly wrong data.
    queryKey: ['seller-products', 'bundle-picker', 1],
    queryFn: async () =>
      (await api.get<{ products: Product[] }>('/seller/products?page_size=50')).data,
  })

  const { data: mine } = useQuery({
    queryKey: ['seller-bundles'],
    queryFn: async () =>
      (await api.get<{ bundles: Bundle[] }>(`/bundles?seller=${user?.id ?? ''}`)).data.bundles,
    enabled: !!user,
  })

  const variants = (data?.products ?? []).flatMap((p) =>
    (p.variants ?? []).map((v) => ({
      id: v.id,
      label: `${p.name} — ${v.name} (${formatIDR(v.price)})`,
      price: v.price,
    })),
  )

  const itemsTotal = Object.entries(picked).reduce((sum, [, v]) => sum + v.price, 0)

  const create = useMutation({
    mutationFn: async () => {
      return api.post('/seller/bundles', {
        name,
        description,
        price: Number(price),
        items: Object.keys(picked).map((variant_id) => ({ variant_id, quantity: 1 })),
      })
    },
    onSuccess: () => {
      setName('')
      setDescription('')
      setPrice('')
      setPicked({})
      queryClient.invalidateQueries({ queryKey: ['seller-bundles'] })
    },
    onError: (e: Error) => setError(e.message || 'Gagal membuat bundling'),
  })
  const [error, setError] = useState('')

  const toggleVariant = (id: string, price: number) => {
    setPicked((prev) => {
      const next = { ...prev }
      if (id in next) delete next[id]
      else next[id] = { price }
      return next
    })
  }

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-4">
      <div>
        <h2 className="font-bold text-sm">🎁 Bundling Produk</h2>
        <p className="text-xs text-gray-500">Jual beberapa produk sebagai satu paket dengan harga spesial.</p>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-red-50 dark:bg-red-950/40 p-2.5 text-xs text-red-700">{error}</p>
      )}


      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Nama paket"
          className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
        />
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Deskripsi singkat"
          className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
        />
        <div className="relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 text-sm">Rp</span>
          <input
            type="number"
            min={0}
            value={price}
            onChange={(e) => setPrice(e.target.value)}
            placeholder="Harga paket"
            className="w-full pl-9 pr-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
          />
        </div>
      </div>

      <details className="border rounded-lg px-4 py-3">
        <summary className="text-sm font-medium cursor-pointer">
          Pilih produk ({Object.keys(picked).length} dipilih)
          {itemsTotal > 0 && (
            <span className="ml-2 text-xs text-gray-400">
              total normal: {formatIDR(itemsTotal)}
              {Number(price) > 0 && Number(price) < itemsTotal && (
                <span className="text-green-600">
                  {' '}· hemat {Math.round((1 - Number(price) / itemsTotal) * 100)}%
                </span>
              )}
            </span>
          )}
        </summary>
        <div className="mt-3 max-h-56 overflow-y-auto divide-y divide-gray-100 dark:divide-gray-800">
          {variants.map((v) => (
            <label key={v.id} className="flex items-center gap-2 py-1.5 text-sm cursor-pointer">
              <input
                type="checkbox"
                checked={v.id in picked}
                onChange={() => toggleVariant(v.id, v.price)}
                className="accent-amber-500"
              />
              <span className="flex-1 truncate">{v.label}</span>
            </label>
          ))}
        </div>
      </details>

      <button
        type="button"
        onClick={() => create.mutate()}
        disabled={create.isPending || !name.trim() || !Number(price) || Object.keys(picked).length === 0}
        className="px-5 py-2.5 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
      >
        {create.isPending ? 'Menyimpan...' : 'Buat Paket'}
      </button>

      {(mine ?? []).length > 0 && (
        <div className="pt-2 border-t border-gray-100 dark:border-gray-800 space-y-2">
          <p className="text-xs uppercase tracking-wide text-gray-400 font-medium">Paket aktif</p>
          {mine!.map((b) => (
            <div key={b.id} className="flex items-center justify-between text-sm">
              <div className="min-w-0">
                <p className="font-medium truncate">{b.name}</p>
                <p className="text-xs text-gray-400">{b.items.length} produk · {formatIDR(b.price)}</p>
              </div>
              <span className={`px-2 py-0.5 rounded-full text-xs ${b.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500'}`}>
                {b.is_active ? 'tayang' : 'nonaktif'}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
