import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { Product } from '../types'
import { formatIDR, etaLabel, slugify } from '../lib/format'
import { Rating } from '../components/Rating'
import { ProductCard, CompareBar } from '../components/ProductCard'
import { ShareButton } from '../components/ShareButton'
import { Seo } from '../components/Seo'
import { Modal } from '../components/Modal'
import { QueryState } from '../components/QueryState'
import { Countdown } from '../components/Countdown'
import { useSession } from '../stores/session'
import { useActiveFlashSale } from '../lib/flashSale'


interface Review {
  id: string
  user_name: string
  rating: number
  title: string
  content: string
  images?: string[]
  helpful_count?: number
  is_verified_purchase?: boolean
  created_at: string
}

interface CustomerPhoto {
  url: string
  rating: number
  user_name: string
  review_id: string
}

interface RatingCount {
  rating: number
  count: number
}

interface QA {
  id: string
  question: string
  answer: string
  answered_at?: string
  ask_user_name: string
}

function JsonLd({ data }: { data: Record<string, unknown> }) {
  // Escape "<" so seller-controlled text (name/description) can't break out
  // of the ld+json <script> element via "</script><script>...".
  const json = JSON.stringify(data).replace(/</g, '\\u003c')
  return <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: json }} />
}

export function ProductPage() {
  const { slug } = useParams()
  const navigate = useNavigate()
  const { user } = useSession()
  const queryClient = useQueryClient()
  const { data: flashSale } = useActiveFlashSale()
  const [variantId, setVariantId] = useState<string | null>(null)
  const [qty, setQty] = useState(1)
  const [notice, setNotice] = useState('')
  const [qaQuestion, setQaQuestion] = useState('')
  const [alertTarget, setAlertTarget] = useState('')
  const [reportOpen, setReportOpen] = useState(false)
  const [reportReason, setReportReason] = useState('fake')
  const [reportDesc, setReportDesc] = useState('')

  const productQuery = useQuery({
    queryKey: ['product', slug],
    queryFn: async () => (await api.get<{ product: Product }>(`/products/${slug}`)).data.product,
  })
  const { data } = productQuery

  // `selected` is the single source of truth for "which variant is this page
  // acting on", and it is declared HERE — before every mutation that needs it,
  // and before the early return that guards the render.
  //
  // It used to be declared at line 286, after the addToCart / buyNow /
  // addToWishlist mutations, and those mutations read the raw `variantId` state
  // instead. `variantId` starts as `null` and is only ever set by clicking a
  // variant chip, so on a product the buyer never clicks — i.e. every
  // single-variant product — `selected` fell back to `variants[0]`, the
  // "+ Keranjang" button rendered ENABLED (it tested `!selected`), and the one
  // chip rendered as already-selected (`!variantId && v.id === selected?.id`).
  // Clicking it then ran `if (!variantId) throw new Error('Pilih varian dulu')`
  // inside `mutationFn`, which rejects with no `onError` attached, so the
  // request never left the browser and the user saw nothing happen. Ten of the
  // thirty seeded products are single-variant, so add-to-cart was broken on a
  // third of the catalogue.
  const product = data
  const selected = product?.variants?.find((v) => v.id === variantId) ?? product?.variants?.[0] ?? null
  const selectedId = selected?.id ?? null


  const { data: related } = useQuery({
    queryKey: ['related', data?.id],
    queryFn: async () => (await api.get<{ products: Product[] }>(`/products/${data!.id}/related`)).data.products,
    enabled: !!data,
  })

  const [reviewSort, setReviewSort] = useState('helpful')
  const { data: reviews } = useQuery({
    queryKey: ['reviews', data?.id, reviewSort],
    queryFn: async () =>
      (
        await api.get<{ reviews: Review[]; distribution: RatingCount[]; total: number }>(
          `/products/${data!.id}/reviews?sort=${reviewSort}`,
        )
      ).data,
    enabled: !!data,
  })

  const { data: photos } = useQuery({
    queryKey: ['customer-photos', data?.id],
    queryFn: async () => (await api.get<{ photos: CustomerPhoto[] }>(`/products/${data!.id}/photos`)).data.photos,
    enabled: !!data,
  })

  const { data: shippingMethods } = useQuery({
    queryKey: ['shipping-methods'],
    queryFn: async () =>
      (await api.get<{ methods: { code: string; name: string; min_days: number; max_days: number }[] }>('/shipping/methods')).data.methods,
    staleTime: 5 * 60_000,
  })
  const defaultMethod = (shippingMethods ?? [])[0]

  // Review pagination ("muat lebih banyak") + helpful votes (local overlay).
  const [moreReviews, setMoreReviews] = useState<Review[]>([])
  const [nextReviewPage, setNextReviewPage] = useState(2)
  const [loadingMore, setLoadingMore] = useState(false)
  const [helpfulCounts, setHelpfulCounts] = useState<Record<string, number>>({})
  useEffect(() => {
    setMoreReviews([])
    setNextReviewPage(2)
    setHelpfulCounts({})
  }, [data?.id, reviewSort])
  const allReviews = [...(reviews?.reviews ?? []), ...moreReviews]
  const loadMoreReviews = async () => {
    if (!data || loadingMore) return
    setLoadingMore(true)
    try {
      const res = await api.get<{ reviews: Review[] }>(
        `/products/${data.id}/reviews?page=${nextReviewPage}&page_size=10&sort=${reviewSort}`,
      )
      setMoreReviews((prev) => [...prev, ...res.data.reviews])
      setNextReviewPage((p) => p + 1)
    } finally {
      setLoadingMore(false)
    }
  }
  const markHelpful = useMutation({
    mutationFn: async (id: string) =>
      (await api.post<{ helpful: boolean; helpful_count: number }>(`/products/reviews/${id}/helpful`)).data,
    onSuccess: (d, id) => setHelpfulCounts((m) => ({ ...m, [id]: d.helpful_count })),
  })

  const { data: qaList } = useQuery({
    queryKey: ['qa', data?.id],
    queryFn: async () => (await api.get<{ questions: QA[] }>(`/products/${data!.id}/qa`)).data.questions,
    enabled: !!data,
  })

  const askQA = useMutation({
    mutationFn: async () => {
      await api.post(`/products/${data!.id}/qa`, { question: qaQuestion })
      setQaQuestion('')
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['qa', data?.id] }),
  })

  const watchPrice = useMutation({
    mutationFn: async () => {
      await api.post('/price-alerts', { variant_id: selectedId, target_price: Number(alertTarget) })
      setNotice('Kami akan kabari saat harga turun!')
      setAlertTarget('')
    },
  })

  const { data: restockAlerts } = useQuery({
    queryKey: ['restock-alerts'],
    queryFn: async () => (await api.get<{ alerts: { id: string; variant_id: string }[] }>('/back-in-stock')).data.alerts,
    enabled: !!user,
  })

  const watchRestock = useMutation({
    mutationFn: async () => {
      await api.post('/back-in-stock', { variant_id: selected!.id })
    },
    onSuccess: () => {
      setNotice('Kami akan kabari saat stok tersedia!')
      queryClient.invalidateQueries({ queryKey: ['restock-alerts'] })
      setTimeout(() => setNotice(''), 2500)
    },
  })

  const reportProduct = useMutation({
    mutationFn: async () => {
      await api.post(`/products/${data!.id}/report`, {
        reason: reportReason,
        description: reportDesc,
      })
    },
    onSuccess: () => {
      setReportOpen(false)
      setReportDesc('')
      setNotice('Terima kasih! Laporanmu akan ditinjau admin.')
      setTimeout(() => setNotice(''), 4000)
    },
  })

  const [aiSummary, setAiSummary] = useState<{
    average: number
    total: number
    distribution: Record<number, number>
    summary: string
  } | null>(null)
  const [aiSummaryOpen, setAiSummaryOpen] = useState(false)
  const [activeImage, setActiveImage] = useState(0)
  const [zoomOpen, setZoomOpen] = useState(false)

  // Flash-sale deadline. The ticking itself lives in <Countdown> so the rest
  // of this 800-line page — and every ProductCard in "Produk Serupa" — no
  // longer re-renders once per second.
  const endsAtMs = flashSale?.flash_sale.ends_at ? new Date(flashSale.flash_sale.ends_at).getTime() : 0

  const loadAiSummary = useMutation({
    mutationFn: async () =>
      (await api.post<{ average: number; total: number; distribution: Record<number, number>; summary: string }>(
        '/ai/review-summary',
        { product_id: data!.id },
      )).data,
    onSuccess: setAiSummary,
  })

  useEffect(() => {
    if (!data) return
    // analytics view beacon (default variant selection is handled at render)
    api.post(`/products/${data.id}/view`).catch(() => {})
    const item = {
      id: data.id,
      name: data.name,
      slug: data.slug,
      avg_rating: data.avg_rating,
      rating_count: data.rating_count,
      sold_count: data.sold_count,
      variants: data.variants,
      images: data.images,
      viewed_at: Date.now(),
    }
    const list = (JSON.parse(localStorage.getItem('vc_recent') ?? '[]') as typeof item[]).filter(
      (p) => p.id !== data.id,
    )
    list.unshift(item)
    localStorage.setItem('vc_recent', JSON.stringify(list.slice(0, 12)))
  }, [data])

  const addToCart = useMutation({
    mutationFn: async () => {
      if (!selectedId) throw new Error('Pilih varian dulu')
      await api.post('/cart/items', { variant_id: selectedId, quantity: qty })
    },
    onSuccess: () => {
      setNotice('Ditambahkan ke keranjang!')
      queryClient.invalidateQueries({ queryKey: ['cart'] })
      setTimeout(() => setNotice(''), 2500)
    },
    // Was absent: a locally-thrown error (no variant) rejected into the void
    // and the button appeared to do nothing at all.
    onError: (e: Error) => setNotice(e.message || 'Gagal menambah ke keranjang'),
  })

  // Mount-stable idempotency key (same pattern as CheckoutPage): retries and
  // double-clicks reuse ONE key so the backend can dedupe order creation.
  const [buyNowIdemKey] = useState(() => crypto.randomUUID())

  const buyNow = useMutation({
    mutationFn: async () => {
      if (!selectedId || !user) throw new Error('Pilih varian dulu')
      return (
        await api.post<{ orders: { id: string }[] }>(
          '/checkout/buy-now',
          { variant_id: selectedId, quantity: qty },
          { headers: { 'X-Idempotency-Key': buyNowIdemKey } },
        )
      ).data
    },
    onSuccess: (placed) => {
      if (placed.orders?.length) navigate(`/orders/${placed.orders[0].id}`)
    },
    onError: (e: Error) => setNotice(e.message),
  })

  const addToWishlist = useMutation({
    mutationFn: async () => {
      if (!selectedId) throw new Error('Pilih varian dulu')
      await api.post(`/wishlist/items/${selectedId}`)
    },
    onSuccess: () => setNotice('Disimpan ke wishlist!'),
    onError: (e: Error) => setNotice(e.message),
  })

  // Derived values are computed BEFORE the early return: hooks must run
  // unconditionally, and this page used to sit on the other side of one.
  // (`product` and `selected` are declared at the top of the component now.)
  // Memoised: this array was rebuilt on every render, so the flash-sale
  // countdown's per-second tick turned into a full re-render of this 800-line
  // page including every ProductCard in "Produk Serupa".
  const gallery: string[] = useMemo(
    () =>
      Array.from(
        new Set(
          [...(product?.images ?? []).map((im) => im.url), ...(selected?.image_url ? [selected.image_url] : [])].filter(Boolean),
        ),
      ),
    [product?.images, selected?.image_url],
  )
  const image = gallery[Math.min(activeImage, Math.max(0, gallery.length - 1))]

  if (!product) {
    return <QueryState query={productQuery} label="produk" />
  }

  const watching = !!selected && !!restockAlerts?.some((a) => a.variant_id === selected.id)
  const saleItem = selected ? flashSale?.items.find((i) => i.variant_id === selected.id) : undefined
  const salePrice = saleItem?.sale_price ?? selected?.price ?? 0
  const onSale = !!saleItem && saleItem.sale_price < (selected?.price ?? 0)
  const soldPct = saleItem && saleItem.initial_stock > 0 ? Math.min(100, Math.round((saleItem.sold_count / saleItem.initial_stock) * 100)) : 0

  return (
    <>
      <Seo
        title={`${product.name} — VinCommerce`}
        description={product.description?.slice(0, 160)}
        path={`/product/${product.slug}`}
        image={gallery[0]}
      />
      <JsonLd
        data={{
          '@context': 'https://schema.org',
          '@type': 'Product',
          name: product.name,
          description: product.description?.slice(0, 300),
          image: gallery,
          brand: product.brand ? { '@type': 'Brand', name: product.brand.name } : undefined,
          sku: selected?.sku,
          offers: {
            '@type': 'Offer',
            url: window.location.href,
            priceCurrency: 'IDR',
            price: Math.round(salePrice),
            availability: (selected?.stock ?? 0) > 0 ? 'https://schema.org/InStock' : 'https://schema.org/OutOfStock',
            itemCondition: 'https://schema.org/NewCondition',
          },
          aggregateRating:
            product.rating_count > 0
              ? {
                  '@type': 'AggregateRating',
                  ratingValue: product.avg_rating,
                  reviewCount: product.rating_count,
                }
              : undefined,
        }}
      />
      {zoomOpen && image && (
        <div
          className="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4 cursor-zoom-out"
          onClick={() => setZoomOpen(false)}
        >
          <img src={image} alt={product.name} className="max-h-full max-w-full object-contain rounded-xl" />
          {gallery.length > 1 && (
            <div className="absolute bottom-6 flex gap-2">
              {gallery.map((url, i) => (
                <button type="button"
                  key={url + i}
                  onClick={(e) => {
                    e.stopPropagation()
                    setActiveImage(i)
                  }}
                  className={`w-14 h-14 rounded-lg overflow-hidden border-2 ${
                    i === activeImage ? 'border-amber-400' : 'border-transparent opacity-60'
                  }`}
                >
                  <img src={url} alt="" className="w-full h-full object-cover" />
                </button>
              ))}
            </div>
          )}
          <button type="button"
            onClick={() => setZoomOpen(false)}
            aria-label="Tutup"
            className="absolute top-4 right-4 text-white text-3xl leading-none"
          >
            ×
          </button>
        </div>
      )}
      <div className="mx-auto max-w-7xl px-4 py-6 space-y-10">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-8 bg-white border border-gray-200 rounded-2xl p-6">
        <div className="space-y-3">
          <div
            className="aspect-square rounded-xl bg-gray-100 overflow-hidden cursor-zoom-in group relative"
            onClick={() => setZoomOpen(true)}
          >
            {gallery[activeImage] ? (
              <img src={gallery[activeImage]} alt={product.name} className="w-full h-full object-cover transition-transform duration-300 group-hover:scale-105" />
            ) : (
              <div className="w-full h-full flex items-center justify-center text-gray-400">{product.name}</div>
            )}
          </div>
          {gallery.length > 1 && (
            <div className="flex gap-2 overflow-x-auto pb-1">
              {gallery.map((url, i) => (
                <button type="button"
                  key={url + i}
                  onClick={() => setActiveImage(i)}
                  className={`w-16 h-16 shrink-0 rounded-lg overflow-hidden border-2 transition-colors ${
                    i === activeImage ? 'border-amber-500' : 'border-transparent opacity-70 hover:opacity-100'
                  }`}
                >
                  <img src={url} alt={`${product.name} ${i + 1}`} className="w-full h-full object-cover" />
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="space-y-4">
          <div>
            <h1 className="text-xl md:text-2xl font-bold">{product.name}</h1>
            <div className="flex items-center gap-3 mt-2 text-sm">
              <Rating value={product.avg_rating} count={product.rating_count} />
              <span className="text-gray-500">{product.sold_count} terjual</span>
              {product.brand && <span className="text-gray-500">{product.brand.name}</span>}
            </div>
            {product.seller && (
              // `/search?category=` expects a SLUG. It was being handed
              // `product.category?.name`, so the filter matched nothing and
              // the page came back with 0 results. `slug` is optional on the
              // API payload, so fall back to slugify(name) rather than
              // producing an empty `?category=` that searches for "".
              <Link
                to={`/search?category=${encodeURIComponent(product.category?.slug ?? slugify(product.category?.name ?? ''))}`}
                className="text-xs text-gray-500 mt-1 inline-block"
              >
                Dijual oleh <span className="text-amber-600">{product.seller.name}</span>
              </Link>
            )}
            {product.seller && (
              <div className="mt-2">
                <ShareButton
                  title={product.name}
                  className="px-3 py-1.5 rounded-full border border-gray-300 text-xs hover:border-amber-400 hover:text-amber-600"
                />
              </div>
            )}
          </div>

          <div className="bg-gray-50 rounded-xl p-4">
            <div className="flex items-baseline gap-3">
              <p className={`text-2xl font-extrabold ${onSale ? 'text-red-600' : 'text-amber-600'}`}>
                {formatIDR(salePrice)}
              </p>
              {(selected?.compare_at_price && selected.compare_at_price > salePrice) || onSale ? (
                <p className="text-sm text-gray-400 line-through">{formatIDR(selected?.price ?? 0)}</p>
              ) : null}
            </div>
            {onSale && (
              <div className="mt-3 space-y-1">
                <div className="flex items-center gap-2">
                  <span className="px-2 py-0.5 rounded-lg bg-red-600 text-white text-xs font-bold">
                    ⚡ Flash Sale
                  </span>
                  <Countdown to={endsAtMs} className="text-sm text-red-600 font-bold" />
                </div>
                <div className="h-2 bg-gray-200 rounded-full overflow-hidden">
                  <div className="h-full bg-red-500" style={{ width: `${soldPct}%` }} />
                </div>
                <p className="text-xs text-gray-500">
                  {saleItem?.sold_count ?? 0} dari {saleItem?.initial_stock ?? 0} terjual
                </p>
              </div>
            )}
          </div>

          {product.variants && product.variants.length > 0 && (
            <div>
              <p className="text-sm font-medium mb-2">Pilih varian: {selected?.name}</p>
              <div className="flex flex-wrap gap-2">
                {product.variants.map((v) => (
                  <button type="button"
                    key={v.id}
                    onClick={() => setVariantId(v.id)}
                    disabled={!v.is_active || v.stock === 0}
                    aria-pressed={v.id === selectedId}
                    className={`px-4 py-2 rounded-lg border text-sm ${
                      v.id === selectedId
                        ? 'border-amber-500 bg-amber-50 text-amber-700'
                        : 'border-gray-300 hover:border-amber-400'
                    } disabled:opacity-40 disabled:cursor-not-allowed`}
                  >
                    {v.name}
                  </button>
                ))}
              </div>
              <p className="text-xs text-gray-500 mt-2">
                Stok: {selected?.stock ?? 0} unit
              </p>
              {selected && selected.stock > 0 && (
                <p className="text-xs text-teal-600 mt-1">
                  🚚 Estimasi tiba <b>{etaLabel(defaultMethod?.min_days ?? 3, defaultMethod?.max_days ?? 7)}</b> ({defaultMethod?.name ?? 'Reguler'})
                </p>
              )}
            </div>
          )}

          <div className="flex items-center gap-3">
            <div className="flex items-center border rounded-lg">
              <button
                type="button"
                aria-label="Kurangi jumlah"
                onClick={() => setQty(Math.max(1, qty - 1))}
                className="px-3 py-2 text-gray-500 hover:text-gray-900"
              >
                −
              </button>
              <span className="w-10 text-center">{qty}</span>
              <button type="button" aria-label="Tambah jumlah" onClick={() => setQty(qty + 1)} className="px-3 py-2 text-gray-500 hover:text-gray-900">
                +
              </button>
            </div>
            {selected?.stock === 0 ? (
              user ? (
                watching ? (
                  <button type="button"
                    disabled
                    className="flex-1 py-3 rounded-xl bg-green-100 text-green-700 font-semibold cursor-default"
                  >
                    ✓ Menunggu stok tersedia
                  </button>
                ) : (
                  <button type="button"
                    onClick={() => watchRestock.mutate()}
                    disabled={watchRestock.isPending}
                    className="flex-1 py-3 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white font-semibold hover:bg-gray-700 disabled:opacity-50"
                  >
                    🔔 Beri tahu saya saat tersedia
                  </button>
                )
              ) : (
                <Link
                  to="/login"
                  className="flex-1 py-3 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white font-semibold text-center"
                >
                  🔔 Beri tahu saya saat tersedia
                </Link>
              )
            ) : (
              <>
                <button type="button"
                  onClick={() => addToCart.mutate()}
                  disabled={!selected || addToCart.isPending}
                  className="flex-1 py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-40"
                >
                  + Keranjang
                </button>
                {user && (
                  <button type="button"
                    onClick={() => buyNow.mutate()}
                    disabled={!selected || buyNow.isPending}
                    className="flex-1 py-3 rounded-xl bg-red-600 text-white font-semibold hover:bg-red-700 disabled:opacity-40"
                    title="Langsung checkout tanpa masuk keranjang"
                  >
                    {buyNow.isPending ? 'Memproses...' : '⚡ Beli Sekarang'}
                  </button>
                )}
              </>
            )}
            {user && (
              <button type="button"
                onClick={() => addToWishlist.mutate()}
                className="px-4 py-3 rounded-xl border border-gray-300 hover:border-amber-400 hover:text-amber-600"
                title="Simpan ke wishlist"
              >
                ♥
              </button>
            )}
          </div>
          {notice && <p className="text-sm text-green-600">{notice}</p>}
          {!user && (
            <p className="text-xs text-gray-500">
              <Link to="/login" className="text-amber-600 underline">Masuk</Link> untuk menyimpan ke wishlist.
            </p>
          )}
          {user && user.id !== product.seller_id && (
            <button type="button"
              onClick={() => setReportOpen(true)}
              className="text-xs text-gray-400 hover:text-red-500"
            >
              🚩 Laporkan produk
            </button>
          )}
        </div>
      </div>

      <Modal
        open={reportOpen}
        onClose={() => setReportOpen(false)}
        title="Laporkan Produk"
        description="Pilih alasan pelaporan. Admin akan meninjau laporanmu."
        footer={
          <>
            <button
              type="button"
              onClick={() => setReportOpen(false)}
              className="px-4 py-2 rounded-xl border text-sm"
            >
              Batal
            </button>
            <button
              type="button"
              onClick={() => reportProduct.mutate()}
              disabled={reportProduct.isPending}
              className="px-4 py-2 rounded-xl bg-red-600 text-white text-sm disabled:opacity-50"
            >
              {reportProduct.isPending ? 'Mengirim...' : 'Kirim Laporan'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          <div>
            <label htmlFor="report-reason" className="block text-sm font-medium mb-1">Alasan</label>
            <select
              id="report-reason"
              value={reportReason}
              onChange={(e) => setReportReason(e.target.value)}
              className="w-full px-3 py-2.5 border rounded-xl text-sm outline-none dark:bg-gray-800"
            >
              <option value="fake">Barang palsu / tiruan</option>
              <option value="prohibited">Barang terlarang</option>
              <option value="copyright">Pelanggaran hak cipta</option>
              <option value="misleading">Informasi menyesatkan</option>
              <option value="other">Lainnya</option>
            </select>
          </div>
          <div>
            <label htmlFor="report-desc" className="block text-sm font-medium mb-1">Penjelasan (opsional)</label>
            <textarea
              id="report-desc"
              value={reportDesc}
              onChange={(e) => setReportDesc(e.target.value)}
              placeholder="Penjelasan singkat"
              rows={3}
              className="w-full px-3 py-2.5 border rounded-xl text-sm outline-none dark:bg-gray-800 resize-none"
            />
          </div>
          {reportProduct.isError && (
            <p role="alert" className="text-sm text-red-600">{reportProduct.error.message}</p>
          )}
        </div>
      </Modal>


      <div className="bg-white border border-gray-200 rounded-2xl p-6">
        <h2 className="font-bold mb-3">Deskripsi Produk</h2>
        <p className="text-sm text-gray-700 whitespace-pre-line">{product.description || 'Tidak ada deskripsi.'}</p>
      </div>

      {user && (
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6">
          <div className="flex items-center justify-between mb-3">
            <h2 className="font-bold">✨ Ringkasan AI</h2>
            {!aiSummary && !loadAiSummary.isPending && (
              <button type="button"
                onClick={() => {
                  setAiSummaryOpen(true)
                  loadAiSummary.mutate()
                }}
                className="px-4 py-2 rounded-lg bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-xs hover:bg-gray-800"
              >
                Buat Ringkasan
              </button>
            )}
          </div>
          {loadAiSummary.isPending && <p className="text-sm text-gray-500">Menyusun ringkasan ulasan...</p>}
          {aiSummary && (
            <div className="space-y-2">
              <p className="text-sm text-gray-700 dark:text-gray-200">{aiSummary.summary}</p>
              <p className="text-xs text-gray-500">
                Rata-rata {aiSummary.average}★ dari {aiSummary.total} ulasan
              </p>
              <div className="flex gap-1.5">
                {[5, 4, 3, 2, 1].map((star) => (
                  <span
                    key={star}
                    className="px-2 py-1 rounded-lg bg-amber-50 dark:bg-amber-900/20 text-amber-700 text-xs"
                  >
                    {star}★ {aiSummary.distribution[star] ?? 0}
                  </span>
                ))}
              </div>
            </div>
          )}
          {aiSummaryOpen && !aiSummary && !loadAiSummary.isPending && (
            <p className="text-xs text-red-500">Gagal menyusun ringkasan. Coba lagi.</p>
          )}
        </div>
      )}

      {photos && photos.length > 0 && (
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6">
          <h2 className="font-bold mb-3">Foto Pembeli ({photos.length})</h2>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {photos.map((p, i) => (
              <button type="button"
                key={p.review_id + i}
                onClick={() => {
                  setActiveImage(0)
                  setZoomOpen(true)
                }}
                className="relative w-20 h-20 shrink-0 rounded-lg overflow-hidden group"
                title={`${'★'.repeat(p.rating)} oleh ${p.user_name}`}
              >
                <img src={p.url} alt={`Foto pembeli ${i + 1}`} loading="lazy" className="w-full h-full object-cover" />
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="font-bold">Ulasan ({reviews?.total ?? 0})</h2>
          <select
            value={reviewSort}
            onChange={(e) => setReviewSort(e.target.value)}
            className="px-3 py-1.5 border rounded-lg text-xs outline-none dark:bg-gray-800"
            aria-label="Urutkan ulasan"
          >
            <option value="helpful">Paling Membantu</option>
            <option value="recent">Terbaru</option>
            <option value="rating_desc">Bintang Tertinggi</option>
            <option value="rating_asc">Bintang Terendah</option>
            <option value="with_images">Dengan Foto</option>
          </select>
        </div>
        {reviews?.distribution && reviews.distribution.length > 0 && (
          <div className="flex items-center gap-6 mb-6">
            <div className="text-center">
              <p className="text-4xl font-extrabold text-amber-600">{product.avg_rating.toFixed(1)}</p>
              <p className="text-xs text-gray-500">{product.rating_count} ulasan</p>
            </div>
            <div className="flex-1 space-y-1">
              {[5, 4, 3, 2, 1].map((star) => {
                const bucket = reviews.distribution.find((d) => d.rating === star)
                const total = reviews.distribution.reduce((s, d) => s + d.count, 0) || 1
                return (
                  <div key={star} className="flex items-center gap-2 text-xs">
                    <span className="w-8 text-gray-500">{star}★</span>
                    <div className="flex-1 h-2 bg-gray-100 dark:bg-gray-800 rounded-full overflow-hidden">
                      <div
                        className="h-full bg-amber-400 rounded-full"
                        style={{ width: `${((bucket?.count ?? 0) / total) * 100}%` }}
                      />
                    </div>
                    <span className="w-6 text-gray-400">{bucket?.count ?? 0}</span>
                  </div>
                )
              })}
            </div>
          </div>
        )}
        <div className="space-y-4">
          {allReviews.map((r) => (
            <div key={r.id} className="border-b border-gray-100 dark:border-gray-700 pb-4 last:border-0">
              <div className="flex items-center justify-between">
                <p className="font-medium text-sm">
                  {r.user_name}
                  {r.is_verified_purchase && (
                    <span className="ml-2 px-1.5 py-0.5 rounded bg-green-100 text-green-700 text-[10px] font-semibold">
                      ✓ Pembeli Terverifikasi
                    </span>
                  )}
                </p>
                <Rating value={r.rating} size="text-xs" />
              </div>
              {r.title && <p className="text-sm mt-1">{r.title}</p>}
              <p className="text-sm text-gray-600 dark:text-gray-300 mt-1">{r.content}</p>
              {r.images && r.images.length > 0 && (
                <div className="flex gap-2 mt-2">
                  {r.images.map((img, i) => (
                    <img
                      key={i}
                      src={img}
                      alt=""
                      loading="lazy"
                      className="w-16 h-16 rounded-lg object-cover bg-gray-100 border border-gray-200"
                    />
                  ))}
                </div>
              )}
              {user && (
                <button type="button"
                  onClick={() => markHelpful.mutate(r.id)}
                  disabled={markHelpful.isPending}
                  className="mt-2 text-xs text-gray-400 hover:text-amber-600"
                >
                  👍 Membantu ({helpfulCounts[r.id] ?? r.helpful_count ?? 0})
                </button>
              )}
            </div>
          ))}
          {(reviews?.total ?? 0) > allReviews.length && (
            <button type="button"
              onClick={loadMoreReviews}
              disabled={loadingMore}
              className="w-full py-2.5 rounded-xl border text-sm hover:border-amber-400 disabled:opacity-50"
            >
              {loadingMore ? 'Memuat...' : `Muat ulasan lainnya (${(reviews?.total ?? 0) - allReviews.length} lagi)`}
            </button>
          )}
          {reviews?.reviews.length === 0 && <p className="text-sm text-gray-500">Belum ada ulasan.</p>}
        </div>
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6">
        <h2 className="font-bold mb-4">Tanya Jawab ({qaList?.length ?? 0})</h2>
        <div className="space-y-4">
          {qaList?.map((q) => (
            <div key={q.id} className="border-b border-gray-100 dark:border-gray-700 pb-4 last:border-0">
              <p className="text-sm font-medium">❓ {q.question}</p>
              {q.answer ? (
                <p className="text-sm text-gray-600 dark:text-gray-300 mt-1">✅ {q.answer}</p>
              ) : (
                <p className="text-xs text-gray-400 mt-1">Menunggu jawaban penjual...</p>
              )}
            </div>
          ))}
          {user && (
            <div className="flex gap-2">
              <input
                value={qaQuestion}
                onChange={(e) => setQaQuestion(e.target.value)}
                placeholder="Tanyakan ke penjual..."
                className="flex-1 px-4 py-2.5 border rounded-xl text-sm outline-none focus:border-amber-400 dark:bg-gray-800"
              />
              <button type="button"
                onClick={() => askQA.mutate()}
                disabled={askQA.isPending || !qaQuestion.trim()}
                className="px-4 py-2.5 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-sm disabled:opacity-50"
              >
                Tanya
              </button>
            </div>
          )}
        </div>
      </div>

      {user && (
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6">
          <h2 className="font-bold mb-3">🔔 Pantau Harga</h2>
          <div className="flex gap-2">
            <input
              type="number"
              value={alertTarget}
              onChange={(e) => setAlertTarget(e.target.value)}
              placeholder="Beri tahu saya jika harga di bawah..."
              className="flex-1 px-4 py-2.5 border rounded-xl text-sm outline-none focus:border-amber-400 dark:bg-gray-800"
            />
            <button type="button"
              onClick={() => watchPrice.mutate()}
              disabled={watchPrice.isPending || !alertTarget}
              className="px-4 py-2.5 rounded-xl bg-amber-500 text-white text-sm disabled:opacity-50"
            >
              Aktifkan
            </button>
          </div>
        </div>
      )}

      {related && related.length > 0 && (
        <section>
          <h2 className="font-bold mb-4">Produk Serupa</h2>
          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
            {related.map((p) => <ProductCard key={p.id} product={p} />)}
          </div>
        </section>
      )}

      <CompareBar />
    </div>
    </>
  )
}


