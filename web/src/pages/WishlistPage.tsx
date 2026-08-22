import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import type { Product } from '../types'
import { ProductCard } from '../components/ProductCard'

export function WishlistPage() {
  const { user } = useSession()
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['wishlist'],
    queryFn: async () => (await api.get<{ products: Product[] }>('/wishlist')).data.products,
    enabled: !!user,
  })

  const remove = useMutation({
    mutationFn: async (variantId: string) => api.delete(`/wishlist/items/${variantId}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['wishlist'] }),
  })

  const addAll = useMutation({
    mutationFn: async () => {
      for (const p of data ?? []) {
        const variantId = p.variants?.[0]?.id
        if (variantId) {
          await api.post('/cart/items', { variant_id: variantId, quantity: 1 })
        }
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['cart'] }),
  })

  if (!user) return <Navigate to="/login" replace />

  return (
    <div className="mx-auto max-w-7xl px-4 py-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-bold">Wishlist</h1>
        {data && data.length > 0 && (
          <button
            onClick={() => addAll.mutate()}
            disabled={addAll.isPending}
            className="px-4 py-2 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
          >
            {addAll.isPending ? 'Menambahkan...' : '🛒 Tambah Semua ke Keranjang'}
          </button>
        )}
      </div>
      {data?.length === 0 && (
        <div className="text-center py-16">
          <p className="text-gray-500 mb-4">Wishlist kosong.</p>
          <Link to="/search" className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
            Jelajahi Produk
          </Link>
        </div>
      )}
      <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
        {data?.map((p) => (
          <div key={p.id} className="relative">
            <ProductCard product={p} />
            {p.variants?.[0] && (
              <button
                onClick={() => remove.mutate(p.variants![0].id)}
                className="absolute top-2 right-2 bg-white rounded-full w-7 h-7 shadow flex items-center justify-center text-red-500 text-sm"
                title="Hapus dari wishlist"
              >
                ✕
              </button>
            )}
          </div>
        ))}
      </div>
      {addAll.isSuccess && (
        <p className="text-sm text-green-600 mt-4">Semua item ditambahkan ke keranjang!</p>
      )}
    </div>
  )
}
