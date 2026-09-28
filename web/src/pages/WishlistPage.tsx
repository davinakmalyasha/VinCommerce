import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import type { Product } from '../types'
import { ProductCard } from '../components/ProductCard'
import { QueryState } from '../components/QueryState'

export function WishlistPage() {
  const { user } = useSession()
  const queryClient = useQueryClient()

  const wishlistQuery = useQuery({
    queryKey: ['wishlist'],
    queryFn: async () => (await api.get<{ products: Product[] }>('/wishlist')).data.products,
    enabled: !!user,
  })
  const data = wishlistQuery.data
  const [error, setError] = useState('')

  const remove = useMutation({
    mutationFn: async (variantId: string) => api.delete(`/wishlist/items/${variantId}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['wishlist'] }),
    onError: (e: Error) => setError(e.message || 'Gagal menghapus dari wishlist.'),
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
    onError: (e: Error) => setError(e.message || 'Gagal menambahkan ke keranjang.'),
  })

  if (!user) return <Navigate to="/login" replace />

  return (
    <div className="mx-auto max-w-7xl px-4 py-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-bold">Wishlist</h1>
        {data && data.length > 0 && (
          <button
            type="button"
            onClick={() => addAll.mutate()}
            disabled={addAll.isPending}
            className="px-4 py-2 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
          >
            {addAll.isPending ? 'Menambahkan...' : '🛒 Tambah Semua ke Keranjang'}
          </button>
        )}
      </div>
      {error && (
        <p role="alert" className="text-sm text-red-600 mb-3 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">{error}</p>
      )}
      <QueryState query={wishlistQuery} label="wishlist" className="!text-left">
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
                  type="button"
                  onClick={() => remove.mutate(p.variants![0].id)}
                  disabled={remove.isPending}
                  className="absolute top-2 right-2 bg-white rounded-full w-7 h-7 shadow flex items-center justify-center text-red-500 text-sm disabled:opacity-50"
                  title="Hapus dari wishlist"
                  aria-label={`Hapus ${p.name} dari wishlist`}
                >
                  ✕
                </button>
              )}
            </div>
          ))}
        </div>
      </QueryState>
      {addAll.isSuccess && (
        <p role="status" className="text-sm text-green-600 mt-4">Semua item ditambahkan ke keranjang!</p>
      )}
    </div>
  )
}
