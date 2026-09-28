import { memo, useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { Product } from '../types'
import { formatIDR } from '../lib/format'
import { Rating } from './Rating'
import { useSession } from '../stores/session'
import { api } from '../lib/api'
import { useActiveFlashSale } from '../lib/flashSale'
import { addCompareItem, compareIds, readCompareCount, subscribeCompare } from '../lib/compare'

function ProductCardImpl({ product }: { product: Product }) {
  const { user } = useSession()
  const queryClient = useQueryClient()
  const [saved, setSaved] = useState(false)
  const [compareMsg, setCompareMsg] = useState('')
  const { data: flashSale } = useActiveFlashSale()
  const price = product.variants?.[0]?.price ?? 0
  const compareAt = product.variants?.[0]?.compare_at_price
  const image = product.images?.[0]?.url
  const variantId = product.variants?.[0]?.id

  const saleItem = variantId ? flashSale?.items.find((i) => i.variant_id === variantId) : undefined
  const salePrice = saleItem?.sale_price ?? price
  const onSale = !!saleItem && saleItem.sale_price < price
  const discountPct = onSale ? Math.round((1 - salePrice / price) * 100) : 0

  const wishlist = useMutation({
    mutationFn: async () => {
      if (!variantId) return
      if (saved) {
        await api.delete(`/wishlist/items/${variantId}`)
      } else {
        await api.post(`/wishlist/items/${variantId}`)
      }
      setSaved(!saved)
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['wishlist'] }),
  })

  const addCompare = () => {
    if (compareIds().includes(product.id)) {
      setCompareMsg('Sudah ada')
      return
    }
    if (compareIds().length >= 4) {
      setCompareMsg('Penuh (maks 4)')
      return
    }
    addCompareItem(product)
    setCompareMsg('Ditambahkan ✓')
  }

  return (
    <div className="group relative flex flex-col bg-white dark:bg-gray-900 rounded-xl border border-gray-200 dark:border-gray-700 overflow-hidden hover:shadow-lg hover:-translate-y-0.5 transition-all">
      {user && variantId && (
        <button
          type="button"
          onClick={() => wishlist.mutate()}
          aria-label={saved ? 'Hapus dari wishlist' : 'Simpan ke wishlist'}
          className="absolute top-2 right-2 z-10 w-8 h-8 rounded-full bg-white/90 dark:bg-gray-800 shadow flex items-center justify-center hover:scale-110 transition-transform"
          title={saved ? 'Hapus dari wishlist' : 'Simpan ke wishlist'}
        >
          <span className={saved ? 'text-red-500' : 'text-gray-400'}>♥</span>
        </button>
      )}
      <Link to={`/product/${product.slug}`} className="flex flex-col flex-1">
        <div className="aspect-square bg-gray-100 overflow-hidden relative">
          {onSale && (
            <span className="absolute top-2 left-2 z-10 px-2 py-0.5 rounded-lg bg-red-600 text-white text-xs font-bold">
              ⚡ -{discountPct}%
            </span>
          )}
          {image ? (
            <img
              src={image}
              alt={product.name}
              loading="lazy"
              className="w-full h-full object-cover group-hover:scale-105 transition-transform"
            />
          ) : (
            <div className="w-full h-full flex items-center justify-center text-gray-400 text-sm">
              {product.name.slice(0, 24)}
            </div>
          )}
        </div>
        <div className="p-3 flex flex-col gap-1 flex-1">
          <p className="text-sm text-gray-700 dark:text-gray-200 line-clamp-2 min-h-[2.5rem]">{product.name}</p>
          <div className="flex items-baseline gap-2">
            <span className={`font-bold ${onSale ? 'text-red-600' : 'text-amber-600'}`}>{formatIDR(salePrice)}</span>
            {(compareAt && compareAt > price) || onSale ? (
              <span className="text-xs text-gray-400 line-through">{formatIDR(price)}</span>
            ) : null}
          </div>
          <div className="flex items-center justify-between text-xs text-gray-500">
            <Rating value={product.avg_rating} count={product.rating_count} />
            <span>{product.sold_count} terjual</span>
          </div>
        </div>
      </Link>
      {/* Compare lives OUTSIDE the card <Link>: a button nested inside a link
          is invalid HTML — clicking it navigated instead of comparing. */}
      <button
        type="button"
        onClick={addCompare}
        className="text-[11px] text-gray-400 hover:text-amber-600 text-left px-3 pb-2 -mt-1"
        title="Tambahkan ke perbandingan"
      >
        ⚖ Bandingkan{compareMsg ? ` · ${compareMsg}` : ''}
      </button>
    </div>
  )
}

/**
 * Memoised so an unrelated parent re-render — a countdown tick, a parent
 * state change — does not re-render every card in a 20-item grid.
 */
export const ProductCard = memo(ProductCardImpl)

/**
 * Floating shortcut to /compare. Compare wrote to localStorage but nothing
 * ever linked to the route, so the comparison table was unreachable.
 */
export function CompareBar() {
  const [count, setCount] = useState(() => readCompareCount())
  const { pathname } = useLocation()

  useEffect(() => subscribeCompare(setCount), [])

  if (count === 0 || pathname === '/compare') return null

  return (
    <div
      role="status"
      className="fixed bottom-4 left-1/2 -translate-x-1/2 z-40 flex items-center gap-3 rounded-full bg-gray-900 dark:bg-gray-100 px-4 py-2.5 text-sm text-white dark:text-gray-900 shadow-lg"
    >
      <span>
        ⚖ {count} produk dipilih
      </span>
      <Link to="/compare" className="rounded-full bg-amber-500 px-3 py-1 text-xs font-semibold text-white">
        Bandingkan ({count})
      </Link>
    </div>
  )
}
