import { useInfiniteQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Product } from '../types'
import { formatIDR } from '../lib/format'
import { Seo } from '../components/Seo'
import { useSession } from '../stores/session'

interface FeedItem {
  bucket: 'flash_deal' | 'followed' | 'bestseller'
  product: Pick<Product, 'id' | 'name' | 'slug'> & { image_url?: string; price?: number }
  sale_price?: number
}

const bucketMeta: Record<FeedItem['bucket'], { label: string; cls: string }> = {
  flash_deal: { label: '⚡ Flash Deal', cls: 'bg-red-100 text-red-700' },
  followed: { label: '🆕 Dari Toko Diikuti', cls: 'bg-blue-100 text-blue-700' },
  bestseller: { label: '🔥 Terlaris', cls: 'bg-amber-100 text-amber-700' },
}

const PAGE = 12

export function DiscoverFeedPage() {
  const { user } = useSession()

  const feedQuery = useInfiniteQuery({
    queryKey: ['feed', !!user],
    queryFn: async ({ pageParam }) => {
      const res = await api.get<{ items: FeedItem[] }>('/feed', {
        params: { page: pageParam, limit: PAGE },
      })
      return res.data.items
    },
    initialPageParam: 1,
    getNextPageParam: (last, all) => (last.length < PAGE ? undefined : all.length + 1),
  })

  const items = feedQuery.data?.pages.flat() ?? []
  // client-side dedupe across pages (interleave shifts)
  const seen = new Set<string>()
  const unique = items.filter((it) => {
    if (!it.product?.id || seen.has(it.product.id)) return false
    seen.add(it.product.id)
    return true
  })

  return (
    <>
      <Seo title="Temukan — VinCommerce" description="Jelajahi flash deals, produk baru dari toko yang kamu ikuti, dan bestseller." path="/discover" />
      <div className="mx-auto max-w-7xl px-4 py-6">
        <h1 className="text-2xl font-extrabold mb-1">Temukan</h1>
        <p className="text-sm text-gray-500 mb-6">
          Flash deals, produk terbaru dari toko yang kamu ikuti, dan yang paling laris — dalam satu alur.
        </p>

        <div className="columns-2 md:columns-3 lg:columns-4 gap-4 [&>*]:mb-4">
          {unique.map((it, i) => (
            <FeedCard key={`${it.bucket}-${it.product.id}-${i}`} item={it} />
          ))}
        </div>

        {feedQuery.isFetching && <p className="text-center text-sm text-gray-400 py-6">Memuat...</p>}
        {!feedQuery.hasNextPage && unique.length > 0 && (
          <p className="text-center text-xs text-gray-400 py-6">Kamu sudah melihat semuanya 🎉</p>
        )}
        {unique.length === 0 && !feedQuery.isFetching && (
          <p className="text-center text-sm text-gray-500 py-16">Belum ada apa pun di feed.</p>
        )}
        {feedQuery.hasNextPage && !feedQuery.isFetching && (
          <div className="flex justify-center pb-8">
            <button
              onClick={() => feedQuery.fetchNextPage()}
              className="px-6 py-3 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-sm font-medium hover:bg-gray-800"
            >
              Muat lebih banyak
            </button>
          </div>
        )}
      </div>
    </>
  )
}

function FeedCard({ item }: { item: FeedItem }) {
  const meta = bucketMeta[item.bucket] ?? bucketMeta.bestseller
  const price = item.sale_price ?? item.product.price
  const hasSale = item.sale_price != null && item.product.price != null && item.sale_price < item.product.price!

  return (
    <Link
      to={`/product/${item.product.slug}`}
      className="block break-inside-avoid bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden hover:shadow-md transition-shadow"
    >
      <div className="aspect-square bg-gray-100 dark:bg-gray-800 overflow-hidden relative">
        {item.product.image_url ? (
          <img src={item.product.image_url} alt={item.product.name} loading="lazy" className="w-full h-full object-cover" />
        ) : (
          <div className="w-full h-full flex items-center justify-center text-gray-300 text-3xl">🛍️</div>
        )}
      </div>
      <div className="p-3 space-y-1.5">
        <span className={`inline-block px-2 py-0.5 rounded-full text-[10px] font-semibold ${meta.cls}`}>
          {meta.label}
        </span>
        <p className="text-sm line-clamp-2">{item.product.name}</p>
        <p className={`font-bold ${hasSale ? 'text-red-600' : 'text-amber-600'}`}>{formatIDR(price ?? 0)}</p>
        {hasSale && (
          <p className="text-xs text-gray-400 line-through">{formatIDR(item.product.price!)}</p>
        )}
      </div>
    </Link>
  )
}
