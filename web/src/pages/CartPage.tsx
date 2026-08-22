import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { CartLine } from '../types'
import { formatIDR } from '../lib/format'
import { useSession } from '../stores/session'

export function CartPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { user } = useSession()
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const { data: lines } = useQuery({
    queryKey: ['cart'],
    queryFn: async () => (await api.get<{ lines: CartLine[] }>('/cart')).data.lines,
  })

  const update = useMutation({
    mutationFn: async ({ variantId, quantity }: { variantId: string; quantity: number }) =>
      api.put('/cart/items', { variant_id: variantId, quantity }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['cart'] }),
  })

  const remove = useMutation({
    mutationFn: async (variantId: string) => api.delete(`/cart/items/${variantId}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['cart'] }),
  })

  const bulkRemove = useMutation({
    mutationFn: async (variantIds: string[]) => api.post('/cart/bulk-remove', { variant_ids: variantIds }),
    onSuccess: () => {
      setSelected(new Set())
      queryClient.invalidateQueries({ queryKey: ['cart'] })
    },
  })

  const bulkMove = useMutation({
    mutationFn: async (variantIds: string[]) => api.post('/cart/bulk-move', { variant_ids: variantIds }),
    onSuccess: () => {
      setSelected(new Set())
      queryClient.invalidateQueries({ queryKey: ['cart'] })
      queryClient.invalidateQueries({ queryKey: ['wishlist'] })
    },
  })

  if (!lines || lines.length === 0) {
    return (
      <div className="mx-auto max-w-7xl px-4 py-20 text-center">
        <p className="text-gray-500 mb-4">Keranjangmu masih kosong.</p>
        <Link to="/search" className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
          Mulai Belanja
        </Link>
      </div>
    )
  }

  const allSelected = selected.size === lines.length
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(lines.map((l) => l.variant_id)))
  const toggle = (variantId: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(variantId)) next.delete(variantId)
      else next.add(variantId)
      return next
    })

  const selectedLines = lines.filter((l) => selected.has(l.variant_id))
  const total = lines.reduce((s, l) => s + l.subtotal, 0)
  const selectedCount = selectedLines.length

  const sellers = new Map<string, { name: string; items: CartLine[] }>()
  for (const l of lines) {
    const g = sellers.get(l.seller_id)
    if (g) g.items.push(l)
    else sellers.set(l.seller_id, { name: l.seller_name, items: [l] })
  }

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 lg:grid-cols-3 gap-6">
      <div className="lg:col-span-2 space-y-3">
        <div className="flex items-center justify-between text-sm">
          <label className="flex items-center gap-2 cursor-pointer select-none">
            <input type="checkbox" checked={allSelected} onChange={toggleAll} className="accent-amber-500" />
            <span className="font-medium">Pilih semua ({lines.length})</span>
          </label>
          {selectedCount > 0 && (
            <div className="flex gap-4">
              {user && (
                <button onClick={() => bulkMove.mutate([...selected])} className="text-blue-600 hover:underline">
                  Pindah ke wishlist ({selectedCount})
                </button>
              )}
              <button onClick={() => bulkRemove.mutate([...selected])} className="text-red-500 hover:underline">
                Hapus ({selectedCount})
              </button>
            </div>
          )}
        </div>

        {[...sellers.entries()].map(([sellerId, group]) => {
          const groupTotal = group.items.reduce((s, l) => s + l.subtotal, 0)
          return (
            <div key={sellerId} className="bg-white border border-gray-200 rounded-xl overflow-hidden">
              <div className="px-4 py-2.5 bg-gray-50 dark:bg-gray-800 border-b border-gray-200 flex items-center justify-between">
                <p className="text-sm font-semibold">🏪 {group.name}</p>
                <p className="text-xs text-gray-500">Subtotal toko: <b>{formatIDR(groupTotal)}</b></p>
              </div>
              <div className="divide-y divide-gray-100 dark:divide-gray-800">
                {group.items.map((l) => (
                  <div key={l.variant_id} className="p-4 flex gap-4 items-center">
                    <input
                      type="checkbox"
                      checked={selected.has(l.variant_id)}
                      onChange={() => toggle(l.variant_id)}
                      className="accent-amber-500"
                    />
                    {l.image_url ? (
                      <img src={l.image_url} alt="" className="w-20 h-20 rounded-lg object-cover bg-gray-100" />
                    ) : (
                      <div className="w-20 h-20 rounded-lg bg-gray-100 flex items-center justify-center text-xs text-gray-400">
                        {l.product_name.slice(0, 10)}
                      </div>
                    )}
                    <div className="flex-1">
                      <Link to={`/product/${l.product_slug ?? l.product_id}`} className="font-medium text-sm hover:text-amber-600 line-clamp-1">
                        {l.product_name}
                      </Link>
                      <p className="text-xs text-gray-500 mt-0.5">
                        {l.variant_name} · dari {l.seller_name}
                      </p>
                      <div className="flex items-center justify-between mt-3">
                        <div className="flex items-center border rounded-lg">
                          <button
                            onClick={() => update.mutate({ variantId: l.variant_id, quantity: l.quantity - 1 })}
                            className="px-3 py-1.5 text-gray-500 hover:text-gray-900"
                          >
                            −
                          </button>
                          <span className="w-8 text-center text-sm">{l.quantity}</span>
                          <button
                            onClick={() => update.mutate({ variantId: l.variant_id, quantity: l.quantity + 1 })}
                            className="px-3 py-1.5 text-gray-500 hover:text-gray-900"
                          >
                            +
                          </button>
                        </div>
                        <div className="flex items-center gap-3">
                          <p className="font-bold text-amber-600">{formatIDR(l.subtotal)}</p>
                          <button onClick={() => remove.mutate(l.variant_id)} className="text-xs text-red-500 hover:underline">
                            Hapus
                          </button>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )
        })}
      </div>

      <aside className="h-fit bg-white border border-gray-200 rounded-xl p-5 space-y-3">
        <h2 className="font-bold">Ringkasan</h2>
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Subtotal ({lines.length} item)</span>
          <span className="font-medium">{formatIDR(total)}</span>
        </div>
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Ongkir</span>
          <span className="text-gray-400">Dihitung saat checkout</span>
        </div>
        <hr />
        <div className="flex justify-between font-bold">
          <span>Total</span>
          <span className="text-amber-600">{formatIDR(total)}</span>
        </div>
        <button
          onClick={() => navigate('/checkout')}
          className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600"
        >
          Checkout
        </button>
        {selectedCount > 0 && (
          <p className="text-xs text-gray-400">
            {selectedCount} item dipilih — gunakan aksi di atas untuk menghapus atau memindahkan ke wishlist.
          </p>
        )}
      </aside>
    </div>
  )
}
